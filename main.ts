import {
  Client,
  GatewayIntentBits,
  type GuildTextBasedChannel,
  type VoiceBasedChannel,
  Message,
} from "discord.js";
import {
  createAudioPlayer,
  joinVoiceChannel,
  generateDependencyReport,
  VoiceConnection,
  AudioResource,
} from "@discordjs/voice";
import { handlePlay, handleSkip, handleList } from "./handlers";

export const PREFIX = "/";
export const QUEUE: Song[] = [];
export const PLAYER = createAudioPlayer();
export const CLIENT = new Client({
  intents: [
    GatewayIntentBits.Guilds,
    GatewayIntentBits.GuildVoiceStates,
    GatewayIntentBits.GuildMessages,
    GatewayIntentBits.MessageContent,
  ],
});

CLIENT.on("messageCreate", async (message: Message) => {
  try {
    console.log(`command: ${message.content}`);
    if (isInvalidMessage(message)) return;

    const { command, args } = parseCommand(message);
    joinVoiceChannelIfNecessary(message.member?.voice.channel);

    if (command === "play") await handlePlay(args[0]);
    else if (command === "connect" || command === "join")
      forceJoinVoiceChannel(message.member?.voice.channel);
    else if (command === "skip") await handleSkip();
    else if (command === "list" || command === "queue") await handleList();
    else if (command === "disconnect" || command === "stop") await handleSkip();
    else await message.channel.send("invalid command");
  } catch (err) {
    if (err instanceof BotError) {
      console.log(err);
      message.channel.send(err.message);
    }
  }
});

PLAYER.on("stateChange", async (oldState, newState) => {
  if (newState.status === "idle" && oldState.status !== "idle") {
    const nextUrl = QUEUE.shift();
    if (!nextUrl) {
      return;
    }

    await handlePlay(nextUrl.url, nextUrl.audio, nextUrl.title);
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

type Song = {
  title: string;
  url: string;
  audio?: AudioResource;
};

export const BotError = class extends Error {
  constructor(message: string) {
    super(message);
    this.name = "BotError";
  }
};

function forceJoinVoiceChannel(channel?: VoiceBasedChannel | null) {
  if (!channel) throw new BotError("join voice channel first");
  if (VOICE_CONNECTION) VOICE_CONNECTION.destroy();

  VOICE_CONNECTION = joinVoiceChannel({
    channelId: channel.id,
    guildId: channel.guild.id,
    adapterCreator: channel.guild.voiceAdapterCreator,
    selfDeaf: false,
  });

  VOICE_CONNECTION.subscribe(PLAYER);

  MUSIC_CHANNEL.send(`joined ${channel.name}`);
}

function joinVoiceChannelIfNecessary(channel?: VoiceBasedChannel | null) {
  if (VOICE_CONNECTION) return;
  if (!channel) throw new BotError("join voice channel first");

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
