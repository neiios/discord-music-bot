package voice

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/neiios/discord-music-bot/internal/api"
	"github.com/neiios/discord-music-bot/internal/downloader"
	"github.com/neiios/discord-music-bot/internal/env"
	"github.com/neiios/discord-music-bot/internal/gateway"
)

type Manager struct {
	env       env.Env
	gateway   *gateway.Connection
	apiClient *api.Client
	channelID string
	queue     *Queue

	mu          sync.Mutex
	voiceState  *gateway.VoiceState
	voiceServer *gateway.VoiceServerUpdate
	voiceConn   *Connection
	voiceReady  chan struct{}
	nowPlaying  *downloader.Song

	skipMu     sync.Mutex
	skipCancel context.CancelFunc

	loaderMu      sync.Mutex
	loaderCancels map[int64]context.CancelFunc
	loaderNextID  int64
}

func NewManager(ctx context.Context, gw *gateway.Connection, env env.Env, apiClient *api.Client) *Manager {
	m := &Manager{
		env:           env,
		gateway:       gw,
		apiClient:     apiClient,
		channelID:     env.MusicChannelId,
		queue:         NewQueue(),
		voiceReady:    make(chan struct{}),
		loaderCancels: make(map[int64]context.CancelFunc),
	}

	go m.playLoop(ctx)
	return m
}

func (m *Manager) HandlePlay(ctx context.Context, url url.URL) {
	if err := m.sendVoiceStateUpdate(ctx); err != nil {
		slog.Error("failed to request voice state", "error", err)
	}
	go m.downloadAndQueue(ctx, url)
}

func (m *Manager) HandleConnect(ctx context.Context) error {
	return m.sendVoiceStateUpdate(ctx)
}

func (m *Manager) HandleSkip() {
	m.skipMu.Lock()
	cancel := m.skipCancel
	m.skipMu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (m *Manager) HandleStop() {
	m.loaderMu.Lock()
	for _, cancel := range m.loaderCancels {
		cancel()
	}
	m.loaderCancels = make(map[int64]context.CancelFunc)
	m.loaderMu.Unlock()

	m.queue.Clear()
	m.HandleSkip()
}

func (m *Manager) HandleQueue() {
	m.mu.Lock()
	np := m.nowPlaying
	m.mu.Unlock()

	upcoming := m.queue.List()

	if np == nil && len(upcoming) == 0 {
		m.sendFeedback("**Queue is empty**")
		return
	}

	var b strings.Builder
	if np != nil {
		fmt.Fprintf(&b, "**Now playing:** %s\n\n", np.Metadata.Title)
	}
	if len(upcoming) > 0 {
		b.WriteString("-# Up next\n")
	}
	for i, song := range upcoming {
		fmt.Fprintf(&b, "%d. %s\n", i+1, song.Metadata.Title)
	}
	fmt.Fprint(&b, "\n")

	m.sendFeedback(b.String())
}

func (m *Manager) HandleVoiceStateUpdate(state gateway.VoiceState) {
	if state.UserID != m.gateway.SelfID {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.voiceState = &state
	if state.ChannelID == "" && m.voiceConn != nil {
		m.voiceConn.Close()
		m.voiceConn = nil
	}
	m.signalVoiceReady()
}

func (m *Manager) HandleVoiceServerUpdate(update gateway.VoiceServerUpdate) {
	if update.GuildID != m.env.GuildId {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.voiceServer = &update
	if m.voiceConn != nil {
		m.voiceConn.Close()
		m.voiceConn = nil
	}
	m.signalVoiceReady()
}

func (m *Manager) setNowPlaying(s *downloader.Song) {
	m.mu.Lock()
	m.nowPlaying = s
	m.mu.Unlock()
}

func (m *Manager) playLoop(ctx context.Context) {
	for {
		signal := m.queue.Signal()
		select {
		case <-ctx.Done():
			return
		case <-signal:
		}

		for {
			song, ok := m.queue.Pop()
			if !ok {
				break
			}

			m.setNowPlaying(&song)

			conn, err := m.ensureVoiceConnection(ctx)
			if err != nil {
				slog.Error("failed to establish voice connection", "error", err)
				m.setNowPlaying(nil)
				continue
			}

			packets, err := ExtractOpusPackets(song.Audio)
			if err != nil {
				slog.Error("failed to extract opus packets", "error", err)
				m.setNowPlaying(nil)
				continue
			}

			slog.Info("starting playback", "title", song.Metadata.Title, "packets", len(packets))
			m.sendFeedback(fmt.Sprintf("**Now playing:** %s", song.Metadata.Title))

			songCtx, songCancel := context.WithCancel(ctx)
			m.skipMu.Lock()
			m.skipCancel = songCancel
			m.skipMu.Unlock()

			if err := m.sendPackets(songCtx, conn, packets); err != nil {
				slog.Info("playback interrupted", "error", err, "title", song.Metadata.Title)
			}

			m.skipMu.Lock()
			m.skipCancel = nil
			m.skipMu.Unlock()
			songCancel()

			silenceFrame := GetSilenceFrame()
			for range 5 {
				select {
				case <-ctx.Done():
					return
				case conn.OpusSend <- silenceFrame:
				}
			}

			if err := conn.Speaking(false); err != nil {
				slog.Warn("failed to turn off speaking", "error", err)
			}

			m.setNowPlaying(nil)

			slog.Info("playback complete", "title", song.Metadata.Title)
		}
	}
}

func (m *Manager) sendPackets(ctx context.Context, conn *Connection, packets [][]byte) error {
	for _, packet := range packets {
		conn.RLock()
		ready := conn.Ready
		conn.RUnlock()

		if !ready {
			return fmt.Errorf("voice connection no longer ready")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case conn.OpusSend <- packet:
		}
	}
	return nil
}

func (m *Manager) ensureVoiceConnection(ctx context.Context) (*Connection, error) {
	m.mu.Lock()
	conn := m.voiceConn
	m.mu.Unlock()

	if conn != nil {
		conn.RLock()
		ready := conn.Ready
		conn.RUnlock()
		if ready {
			return conn, nil
		}
	}

	if err := m.sendVoiceStateUpdate(ctx); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for {
		m.mu.Lock()
		state := m.voiceState
		server := m.voiceServer
		existingConn := m.voiceConn
		ch := m.voiceReady
		m.mu.Unlock()

		if existingConn != nil {
			existingConn.RLock()
			ready := existingConn.Ready
			existingConn.RUnlock()
			if ready {
				return existingConn, nil
			}
		}

		if state != nil && server != nil && existingConn == nil {
			cfg := ConnectionConfig{
				UserID:      m.gateway.SelfID,
				State:       *state,
				Server:      *server,
				MainGateway: m.gateway,
			}
			conn, err := Connect(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to connect: %w", err)
			}

			if err := conn.WaitUntilConnected(10 * time.Second); err != nil {
				conn.Close()
				return nil, fmt.Errorf("connection did not become ready: %w", err)
			}

			m.mu.Lock()
			m.voiceConn = conn
			m.mu.Unlock()

			return conn, nil
		}

		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("timed out waiting for voice server/state")
		case <-ch:
		}
	}
}

// Must be called with m.mu held.
func (m *Manager) signalVoiceReady() {
	if m.voiceState != nil && m.voiceServer != nil {
		select {
		case <-m.voiceReady:
		default:
			close(m.voiceReady)
		}
	}
}

func (m *Manager) sendVoiceStateUpdate(ctx context.Context) error {
	m.mu.Lock()
	alreadyJoined := m.voiceState != nil && m.voiceState.ChannelID == m.env.VoiceChannelId
	if !alreadyJoined {
		m.voiceReady = make(chan struct{})
	}
	m.mu.Unlock()

	if alreadyJoined {
		return nil
	}

	return sendVoiceStateUpdate(ctx, m.gateway, m.env.GuildId, m.env.VoiceChannelId)
}

const maxPlaylistSize = 200

func (m *Manager) registerLoader(cancel context.CancelFunc) int64 {
	m.loaderMu.Lock()
	defer m.loaderMu.Unlock()
	id := m.loaderNextID
	m.loaderNextID++
	m.loaderCancels[id] = cancel
	return id
}

func (m *Manager) unregisterLoader(id int64) {
	m.loaderMu.Lock()
	defer m.loaderMu.Unlock()
	delete(m.loaderCancels, id)
}

func (m *Manager) downloadAndQueue(ctx context.Context, rawURL url.URL) {
	entries, err := downloader.GetPlaylistEntries(ctx, rawURL)
	if err != nil {
		slog.Error("failed to get playlist entries", "error", err, "url", rawURL)
		m.sendFeedback(fmt.Sprintf("**Failed to get metadata:** `%s`", rawURL.String()))
		return
	}

	if len(entries) == 1 {
		metadata, err := entries[0].ToMetadata()
		if err != nil {
			slog.Error("failed to convert playlist entry to metadata", "error", err)
			m.sendFeedback(fmt.Sprintf("**Failed to get metadata:** `%s`", rawURL.String()))
			return
		}
		m.downloadSingle(ctx, metadata)
		return
	}

	m.downloadPlaylist(ctx, entries)
}

func (m *Manager) downloadSingle(ctx context.Context, metadata downloader.Metadata) {
	loaderCtx, loaderCancel := context.WithCancel(ctx)
	defer loaderCancel()
	loaderID := m.registerLoader(loaderCancel)
	defer m.unregisterLoader(loaderID)

	if metadata.DurationSec > 3*60*60 {
		slog.Error("song too long", "duration", metadata.DurationSec, "title", metadata.Title)
		m.sendFeedback(fmt.Sprintf("**Song too long:** (>3h) - %s", metadata.Title))
		return
	}

	song, err := downloader.DownloadSong(loaderCtx, metadata)
	if err != nil {
		slog.Error("failed to download song", "error", err, "title", metadata.Title)
		m.sendFeedback(fmt.Sprintf("**Failed to download:** %s", metadata.Title))
		return
	}

	select {
	case <-loaderCtx.Done():
		return
	default:
	}

	pos := m.queue.Add(song)

	m.mu.Lock()
	playing := m.nowPlaying != nil
	m.mu.Unlock()

	if pos > 1 || playing {
		m.sendFeedback(fmt.Sprintf("**Queued:** %s - position #%d", song.Metadata.Title, pos))
	}

	slog.Info("queued song for playback", "title", song.Metadata.Title, "position", pos)
}

func (m *Manager) downloadPlaylist(ctx context.Context, entries []downloader.PlaylistEntry) {
	if len(entries) > maxPlaylistSize {
		m.sendFeedback(fmt.Sprintf("**Playlist trimmed** from %d to %d songs", len(entries), maxPlaylistSize))
		entries = entries[:maxPlaylistSize]
	}

	m.sendFeedback(fmt.Sprintf("**Loading playlist** (%d songs)", len(entries)))

	loaderCtx, loaderCancel := context.WithCancel(ctx)
	defer loaderCancel()
	loaderID := m.registerLoader(loaderCancel)
	defer m.unregisterLoader(loaderID)

	inserter := m.queue.NewInserter(len(entries))
	defer inserter.Close()

	const preloadBuffer = 2
	for i, entry := range entries {
		for m.queue.Len() > preloadBuffer {
			consumed := m.queue.Consumed()
			select {
			case <-consumed:
			case <-loaderCtx.Done():
				slog.Info("playlist loader cancelled", "loaded", i, "total", len(entries))
				return
			}
		}

		metadata, err := entry.ToMetadata()
		if err != nil {
			slog.Warn("playlist entry has no URL, skipping", "id", entry.ID, "title", entry.Title)
			inserter.Skip()
			continue
		}

		metadata, err = downloader.GetSongMetadata(loaderCtx, metadata.URL)
		if err != nil {
			slog.Warn("failed to get metadata for playlist entry", "title", entry.Title, "error", err)
			inserter.Skip()
			continue
		}

		if metadata.DurationSec > 3*60*60 {
			slog.Warn("skipping playlist entry (>3h)", "title", metadata.Title, "duration", metadata.DurationSec)
			inserter.Skip()
			continue
		}

		song, err := downloader.DownloadSong(loaderCtx, metadata)
		if err != nil {
			slog.Warn("failed to download playlist entry", "title", metadata.Title, "error", err)
			inserter.Skip()
			continue
		}

		select {
		case <-loaderCtx.Done():
			slog.Info("playlist loader cancelled", "loaded", i, "total", len(entries))
			return
		default:
		}

		inserter.Add(song)
		slog.Info("queued playlist entry", "title", song.Metadata.Title, "index", i+1, "total", len(entries))
	}

	slog.Info("playlist loading complete", "total", len(entries))
}

func (m *Manager) sendFeedback(content string) {
	if err := m.apiClient.SendMessage(m.channelID, content); err != nil {
		slog.Error("failed to send feedback message", "error", err)
	}
}
