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

type Manager struct {
	env         env.Env
	gateway     *gateway.Connection
	playQueue   chan downloader.Song
	mu          sync.Mutex
	voiceState  *gateway.VoiceState
	voiceServer *gateway.VoiceServerUpdate
	voiceConn   *Connection
}

func NewManager(ctx context.Context, gw *gateway.Connection, env env.Env) *Manager {
	m := &Manager{
		env:       env,
		gateway:   gw,
		playQueue: make(chan downloader.Song, 3),
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
			if err := m.ensureVoiceConnection(ctx); err != nil {
				slog.Error("failed to establish voice connection", "error", err)
				continue
			}

			packets, err := ExtractOpusPackets(song.Audio)
			if err != nil {
				slog.Error("failed to extract opus packets", "error", err)
				continue
			}

			m.mu.Lock()
			conn := m.voiceConn
			m.mu.Unlock()

			if conn == nil {
				slog.Error("voice connection missing when attempting playback")
				continue
			}

			if err := conn.SendOpusPackets(ctx, packets); err != nil {
				slog.Error("failed to stream audio", "error", err)
				m.mu.Lock()
				conn.Close()
				m.voiceConn = nil
				m.mu.Unlock()
			}
		}
	}
}

func (m *Manager) ensureVoiceConnection(ctx context.Context) error {
	m.mu.Lock()
	if m.voiceConn != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if err := m.sendVoiceStateUpdate(ctx); err != nil {
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for {
		m.mu.Lock()
		state := m.voiceState
		server := m.voiceServer
		m.mu.Unlock()

		if state != nil && server != nil {
			// use the long-lived context for the voice websocket so heartbeats keep running
			conn, err := Connect(ctx, m.gateway.SelfID, *state, *server)
			if err != nil {
				return err
			}
			m.mu.Lock()
			m.voiceConn = conn
			m.mu.Unlock()
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for voice server/state")
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
