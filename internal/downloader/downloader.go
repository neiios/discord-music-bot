package downloader

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

func DownloadSong(ctx context.Context, metadata Metadata) (Song, error) {
	slog.Info("downloading song", "metadata", metadata)
	tmpDir, err := os.MkdirTemp("", "discord-music-*")
	if err != nil {
		return Song{}, err
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "audio.opus")

	cmd := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "--extract-audio", "--audio-format", "opus", metadata.URL.String(), "-o", tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Info("yt-dlp stderr", "stderr", stderr.String())
		return Song{}, err
	}

	audio, err := os.ReadFile(tmpPath)
	if err != nil {
		return Song{}, err
	}

	song := Song{
		ID:       newID(),
		Metadata: metadata,
		Audio:    audio,
	}

	return song, nil
}

func GetSongMetadata(ctx context.Context, url url.URL) (Metadata, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "--dump-json", "--extract-audio", "--audio-format", "opus", url.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		slog.Info("yt-dlp stderr", "stderr", stderr.String())
		return Metadata{}, err
	}

	var metadata Metadata
	if err := json.NewDecoder(&stdout).Decode(&metadata); err != nil {
		return Metadata{}, err
	}
	metadata.URL = url

	return metadata, nil
}

type Song struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
	Audio    []byte   `json:"audio"`
}

type Metadata struct {
	Title       string  `json:"title"`
	DurationSec int     `json:"duration"`
	URL         url.URL `json:"-"`
}

type PlaylistEntry struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Duration   float64 `json:"duration"`
	URL        string  `json:"url"`
	WebpageURL string  `json:"webpage_url"`
}

func (e PlaylistEntry) ToMetadata() (Metadata, error) {
	rawURL := e.WebpageURL
	if rawURL == "" {
		rawURL = e.URL
	}
	if rawURL == "" {
		return Metadata{}, fmt.Errorf("playlist entry %q has no URL", e.Title)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Metadata{}, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	return Metadata{
		Title:       e.Title,
		DurationSec: int(e.Duration),
		URL:         *parsed,
	}, nil
}

func GetPlaylistEntries(ctx context.Context, rawURL url.URL) ([]PlaylistEntry, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp", "--flat-playlist", "-J", rawURL.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Info("yt-dlp stderr", "stderr", stderr.String())
		return nil, err
	}

	var result struct {
		ID         string          `json:"id"`
		Title      string          `json:"title"`
		Duration   float64         `json:"duration"`
		WebpageURL string          `json:"webpage_url"`
		Entries    []PlaylistEntry `json:"entries"`
	}
	if err := json.NewDecoder(&stdout).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Entries) > 0 {
		return result.Entries, nil
	}

	// Single video — synthesize a one-element list.
	return []PlaylistEntry{{
		ID:         result.ID,
		Title:      result.Title,
		Duration:   result.Duration,
		WebpageURL: result.WebpageURL,
	}}, nil
}

func newID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
