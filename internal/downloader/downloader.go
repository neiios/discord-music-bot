package downloader

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/url"
	"os/exec"

	"github.com/google/uuid"
)

func DownloadSong(metadata Metadata) (Song, error) {
	// yt-dlp --no-playlist --extract-audio --audio-format opus <url> -o -
	slog.Info("downloading song", "metadata", metadata)
	cmd := exec.Command("yt-dlp", "--no-playlist", "--extract-audio", "--audio-format", "opus", metadata.URL.String(), "-o", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		slog.Info("yt-dlp stderr", "stderr", stderr.String())
		return Song{}, err
	}

	song := Song{
		ID:       uuid.NewString(),
		Metadata: metadata,
		Audio:    stdout.Bytes(),
	}

	return song, nil
}

func GetSongMetadata(url *url.URL) (Metadata, error) {
	// yt-dlp --no-playlist --dump-json --extract-audio --audio-format opus <url>
	cmd := exec.Command("yt-dlp", "--no-playlist", "--dump-json", "--extract-audio", "--audio-format", "opus", url.String())
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
	metadata.URL = *url

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
