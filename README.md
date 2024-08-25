# Discord Music Bot

## Setup

- Create application and bot on Discord developer portal
- Create a .env file with the `DISCORD_TOKEN` and `MUSIC_CHANNEL_ID` variables
- On first launch you will need to [link your youtube account to the bot](https://github.com/coletdjnz/yt-dlp-youtube-oauth2) (check logs for more info)

## TODO

- Add a command to list all the songs in the queue
- Spotify support
- Docker container doesn't handle SIGINT properly
- Make commands visible in the picker
- Use recommended (faster) deps for discord.js
- Implement `/loop`
