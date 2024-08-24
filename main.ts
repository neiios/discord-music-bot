import {
  Client,
  GatewayIntentBits,
  GuildTextBasedChannel,
  VoiceBasedChannel,
  Message,
} from "discord.js";
import {
  createAudioPlayer,
  joinVoiceChannel,
  generateDependencyReport,
  VoiceConnection,
} from "@discordjs/voice";
import { handlePlay, handleSkip, handleDisconnect } from "./handlers";

export const PREFIX = "/";
export const QUEUE: URL[] = [];
export const PLAYER = createAudioPlayer();
export const CLIENT = new Client({
  intents: [
    GatewayIntentBits.Guilds,
    GatewayIntentBits.GuildVoiceStates,
    GatewayIntentBits.GuildMessages,
    GatewayIntentBits.MessageContent,
  ],
});

function joinVoiceChannelIfNecessary(channel?: VoiceBasedChannel | null) {
  if (VOICE_CONNECTION) return;
  if (!channel) throw new Error("join voice channel");

  VOICE_CONNECTION = joinVoiceChannel({
    channelId: channel.id,
    guildId: channel.guild.id,
    adapterCreator: channel.guild.voiceAdapterCreator,
    selfDeaf: false,
  });

  VOICE_CONNECTION.subscribe(PLAYER);
}

function parseCommand(message: Message) {
  const args = message.content.slice(PREFIX.length).trim().split(/ +/);
  const command = args.shift()?.toLowerCase();
  return { args, command };
}

function isInvalidMessage(message: Message) {
  return (
    !message.content.startsWith(PREFIX) ||
    message.author.bot ||
    message.channelId != MUSIC_CHANNEL.id
  );
}

CLIENT.on("messageCreate", async (message: Message) => {
  try {
    if (isInvalidMessage(message)) return;
    const { command, args } = parseCommand(message);
    joinVoiceChannelIfNecessary(message.member?.voice.channel);

    if (command === "play") {
      await handlePlay(args[0]);
    } else if (command === "skip") {
      await handleSkip();
    } else if (command === "disconnect") {
      await handleDisconnect();
    } else {
      message.channel.send("invalid command");
    }
  } catch (err) {
    console.log(err);
    message.channel.send("error handling command");
  }
});

PLAYER.on("stateChange", async (oldState, newState) => {
  if (newState.status === "idle" && oldState.status !== "idle") {
    const nextUrl = QUEUE.shift();
    if (!nextUrl) {
      return;
    }

    await handlePlay(nextUrl.toString());
  }
});

CLIENT.once("ready", () => {
  console.log("ready to jam");
});

console.log(generateDependencyReport());
await CLIENT.login(process.env.DISCORD_TOKEN);

export let VOICE_CONNECTION: VoiceConnection | undefined;
export const MUSIC_CHANNEL = (await CLIENT.channels.fetch(
  process.env.MUSIC_CHANNEL_ID!,
)) as GuildTextBasedChannel;
