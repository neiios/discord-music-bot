package gateway

import "encoding/json"

type Event struct {
	Opcode         int              `json:"op"`
	SequenceNumber *int             `json:"s"`
	Data           *json.RawMessage `json:"d"`
	Name           *string          `json:"t"`
}

type Hello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type Identify struct {
	Token      string             `json:"token"`
	Properties IdentifyProperties `json:"properties"`
	Intents    int                `json:"intents"`
}

type IdentifyProperties struct {
	Os      string `json:"os"`
	Browser string `json:"browser"`
	Device  string `json:"device"`
}

type Message struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	Author    User   `json:"author"`
}

type ReadyEvent struct {
	SessionID        string `json:"session_id"`
	ResumeGatewayURL string `json:"resume_gateway_url"`
	User             User   `json:"user"`
}

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
}

type VoiceState struct {
	GuildID        string  `json:"guild_id"`
	ChannelID      string  `json:"channel_id"`
	UserID         string  `json:"user_id"`
	SessionID      string  `json:"session_id"`
	Deaf           bool    `json:"deaf"`
	Mute           bool    `json:"mute"`
	SelfDeaf       bool    `json:"self_deaf"`
	SelfMute       bool    `json:"self_mute"`
	SelfStream     bool    `json:"self_stream"`
	SelfVideo      bool    `json:"self_video"`
	Suppress       bool    `json:"suppress"`
	RequestToSpeak *string `json:"request_to_speak_timestamp"`
}

type VoiceServerUpdate struct {
	Token    string `json:"token"`
	GuildID  string `json:"guild_id"`
	Endpoint string `json:"endpoint"`
}
