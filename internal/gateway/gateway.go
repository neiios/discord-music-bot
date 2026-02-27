package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/api"
)

type Connection struct {
	sendMu             sync.Mutex
	connection         *websocket.Conn
	token              string
	LastSequenceNumber *int
	SessionID          string
	SelfID             string
	resumeURL          string
}

func NewConnection(ctx context.Context, client api.GatewayURLProvider, token string) (*Connection, error) {
	gatewayUrl, err := client.GetGatewayUrl()
	if err != nil {
		return nil, err
	}

	parsedUrl, err := url.Parse(gatewayUrl)
	if err != nil {
		return nil, err
	}

	query := parsedUrl.Query()
	query.Set("v", "10")
	query.Set("encoding", "json")
	parsedUrl.RawQuery = query.Encode()

	websocketConn, _, err := websocket.Dial(ctx, parsedUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	slog.Info("connected to gateway")

	connection := &Connection{
		connection: websocketConn,
		token:      token,
	}

	event, err := connection.ReadEvent(ctx)
	if err != nil {
		return nil, err
	}
	var hello Hello
	if err := json.Unmarshal(*event.Data, &hello); err != nil {
		return nil, err
	}
	connection.startHeartbeat(ctx, hello.HeartbeatInterval)

	identify := Identify{
		Token: connection.token,
		Properties: IdentifyProperties{
			Os:      "templeos",
			Browser: "templeos",
			Device:  "templeos",
		},
		Intents: IntentGuilds | IntentGuildVoiceStates | IntentGuildMessages | IntentMessageContent | IntentGuildMessageTyping,
	}

	if err := connection.sendIdentify(ctx, identify); err != nil {
		return nil, err
	}

	event, err = connection.ReadEvent(ctx)
	if err != nil {
		return nil, err
	}
	if event.Name == nil || *event.Name != "READY" {
		return nil, fmt.Errorf("expected READY event, got %v", event.Name)
	}

	var ready ReadyEvent
	if err := json.Unmarshal(*event.Data, &ready); err != nil {
		return nil, err
	}

	connection.SessionID = ready.SessionID
	connection.SelfID = ready.User.ID
	connection.resumeURL = ready.ResumeGatewayURL

	slog.Info("gateway connection established")

	return connection, nil
}

func (g *Connection) ReadEvent(ctx context.Context) (Event, error) {
	var event Event
	if err := wsjson.Read(ctx, g.connection, &event); err != nil {
		return Event{}, err
	}

	if event.SequenceNumber != nil {
		g.LastSequenceNumber = event.SequenceNumber
	}
	slog.Debug("received event", "event", event)
	return event, nil
}

func (g *Connection) SendEvent(ctx context.Context, event Event) error {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()

	if err := wsjson.Write(ctx, g.connection, event); err != nil {
		return err
	}
	slog.Info("sent event", "event", event)
	return nil
}

func (g *Connection) startHeartbeat(ctx context.Context, interval int) {
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("stopping heartbeat due to context cancellation")
				return
			case <-ticker.C:
				err := g.sendHeartbeat(ctx)
				if err != nil {
					slog.Error("heartbeat failed", "error", err)
					return
				}
			}
		}
	}()
}

func (g *Connection) SendPayload(ctx context.Context, opcode int, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw := json.RawMessage(data)
	event := Event{Opcode: opcode, Data: &raw}
	return g.SendEvent(ctx, event)
}

func (g *Connection) sendHeartbeat(ctx context.Context) error {
	return g.SendPayload(ctx, 1, g.LastSequenceNumber)
}

func (g *Connection) sendIdentify(ctx context.Context, identify Identify) error {
	return g.SendPayload(ctx, 2, identify)
}
