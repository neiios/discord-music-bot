package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/neiios/discord-music-bot/internal/api"
	"github.com/neiios/discord-music-bot/internal/env"
	"github.com/neiios/discord-music-bot/internal/gateway"
	"github.com/neiios/discord-music-bot/internal/voice"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	env, err := env.Read()
	if err != nil {
		slog.Error("failed to read environment variables", "error", err)
		os.Exit(1)
	}

	discordApiBaseUrl := "https://discord.com/api/v10"
	discordClient, err := api.NewClient(discordApiBaseUrl, env.Token)
	if err != nil {
		slog.Error("failed to create discord client", "error", err)
		os.Exit(1)
	}

	connection, err := gateway.NewConnection(ctx, discordClient, env.Token)
	if err != nil {
		slog.Error("failed to connect to gateway", "error", err)
		os.Exit(1)
	}

	voiceManager := voice.NewManager(ctx, connection, env, discordClient)

	for {
		event, err := connection.ReadEvent(ctx)
		if err != nil {
			slog.Error("failed to read event", "error", err)
			os.Exit(1)
		}

		switch {
		case event.Name != nil && *event.Name == "MESSAGE_CREATE":
			var message gateway.Message
			if err := json.Unmarshal(*event.Data, &message); err != nil {
				slog.Error("failed to unmarshal message", "error", err)
				continue
			}
			handleMessage(ctx, message, voiceManager, env)
		case event.Name != nil && *event.Name == "VOICE_STATE_UPDATE":
			var state gateway.VoiceState
			if err := json.Unmarshal(*event.Data, &state); err != nil {
				slog.Error("failed to parse voice state update", "error", err)
				continue
			}
			voiceManager.HandleVoiceStateUpdate(state)
		case event.Name != nil && *event.Name == "VOICE_SERVER_UPDATE":
			var update gateway.VoiceServerUpdate
			if err := json.Unmarshal(*event.Data, &update); err != nil {
				slog.Error("failed to parse voice server update", "error", err)
				continue
			}
			voiceManager.HandleVoiceServerUpdate(update)
		}
	}
}

func handleMessage(ctx context.Context, message gateway.Message, manager *voice.Manager, env env.Env) {
	if message.ChannelID != env.MusicChannelId {
		return
	}
	if message.GuildID != "" && message.GuildID != env.GuildId {
		return
	}

	parts := strings.Fields(message.Content)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/play":
		if len(parts) != 2 {
			slog.Error("invalid play command", "content", message.Content)
			return
		}

		u, err := ParseURL(parts[1])
		if err != nil {
			slog.Error("failed to parse URL", "input", parts[1], "error", err)
			return
		}

		slog.Info("enqueueing song", "url", u.String())
		manager.HandlePlay(ctx, u)
	case "/connect", "/come":
		if err := manager.HandleConnect(ctx); err != nil {
			slog.Error("failed to request voice connection", "error", err)
		}
	case "/skip":
		manager.HandleSkip()
	case "/stop":
		manager.HandleStop()
	case "/queue":
		manager.HandleQueue()
	default:
	}
}

func ParseURL(input string) (url.URL, error) {
	input = strings.TrimSpace(input)

	u, err := url.ParseRequestURI(input)
	if err != nil {
		return url.URL{}, fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return url.URL{}, fmt.Errorf("URL is not absolute")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return url.URL{}, fmt.Errorf("invalid scheme: %s", u.Scheme)
	}

	return *u, nil
}
