package voice

import (
	"net/url"
	"testing"

	"github.com/neiios/discord-music-bot/internal/downloader"
	"github.com/stretchr/testify/require"
)

func TestExtractOpusPacketsE2E(t *testing.T) {
	videoURL, _ := url.ParseRequestURI("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	metadata := downloader.Metadata{
		Title:       "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)",
		DurationSec: 213,
		URL:         *videoURL,
	}

	song, err := downloader.DownloadSong(metadata)
	require.NoError(t, err)
	require.Equal(t, metadata, song.Metadata)

	packets, err := ExtractOpusPackets(song.Audio)
	require.NoError(t, err)
	require.NotEmpty(t, packets)

	expectedMinPackets := metadata.DurationSec * 40 // ~20ms frames => ~50 fps; allow slack
	require.GreaterOrEqual(t, len(packets), expectedMinPackets)
	require.Greater(t, len(packets[0]), 0)
}
