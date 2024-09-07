import { createAudioResource } from "@discordjs/voice";
import { $ } from "bun";
import { PLAYER, QUEUE, MUSIC_CHANNEL, VOICE_CONNECTION } from "./main";
import { demuxProbe, AudioResource } from "@discordjs/voice";
import { Readable } from "node:stream";

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

async function getVideoTitle(url: string): Promise<string> {
  if (isYoutubeUrl(url)) {
    return $`yt-dlp --username oauth2 --password unused --get-title -- "${url}"`.text();
  } else {
    return $`yt-dlp --get-title -- "${url}"`.text();
  }
}

async function probeAndCreateResource(readable: Readable) {
  const { stream, type } = await demuxProbe(readable);
  return createAudioResource(stream, { inputType: type });
}

async function downloadAudio(url: string): Promise<AudioResource> {
  // NOTE: format conversion doesn't work when passing stdout directly to blob
  async function download(url: string): Promise<Blob> {
    if (isYoutubeUrl(url)) {
      return $`yt-dlp --username oauth2 --password unused --extractor-args youtube:player-client=default,mweb --extract-audio -o - -- "${url}"`.blob();
    } else {
      return $`yt-dlp --extract-audio -o - -- "${url}"`.blob();
    }
  }

  const blob = await download(url);
  const stream = blob.stream();
  return probeAndCreateResource(Readable.from(stream));
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

export async function handlePlay(
  maybeUrl?: string,
  audio?: AudioResource,
  title?: string,
) {
  if (!maybeUrl) {
    await MUSIC_CHANNEL.send("provide a url");
    return;
  }

  // TODO: there is probably a race condtion with stop and skip here
  MUSIC_CHANNEL.sendTyping();
  const url = await toURL(maybeUrl);

  if (PLAYER.state.status === "playing" || LOCKED) {
    const [audio, title] = await Promise.all([
      downloadAudio(url),
      getVideoTitle(url),
    ]);

    MUSIC_CHANNEL.send(`queued **${title}**`);
    QUEUE.push({ title, url, audio });
    return;
  }

  LOCKED = true;
  if (audio) {
    PLAYER.play(audio);
    await MUSIC_CHANNEL.send(`playing **${title}**`);
  } else {
    // TODO: download the audio in parts (dowloading the whole 10 hour file is slow for some reason ¯\_(ツ)_/¯)
    // and sometimes yt-dlp is being dumb and downloads the whole video to extract the audio part
    const [audio, title] = await Promise.all([
      downloadAudio(url),
      getVideoTitle(url),
    ]);

    PLAYER.play(audio);
    await MUSIC_CHANNEL.send(`playing **${title}**`);
  }

  LOCKED = false;
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
