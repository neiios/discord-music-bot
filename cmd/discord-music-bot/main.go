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

				parts := strings.Split(message.Content, " ")
				if len(parts) != 2 || parts[0] != "/play" {
					slog.Info("skipped message", "message", message)
					continue
				}

				url, err := ParseURL(parts[1])
				if err != nil {
					slog.Error("invalid URL", "url", parts[1], "error", err)
					continue
				}

				metadata, err := downloader.GetSongMetadata(url)
				if err != nil {
					slog.Error("failed to get song metadata", "url", url, "error", err)
					continue
				}
				slog.Info("fetched song metadata", "url", url, "metadata", metadata)

				if metadata.DurationSec > 3*60*60 {
					slog.Error("song is too long", "title", metadata.Title, "duration", metadata.DurationSec, "url", url)
				}

				song, error := downloader.DownloadSong(metadata)
				if error != nil {
					slog.Error("failed to download the song", "error", error)
					continue
				}

				slog.Info("downloaded song", "url", url, "title", song.Metadata.Title)
			}
		}

		if event.SequenceNumber != nil {
			connection.LastSequenceNumber = event.SequenceNumber
		}
	}
}

func ParseURL(input string) (*url.URL, error) {
	input = strings.TrimSpace(input)

	u, err := url.ParseRequestURI(input)
	if err != nil {
		return nil, fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("URL is not absolute")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid scheme: %s", u.Scheme)
	}

	return u, nil
}
