package voice

import (
	"context"
	"encoding/json"

	"github.com/neiios/discord-music-bot/internal/env"
	"github.com/neiios/discord-music-bot/internal/gateway"
)

func InitiateConnection(ctx context.Context, connection gateway.Connection, env env.Env) error {
	data, err := json.Marshal(VoiceStateUpdateData{
		ChannelID: env.VoiceChannelId,
		GuildID:   env.GuildId,
		SelfMute:  false,
		SelfDeaf:  false,
	})
	if err != nil {
		return err
	}
	rawData := json.RawMessage(data)
	event := gateway.Event{
		Opcode: 4,
		Data:   &rawData,
	}
	err = connection.SendEvent(ctx, event)
	if err != nil {
		return err
	}
	return nil
}

type VoiceStateUpdateData struct {
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}
