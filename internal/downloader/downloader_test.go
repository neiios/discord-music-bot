package downloader

import (
	"bytes"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
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
		assert.NoError(t, err)
		assert.Equal(t, metadata, result)
	})

	t.Run("invalid URL", func(t *testing.T) {
		invalidURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=AAAAAAAAAAA")
		_, err := GetSongMetadata(*invalidURL)
		assert.Error(t, err)
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
		assert.NoError(t, err)
		assert.Equal(t, metadata, song.Metadata)
		assert.Greater(t, len(song.Audio), 0)
		assert.True(t, bytes.HasPrefix(song.Audio, []byte("OggS")), "audio should be Ogg/Opus container")
	})
}
