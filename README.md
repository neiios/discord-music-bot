# Discord Music Bot

A really simple discord music bot

## Setup

- Create application and bot on Discord developer portal
- Create a .env file with the `DISCORD_TOKEN`, `GUILD_ID` and `MUSIC_CHANNEL_ID` variables; `YTDLP_USE_OAUTH_PLUGIN=true` may also be needed
- To enable spotify support also include `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` in the .env file
- Run `make deploy` to pull the latest changes, build the image and run the container
- On first launch you may need to [link your youtube account to the bot](https://github.com/coletdjnz/yt-dlp-youtube-oauth2) if you enabled the oauth plugin (check logs for more info)

## TODO

- Make commands visible in the picker
- Use recommended faster deps for discord.js
- Search songs by name on youtube
- Spotify support
