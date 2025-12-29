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
	Content string `json:"content"`
}
