package voice

import (
	"context"
	"net/url"
	"testing"

	"github.com/neiios/discord-music-bot/internal/assert"
	"github.com/neiios/discord-music-bot/internal/downloader"
)

func TestExtractOpusPacketsE2E(t *testing.T) {
	videoURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	metadata := downloader.Metadata{
		Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
		DurationSec: 213,
		URL:         *videoURL,
	}

	song, err := downloader.DownloadSong(context.Background(), metadata)
	assert.NoErrf(t, err)
	assert.Equalf(t, song.Metadata, metadata)

	packets, err := ExtractOpusPackets(song.Audio)
	assert.NoErrf(t, err)
	assert.NotEmpty(t, packets)

	expectedMinPackets := metadata.DurationSec * 40
	assert.GreaterOrEqual(t, len(packets), expectedMinPackets)
	assert.Greater(t, len(packets[0]), 0)
}
