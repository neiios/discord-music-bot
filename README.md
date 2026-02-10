# Discord Music Bot

A Discord music bot written in Go. 
Connects to Discord's Gateway and Voice APIs from scratch -- no third-party Discord library. 
Downloads audio via `yt-dlp` and streams Opus-encoded audio over UDP.

## Requirements

- Go 1.25
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- [FFmpeg](https://ffmpeg.org/)

## Setup

Set the following environment variables:

| Variable | Description |
|---|---|
| `TOKEN` | Discord bot token |
| `CLIENT_ID` | Application client ID |
| `CLIENT_SECRET` | Application client secret |
| `GUILD_ID` | Server (guild) ID |
| `MUSIC_CHANNEL_ID` | Text channel ID for commands |
| `VOICE_CHANNEL_ID` | Voice channel ID to join |

## Build & Run

```sh
set -o allexport
source .env
set +o allexport

go build ./cmd/discord-music-bot
./discord-music-bot
```

## Commands

Send these in the configured music channel:

- `/connect` or `/come` -- join the voice channel
- `/play <url>` -- enqueue and play audio from a URL

## Architecture

The architecture is described in [ARCH.md](ARCH.md).

## License

AGPL-3.0
