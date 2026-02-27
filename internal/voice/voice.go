package voice

type VoiceStateUpdateData struct {
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}
