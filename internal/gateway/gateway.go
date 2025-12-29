package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/api"
)

type Connection struct {
	connection         *websocket.Conn
	client             *api.Client
	token              string
	LastSequenceNumber *int
}

func NewConnection(ctx context.Context, client api.Client, token string) (*Connection, error) {
	gatewayUrl, err := client.GetGatewayUrl()
	if err != nil {
		return nil, err
	}

	websocketConn, res, err := websocket.Dial(context.Background(), gatewayUrl, nil)
	slog.Info("connected to gateway", "res", res)
	if err != nil {
		return nil, err
	}

	connection := &Connection{
		connection: websocketConn,
		client:     &client,
		token:      token,
	}

	event, err := connection.ReadEvent(ctx)
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
		Intents: (1 << 9) | (1 << 10) | (1 << 15),
	}

	connection.sendIdentify(ctx, identify)
	event, err = connection.ReadEvent(ctx)
	if err != nil {
		return nil, err
	}
	if event.Opcode != 0 && event.Name != nil && *event.Name != "READY" {
		slog.Error("expected ready event after identify", "event", event)
		return nil, err
	}
	// TODO: parse ready event and store session id, resume url

	slog.Info("gateway connection established")

	return connection, nil
}

func (g *Connection) ReadEvent(ctx context.Context) (Event, error) {
	var event Event
	if err := wsjson.Read(ctx, g.connection, &event); err != nil {
		return Event{}, err
	}
	slog.Info("received event", "event", event)

	return event, nil
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

func (g *Connection) sendHeartbeat(ctx context.Context) error {
	d, err := json.Marshal(g.LastSequenceNumber)
	if err != nil {
		return err
	}
	raw := json.RawMessage(d)
	event := Event{Opcode: 1, Data: &raw}
	if err := wsjson.Write(ctx, g.connection, event); err != nil {
		return err
	}
	slog.Info("sent heartbeat", "event", event)
	return nil
}

func (g *Connection) sendIdentify(ctx context.Context, identify Identify) error {
	payload, err := json.Marshal(identify)
	if err != nil {
		return err
	}
	d := json.RawMessage(payload)
	event := Event{Opcode: 2, Data: &d}

	if err = wsjson.Write(ctx, g.connection, event); err != nil {
		slog.Error("sending identify failed", "error", err)
		return err
	}
	slog.Info("sent identify event", "event", event)
	return nil
}
