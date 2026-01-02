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
	"github.com/neiios/discord-music-bot/internal/downloader"
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

	connection, err := gateway.NewConnection(ctx, *discordClient, env.Token)
	if err != nil {
		slog.Error("failed to connect to gateway", "error", err)
		os.Exit(1)
	}

	for {
		event, err := connection.ReadEvent(ctx)
		if err != nil {
			slog.Error("failed to read event", "error", err)
			os.Exit(1)
		}

		if event.Opcode == 0 {
			if event.Name != nil && *event.Name == "MESSAGE_CREATE" {
				var message gateway.Message
				if err := json.Unmarshal(*event.Data, &message); err != nil {
					slog.Error("failed to unmarshal message", "error", err)
					os.Exit(1)
				}
				slog.Info("received message", "content", message.Content)

				if err := HandleNewMessage(message, *connection, env); err != nil {
					slog.Error("failed to handle new message", "error", err)
				}
			}
		}

		if event.SequenceNumber != nil {
			connection.LastSequenceNumber = event.SequenceNumber
		}
	}
}

func HandleNewMessage(message gateway.Message, connection gateway.Connection, env env.Env) error {
	parts := strings.Split(message.Content, " ")
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "/play":
		if len(parts) != 2 {
			return fmt.Errorf("invalid command")
		}

		url, err := ParseURL(parts[1])
		if err != nil {
			slog.Error("failed to parse URL", "input", parts[1], "error", err)
			return fmt.Errorf("invalid URL")
		}

		metadata, err := downloader.GetSongMetadata(url)
		if err != nil {
			slog.Error("failed to get song metadata", "url", url, "error", err)
			return fmt.Errorf("failed to get song metadata")
		}
		slog.Info("fetched song metadata", "url", url, "metadata", metadata)

		if metadata.DurationSec > 3*60*60 {
			slog.Error("song is too long", "title", metadata.Title, "duration", metadata.DurationSec, "url", url)
			return fmt.Errorf("song is too long")
		}

		song, err := downloader.DownloadSong(metadata)
		if err != nil {
			slog.Error("failed to download the song", "error", err)
			return fmt.Errorf("failed to download the song")
		}

		slog.Info("downloaded song", "url", url, "title", song.Metadata.Title)
		return nil
	case "/connect", "/come":
		err := voice.InitiateConnection(context.Background(), connection, env)
		return err
	default:
		return nil
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
