package voice

import (
	"net/url"
	"testing"

	"github.com/neiios/discord-music-bot/internal/downloader"
)

func TestExtractOpusPacketsE2E(t *testing.T) {
	videoURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	metadata := downloader.Metadata{
		Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
		DurationSec: 213,
		URL:         *videoURL,
	}

	song, err := downloader.DownloadSong(metadata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if song.Metadata != metadata {
		t.Fatalf("got metadata %v, want %v", song.Metadata, metadata)
	}

	packets, err := ExtractOpusPackets(song.Audio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packets) == 0 {
		t.Fatalf("expected non-empty packets")
	}

	expectedMinPackets := metadata.DurationSec * 40 // ~20ms frames => ~50 fps; allow slack
	if len(packets) < expectedMinPackets {
		t.Fatalf("got %d packets, want >= %d", len(packets), expectedMinPackets)
	}
	if len(packets[0]) <= 0 {
		t.Fatalf("expected first packet length > 0, got %d", len(packets[0]))
	}
}
