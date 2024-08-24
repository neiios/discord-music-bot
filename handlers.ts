import { createAudioResource } from "@discordjs/voice";
import { $ } from "bun";
import { PLAYER, QUEUE, MUSIC_CHANNEL, VOICE_CONNECTION } from "./main";
import { Readable } from "stream";

let LOCKED = false;

function isYoutubeUrl(url: URL): boolean {
  return (
    url.hostname === "www.youtube.com" ||
    url.hostname === "youtube.com" ||
    url.hostname === "youtu.be"
  );
}

async function getVideoTitle(url: URL) {
  if (isYoutubeUrl(url)) {
    return $`yt-dlp --username oauth2 --password unused --get-title -- "${url}"`.text();
  } else {
    return $`yt-dlp --get-title -- "${url}"`.text();
  }
}

async function downloadAudio(url: URL) {
  if (isYoutubeUrl(url)) {
    return $`yt-dlp --username oauth2 --password unused --extract-audio --audio-format opus -o - -- "${url}"`.blob();
  } else {
    return $`yt-dlp --extract-audio --audio-format opus -o - -- "${url}"`.blob();
  }
}

async function toURL(maybeUrl: string) {
  try {
    return new URL(maybeUrl);
  } catch {
    MUSIC_CHANNEL.send("invalid url");
    throw new Error("invalid url");
  }
}

export async function handlePlay(maybeUrl: string) {
  MUSIC_CHANNEL.sendTyping();
  const url = await toURL(maybeUrl);

  if (PLAYER.state.status === "playing" || LOCKED) {
    const title = await getVideoTitle(url);
    QUEUE.push({ title: title, url });
    MUSIC_CHANNEL.send(`queued **${title}**`);
    return;
  }

  LOCKED = true;
  const title = getVideoTitle(url);

  // TODO: there is probably a race condtion with stop here
  // TODO: download the audio in parts (dowloading the whole 10 hour file is slow for some reason ¯\_(ツ)_/¯)
  console.log(`downloading: ${url}`);
  const blob = await downloadAudio(url);
  const stream = blob.stream();
  const resource = createAudioResource(Readable.from(stream));
  PLAYER.play(resource);

  await MUSIC_CHANNEL.send(`playing **${await title}**`);
  LOCKED = false;
}

export async function handleSkip() {
  if (PLAYER.state.status !== "playing") {
    return;
  }

  const nextUrl = QUEUE.shift();
  if (!nextUrl) {
    return;
  }

  PLAYER.stop();
  await MUSIC_CHANNEL.send("skipped");

  await handlePlay(nextUrl.url.href);
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
