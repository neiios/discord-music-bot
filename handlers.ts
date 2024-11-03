import { createAudioResource } from "@discordjs/voice";
import { $ } from "bun";
import {
  PLAYER,
  QUEUE,
  MUSIC_CHANNEL,
  VOICE_CONNECTION,
  BotError,
} from "./main";
import { AudioResource } from "@discordjs/voice";
import fs from "node:fs";
import crypto from "node:crypto";

let LOCKED = false;

function isYoutubeUrl(url: string): boolean {
  try {
    const parsedUrl = new URL(url);
    return (
      parsedUrl.hostname === "www.youtube.com" ||
      parsedUrl.hostname === "youtube.com" ||
      parsedUrl.hostname === "youtu.be"
    );
  } catch (e) {
    return false;
  }
}

function isSpotifyUrl(url: string): boolean {
  try {
    const parsedUrl = new URL(url);
    return (
      parsedUrl.hostname === "open.spotify.com" &&
      parsedUrl.pathname.includes("/track/")
    );
  } catch (error) {
    console.error(`Error in isSpotifyUrl: ${error.message}`);
    return false;
  }
}

async function getSpotifyAccessToken(): Promise<string> {
  const clientId = process.env.SPOTIFY_CLIENT_ID;
  const clientSecret = process.env.SPOTIFY_CLIENT_SECRET;

  if (!clientId || !clientSecret) {
    throw new BotError("Spotify credentials are missing.");
  }

  const authString = Buffer.from(`${clientId}:${clientSecret}`).toString(
    "base64",
  );

  const response = await fetch("https://accounts.spotify.com/api/token", {
    method: "POST",
    headers: {
      Authorization: `Basic ${authString}`,
      "Content-Type": "application/x-www-form-urlencoded",
    },
    body: "grant_type=client_credentials",
  });

  if (!response.ok) {
    throw new BotError("Failed to retrieve Spotify access token");
  }

  const data = await response.json();
  return data.access_token;
}

async function fetchYouTubeUrlFromSpotify(url: string): Promise<string> {
  const match = url.match(/track\/(\w+)/);
  if (!match) throw new BotError("Invalid Spotify track URL");

  const trackId = match[1];
  const accessToken = await getSpotifyAccessToken();

  const response = await fetch(`https://api.spotify.com/v1/tracks/${trackId}`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
    },
  });

  if (!response.ok) {
    throw new BotError("Failed to fetch track data from Spotify");
  }

  const data = await response.json();
  const title = data.name;
  const artist = data.artists[0].name;

  const query = `${artist} ${title}`;
  let searchResult;
  if (process.env.YTDLP_USE_OAUTH_PLUGIN === "true") {
    searchResult =
      await $`yt-dlp --username oauth2 --password unused "ytsearch:${query}" --get-id --default-search "ytsearch"`.text();
  } else {
    searchResult =
      await $`yt-dlp "ytsearch:${query}" --get-id --default-search "ytsearch"`.text();
  }
  const videoId = searchResult.trim().split("\n")[0];

  if (!videoId) {
    throw new BotError("No matching video found on YouTube");
  }

  return `https://www.youtube.com/watch?v=${videoId}`;
}

async function handleUrl(maybeUrl: string): Promise<string> {
  const url = await toURL(maybeUrl);
  if (isSpotifyUrl(url)) {
    return await fetchYouTubeUrlFromSpotify(url);
  } else {
    return url;
  }
}

async function toURL(maybeUrl: string): Promise<string> {
  try {
    new URL(maybeUrl);
    return maybeUrl;
  } catch {
    MUSIC_CHANNEL.send("invalid url");
    throw new Error("invalid url");
  }
}

async function getVideoTitle(url: string): Promise<string> {
  const handledUrl = await handleUrl(url);
  if (
    isYoutubeUrl(handledUrl) &&
    process.env.YTDLP_USE_OAUTH_PLUGIN === "true"
  ) {
    return $`yt-dlp --username oauth2 --password unused --get-title -- "${handledUrl}"`.text();
  } else {
    return $`yt-dlp --get-title -- "${handledUrl}"`.text();
  }
}

async function downloadAudio(url: string): Promise<AudioResource> {
  const urlHash = crypto.createHash("sha256").update(url).digest("hex");
  if (isYoutubeUrl(url) && process.env.YTDLP_USE_OAUTH_PLUGIN === "true") {
    await $`yt-dlp --username oauth2 --password unused --extractor-args youtube:player-client=default,mweb --extract-audio -o "/tmp/${urlHash}.%(ext)s" -- "${url}"`;
  } else {
    await $`yt-dlp --extract-audio -o /tmp/${urlHash} -- "${url}"`;
  }

  const filename = (await $`ls /tmp/${urlHash}.*`.text()).trim();
  if (!filename) {
    throw new BotError("no file downloaded, oops");
  }

  const stream = fs.createReadStream(filename);
  await $`rm ${filename}*`;
  return createAudioResource(stream);
}

export async function handlePlay(
  maybeUrl?: string,
  audio?: AudioResource,
  title?: string,
) {
  if (!maybeUrl) {
    await MUSIC_CHANNEL.send("provide a url");
    return;
  }

  // TODO: there is probably a race condition with stop and skip here
  MUSIC_CHANNEL.sendTyping();
  const url = await handleUrl(maybeUrl);

  if (PLAYER.state.status === "playing" || LOCKED) {
    const [videoTitle, downloadedAudio] = await Promise.all([
      getVideoTitle(url),
      downloadAudio(url),
    ]);

    MUSIC_CHANNEL.send(`queued **${videoTitle}**`);
    QUEUE.push({ title: videoTitle, url, audio: downloadedAudio });
    return;
  }

  LOCKED = true;
  try {
    let downloadedAudio = audio;
    let videoTitle = title;

    if (!downloadedAudio) {
      // Only download if the audio is not provided
      [videoTitle, downloadedAudio] = await Promise.all([
        getVideoTitle(url),
        downloadAudio(url),
      ]);
    }

    PLAYER.play(downloadedAudio);
    console.log("playing", videoTitle, url);
    await MUSIC_CHANNEL.send(`playing **${videoTitle}**`);
  } finally {
    LOCKED = false;
  }
}

export async function handleSkip() {
  if (PLAYER.state.status !== "playing") {
    return;
  }

  PLAYER.stop();
  await MUSIC_CHANNEL.send("skipped");

  const nextUrl = QUEUE.shift();
  if (!nextUrl) {
    return;
  }

  await handlePlay(nextUrl.url);
}

export async function handleDisconnect() {
  if (!VOICE_CONNECTION) return;

  PLAYER.stop();
  VOICE_CONNECTION.destroy();
  QUEUE.length = 0;
  await MUSIC_CHANNEL.send("have a good time, fren");
}

export async function handleList() {
  if (QUEUE.length === 0) {
    await MUSIC_CHANNEL.send("queue is empty");
    return;
  }

  const titles = QUEUE.map(
    (song, index) => `${index + 1}. **${song.title.trim()}**`,
  );
  const message = `queue:\n${titles.join("\n")}`;
  await MUSIC_CHANNEL.send(message);
}
