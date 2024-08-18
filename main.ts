import {
  Client,
  GatewayIntentBits,
  GuildTextBasedChannel,
  Message,
  VoiceBasedChannel,
} from "discord.js";
import ytdl from "@distube/ytdl-core";
import {
  joinVoiceChannel,
  createAudioPlayer,
  createAudioResource,
  getVoiceConnection,
  generateDependencyReport,
} from "@discordjs/voice";

import fs from "fs";

const agent = ytdl.createAgent(
  JSON.parse(fs.readFileSync("cookies.json").toString()),
);

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

client.on("messageCreate", async (message: Message) => {
  if (!message.content.startsWith(PREFIX) || message.author.bot) return;

  const args = message.content.slice(PREFIX.length).trim().split(/ +/);
  const command = args.shift()?.toLowerCase();

  if (!message.member?.voice.channel) {
    return message.channel.send("Join the voice channel first");
  }

  if (command === "play") {
    const url = args[0];
    if (!ytdl.validateURL(url)) {
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
    const videoTitle = (await ytdl.getInfo(url, { agent })).videoDetails.title;
    await textChannel.send(`Queued **${videoTitle}**`);
    return;
  }

  try {
    const stream = ytdl(url, {
      filter: "audioonly",
      highWaterMark: 1 << 25,
      agent,
    });

    const resource = createAudioResource(stream);
    player.play(resource);

    const videoTitle = (await ytdl.getInfo(url, { agent })).videoDetails.title;
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

  const connection = getVoiceConnection(voiceChannel.guild.id);
  if (connection) connection.destroy();
  await message.channel.send("Have a good time, fren");
}

client.login(process.env.DISCORD_TOKEN);
