package downloader

import (
	"bytes"
	"net/url"
	"testing"
)

func TestGetSongMetadataE2E(t *testing.T) {
	t.Run("valid URL", func(t *testing.T) {
		url, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		metadata := Metadata{
			Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
			DurationSec: 213,
			URL:         *url,
		}

		result, err := GetSongMetadata(*url)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result != metadata {
			t.Errorf("got %v, want %v", result, metadata)
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		invalidURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=AAAAAAAAAAA")
		_, err := GetSongMetadata(*invalidURL)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})
}

func TestDownloadSongE2E(t *testing.T) {
	t.Run("valid metadata", func(t *testing.T) {
		url, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		metadata := Metadata{
			Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
			DurationSec: 213,
			URL:         *url,
		}

		song, err := DownloadSong(metadata)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if song.Metadata != metadata {
			t.Errorf("got metadata %v, want %v", song.Metadata, metadata)
		}
		if len(song.Audio) <= 0 {
			t.Errorf("expected audio length > 0, got %d", len(song.Audio))
		}
		if !bytes.HasPrefix(song.Audio, []byte("OggS")) {
			t.Errorf("audio should be Ogg/Opus container")
		}
	})
}
