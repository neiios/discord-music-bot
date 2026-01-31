package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/neiios/discord-music-bot/internal/downloader"
	"github.com/neiios/discord-music-bot/internal/env"
	"github.com/neiios/discord-music-bot/internal/gateway"
)

// Manager coordinates voice connection lifecycle and audio playback.
type Manager struct {
	env         env.Env
	gateway     *gateway.Connection
	playQueue   chan downloader.Song
	mu          sync.Mutex
	voiceState  *gateway.VoiceState
	voiceServer *gateway.VoiceServerUpdate
	voiceConn   *Connection
}

// NewManager creates a new voice Manager and starts the background playback loop.
func NewManager(ctx context.Context, gw *gateway.Connection, env env.Env) *Manager {
	m := &Manager{
		env:       env,
		gateway:   gw,
		playQueue: make(chan downloader.Song, 3),
	}

	go m.playLoop(ctx)
	return m
}

// HandlePlay initiates voice connection and queues a song for download and playback.
func (m *Manager) HandlePlay(ctx context.Context, url url.URL) {
	if err := m.sendVoiceStateUpdate(ctx); err != nil {
		slog.Error("failed to request voice state", "error", err)
	}
	go m.downloadAndQueue(ctx, url)
}

// HandleConnect requests joining the configured voice channel.
func (m *Manager) HandleConnect(ctx context.Context) error {
	return m.sendVoiceStateUpdate(ctx)
}

// HandleVoiceStateUpdate processes voice state updates from the gateway.
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
}

// HandleVoiceServerUpdate processes voice server updates from the gateway.
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
}

func (m *Manager) playLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case song := <-m.playQueue:
			conn, err := m.ensureVoiceConnection(ctx)
			if err != nil {
				slog.Error("failed to establish voice connection", "error", err)
				continue
			}

			packets, err := ExtractOpusPackets(song.Audio)
			if err != nil {
				slog.Error("failed to extract opus packets", "error", err)
				continue
			}

			slog.Info("starting playback", "title", song.Metadata.Title, "packets", len(packets))

			// Send packets through the OpusSend channel
			if err := m.sendPackets(ctx, conn, packets); err != nil {
				slog.Error("playback interrupted", "error", err, "title", song.Metadata.Title)
				continue
			}

			// Send silence frames to signal end of audio
			silenceFrame := GetSilenceFrame()
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return
				case conn.OpusSend <- silenceFrame:
				}
			}

			// Turn off speaking after playback
			if err := conn.Speaking(false); err != nil {
				slog.Warn("failed to turn off speaking", "error", err)
			}

			slog.Info("playback complete", "title", song.Metadata.Title)
		}
	}
}

// sendPackets sends opus packets through the connection's OpusSend channel.
func (m *Manager) sendPackets(ctx context.Context, conn *Connection, packets [][]byte) error {
	for _, packet := range packets {
		// Check if connection is still ready
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
			// Packet sent successfully
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

	// Request voice connection if needed
	if err := m.sendVoiceStateUpdate(ctx); err != nil {
		return nil, err
	}

	// Wait for voice state and server updates
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for {
		m.mu.Lock()
		state := m.voiceState
		server := m.voiceServer
		existingConn := m.voiceConn
		m.mu.Unlock()

		// If we have an existing ready connection, use it
		if existingConn != nil {
			existingConn.RLock()
			ready := existingConn.Ready
			existingConn.RUnlock()
			if ready {
				return existingConn, nil
			}
		}

		// Create new connection if we have state and server info
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

			// Wait for connection to be ready
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
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (m *Manager) sendVoiceStateUpdate(ctx context.Context) error {
	m.mu.Lock()
	alreadyJoined := m.voiceState != nil && m.voiceState.ChannelID == m.env.VoiceChannelId
	m.mu.Unlock()

	if alreadyJoined {
		return nil
	}

	data, err := json.Marshal(VoiceStateUpdateData{
		ChannelID: m.env.VoiceChannelId,
		GuildID:   m.env.GuildId,
		SelfMute:  false,
		SelfDeaf:  false,
	})
	if err != nil {
		return err
	}

	rawData := json.RawMessage(data)
	event := gateway.Event{Opcode: 4, Data: &rawData}
	return m.gateway.SendEvent(ctx, event)
}

func (m *Manager) downloadAndQueue(ctx context.Context, url url.URL) {
	metadata, err := downloader.GetSongMetadata(url)
	if err != nil {
		slog.Error("failed to get song metadata", "error", err, "url", url)
		return
	}

	if metadata.DurationSec > 3*60*60 {
		slog.Error("song too long", "duration", metadata.DurationSec, "title", metadata.Title)
		return
	}

	song, err := downloader.DownloadSong(metadata)
	if err != nil {
		slog.Error("failed to download song", "error", err, "title", metadata.Title)
		return
	}

	select {
	case m.playQueue <- song:
		slog.Info("queued song for playback", "title", song.Metadata.Title)
	case <-ctx.Done():
		return
	}
}
