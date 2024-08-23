import {
  Client,
  GatewayIntentBits,
  GuildTextBasedChannel,
  Message,
  VoiceBasedChannel,
} from "discord.js";
import {
  joinVoiceChannel,
  createAudioPlayer,
  createAudioResource,
  getVoiceConnection,
  generateDependencyReport,
} from "@discordjs/voice";

import fs from "fs";
import { $ } from "bun";

console.log("Starting...");
console.log(generateDependencyReport());

const PREFIX = "/";
const queue: string[] = [];

const player = createAudioPlayer();

const client = new Client({
  intents: [
    GatewayIntentBits.Guilds,
    GatewayIntentBits.GuildVoiceStates,
    GatewayIntentBits.GuildMessages,
    GatewayIntentBits.MessageContent,
  ],
});

client.once("ready", () => {
  console.log("Bot is online!");
});

class UrlValidationError extends Error {}

function validYoutubeUrl(maybeUrl: string): string {
  const youtubeVideoUrlRegex =
    /^(https?:\/\/)?(www\.)?(youtube\.com\/watch\?v=|youtu\.be\/)([a-zA-Z0-9_-]{11})$/;
  if (youtubeVideoUrlRegex.test(maybeUrl)) {
    return maybeUrl;
  } else {
    throw new UrlValidationError("Invalid YouTube URL");
  }
}

client.on("messageCreate", async (message: Message) => {
  try {
    if (!message.content.startsWith(PREFIX) || message.author.bot) return;

    const args = message.content.slice(PREFIX.length).trim().split(/ +/);
    const command = args.shift()?.toLowerCase();

    if (!message.member?.voice.channel) {
      return message.channel.send("Join the voice channel first");
    }

    if (command === "play") {
      const maybeUrl = args[0];
      const url = validYoutubeUrl(maybeUrl);

      if (!validYoutubeUrl(maybeUrl)) {
        return message.channel.send("Send a valid YouTube URL");
      }

      await handlePlay(
        message.member.voice.channel,
        message.channel as GuildTextBasedChannel,
        url,
      );
    } else if (command === "stop") {
      await handleStop(message);
    }
  } catch (err) {
    if (err instanceof UrlValidationError) {
      message.channel.send("Send a valid YouTube URL");
    } else {
      throw err;
    }
  }
});

async function handlePlay(
  voiceChannel: VoiceBasedChannel,
  textChannel: GuildTextBasedChannel,
  url: string,
) {
  let connection = getVoiceConnection(voiceChannel.guild.id);
  if (!connection) {
    connection = joinVoiceChannel({
      channelId: voiceChannel.id,
      guildId: voiceChannel.guild.id,
      adapterCreator: voiceChannel.guild.voiceAdapterCreator,
      selfDeaf: false,
    });

    connection.subscribe(player);
  }

  if (player.state.status === "playing") {
    queue.push(url);
    const videoTitle =
      await $`yt-dlp --username oauth2 --password unused --get-title -- "${url}"`.text();
    await textChannel.send(`Queued **${videoTitle}**`);
    return;
  }

  try {
    await textChannel.sendTyping();
    const videoTitle =
      await $`yt-dlp --username oauth2 --password unused --get-title -- "${url}"`.text();

    // TODO: ideally i don't want to save the file to disk
    // TODO: download the audio in parts (dowloading the whole 10 hour file is slow for some reason ¯\_(ツ)_/¯)
    // there is probably also a shell escaping vulnerability here somewhere
    console.log(`Downloading: ${url}`);
    await $`yt-dlp --extract-audio --audio-format opus --username oauth2 --password unused -o "/tmp/song.%(ext)s" -- "${url}"`;
    const stream = fs.createReadStream("/tmp/song.opus");
    const resource = createAudioResource(stream);
    player.play(resource);
    await $`rm /tmp/song.opus`;

    await textChannel.send(`Playing **${videoTitle}**`);

    player.on("stateChange", async (oldState, newState) => {
      if (newState.status === "idle" && oldState.status !== "idle") {
        const nextUrl = queue.shift();
        if (nextUrl) {
          await handlePlay(voiceChannel, textChannel, nextUrl);
        }
      }
    });

    player.on("error", (err) => {
      console.error("Error in player: ", err);
      textChannel.send("An error occurred while playing the audio");
    });
  } catch (err) {
    console.error("Error playing audio: ", err);
    textChannel.send("An error occurred while trying to play the audio");
  }
}

async function handleStop(message: Message) {
  const voiceChannel = message.member?.voice.channel;
  if (!voiceChannel) {
    return message.channel.send("Join the voice channel first");
  }

  player.stop();

  const connection = getVoiceConnection(voiceChannel.guild.id);
  if (connection) connection.destroy();
  await message.channel.send("Have a good time, fren");
}

client.login(process.env.DISCORD_TOKEN);
