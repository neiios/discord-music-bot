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
  VoiceConnectionStatus,
  entersState,
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

    if (command === "play") {
      connectToVoiceChannel(message);
      await handlePlay(args[0]);
    } else if (command === "connect" || command === "join") {
      connectToVoiceChannel(message, true);
    } else if (command === "skip") {
      await handleSkip();
    } else if (command === "list" || command === "queue") {
      await handleList();
    } else if (command === "disconnect" || command === "stop") {
      await handleSkip();
    } else {
      throw new BotError("invalid command");
    }
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

function connectToVoiceChannel(message: Message, force: boolean = false) {
  if (!message.member?.voice.channel)
    throw new BotError("join voice channel first");
  if (VOICE_CONNECTION && !force) return;

  const connection = joinVoiceChannel({
    channelId: message.member.voice.channel.id,
    guildId: message.member.voice.channel.guild.id,
    adapterCreator: message.member.voice.channel.guild.voiceAdapterCreator,
  });

  if (force) MUSIC_CHANNEL.send(`joined ${message.member.voice.channel.name}`);

  // https://discordjs.guide/voice/voice-connections.html#handling-disconnects
  connection.on(
    VoiceConnectionStatus.Disconnected,
    async (oldState, newState) => {
      try {
        // Seems to be reconnecting to a new channel - ignore disconnect
        await Promise.race([
          entersState(connection, VoiceConnectionStatus.Signalling, 5_000),
          entersState(connection, VoiceConnectionStatus.Connecting, 5_000),
        ]);
      } catch (error) {
        // Seems to be a real disconnect which SHOULDN'T be recovered from
        connection.destroy();
        VOICE_CONNECTION = undefined;
      }
    },
  );

  connection.subscribe(PLAYER);
  VOICE_CONNECTION = connection;
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
