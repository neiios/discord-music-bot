package env

import (
	"fmt"
	"log/slog"
	"os"
)

type Env struct {
	Token          string
	ClientId       string
	ClientSecret   string
	GuildId        string
	MusicChannelId string
}

func Read() (Env, error) {
	var env Env
	var missing []string

	lookup := func(key string, dest *string) {
		if value, found := os.LookupEnv(key); found {
			*dest = value
		} else {
			missing = append(missing, key)
		}
	}

	lookup("TOKEN", &env.Token)
	lookup("CLIENT_ID", &env.ClientId)
	lookup("CLIENT_SECRET", &env.ClientSecret)
	lookup("GUILD_ID", &env.GuildId)
	lookup("MUSIC_CHANNEL_ID", &env.MusicChannelId)

	if len(missing) > 0 {
		return Env{}, fmt.Errorf("environment variables not set: %v", missing)
	}

	slog.Info("read environment variables")
	return env, nil
}
