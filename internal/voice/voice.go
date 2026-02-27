package voice

import (
	"context"

	"github.com/neiios/discord-music-bot/internal/gateway"
)

type VoiceStateUpdateData struct {
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

func sendVoiceStateUpdate(ctx context.Context, gw *gateway.Connection, guildID, channelID string) error {
	return gw.SendPayload(ctx, 4, VoiceStateUpdateData{
		ChannelID: channelID,
		GuildID:   guildID,
		SelfMute:  false,
		SelfDeaf:  false,
	})
}
