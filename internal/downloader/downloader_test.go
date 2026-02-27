package downloader

import (
	"bytes"
	"context"
	"net/url"
	"testing"

	"github.com/neiios/discord-music-bot/internal/assert"
)

func TestGetSongMetadataE2E(t *testing.T) {
	t.Run("valid URL", func(t *testing.T) {
		url, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		metadata := Metadata{
			Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
			DurationSec: 213,
			URL:         *url,
		}

		result, err := GetSongMetadata(context.Background(), *url)
		assert.NoErr(t, err)
		assert.Equal(t, result, metadata)
	})

	t.Run("invalid URL", func(t *testing.T) {
		invalidURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=AAAAAAAAAAA")
		_, err := GetSongMetadata(context.Background(), *invalidURL)
		assert.IsErr(t, err)
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

		song, err := DownloadSong(context.Background(), metadata)
		assert.NoErr(t, err)
		assert.Equal(t, song.Metadata, metadata)
		assert.Greater(t, len(song.Audio), 0)
		assert.True(t, bytes.HasPrefix(song.Audio, []byte("OggS")), "audio should be Ogg/Opus container")
	})
}
