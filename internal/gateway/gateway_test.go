package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/assert"
)

type mockGatewayURLProvider struct {
	url string
}

func (m *mockGatewayURLProvider) GetGatewayUrl() (string, error) {
	return m.url, nil
}

type mockGatewayServer struct {
	heartbeatInterval int
	sessionID         string
	userID            string
	resumeURL         string
	receivedEvents    []Event
}

func newMockGatewayServer() *mockGatewayServer {
	return &mockGatewayServer{
		heartbeatInterval: 45000,
		sessionID:         "test-session-id",
		userID:            "test-user-id",
		resumeURL:         "wss://resume.discord.gg",
	}
}

func (m *mockGatewayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	ctx := r.Context()

	hello := Hello{HeartbeatInterval: m.heartbeatInterval}
	helloData, _ := json.Marshal(hello)
	helloRaw := json.RawMessage(helloData)
	helloEvent := Event{Opcode: 10, Data: &helloRaw}
	if err := wsjson.Write(ctx, conn, helloEvent); err != nil {
		return
	}

	var identifyEvent Event
	if err := wsjson.Read(ctx, conn, &identifyEvent); err != nil {
		return
	}
	m.receivedEvents = append(m.receivedEvents, identifyEvent)

	readyData := ReadyEvent{
		SessionID:        m.sessionID,
		ResumeGatewayURL: m.resumeURL,
		User: User{
			ID:            m.userID,
			Username:      "TestBot",
			Discriminator: "0000",
		},
	}
	readyBytes, _ := json.Marshal(readyData)
	readyRaw := json.RawMessage(readyBytes)
	readyName := "READY"
	seqNum := 1
	readyEvent := Event{
		Opcode:         0,
		Data:           &readyRaw,
		Name:           &readyName,
		SequenceNumber: &seqNum,
	}
	if err := wsjson.Write(ctx, conn, readyEvent); err != nil {
		return
	}

	for {
		var event Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return
		}
		m.receivedEvents = append(m.receivedEvents, event)

		if event.Opcode == 1 {
			ackEvent := Event{Opcode: 11}
			if err := wsjson.Write(ctx, conn, ackEvent); err != nil {
				return
			}
		}
	}
}

func TestNewConnection_Success(t *testing.T) {
	mockServer := newMockGatewayServer()
	server := httptest.NewServer(mockServer)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	mockClient := &mockGatewayURLProvider{url: wsURL}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockClient, "test-token")
	assert.NoErrf(t, err)
	assert.NotNilf(t, conn)

	assert.Equal(t, conn.SessionID, mockServer.sessionID)
	assert.Equal(t, conn.SelfID, mockServer.userID)
	assert.Equal(t, conn.resumeURL, mockServer.resumeURL)

	assert.Greaterf(t, len(mockServer.receivedEvents), 0)
	identifyEvent := mockServer.receivedEvents[0]
	assert.Equal(t, identifyEvent.Opcode, 2)
}

func TestReadEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		testData := json.RawMessage(`{"test": "data"}`)
		eventName := "TEST_EVENT"
		seqNum := 42
		testEvent := Event{
			Opcode:         0,
			Data:           &testData,
			Name:           &eventName,
			SequenceNumber: &seqNum,
		}
		wsjson.Write(ctx, conn, testEvent)

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	assert.NoErrf(t, err)
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	conn := &Connection{
		connection: wsConn,
		token:      "test-token",
	}

	event, err := conn.ReadEvent(ctx)
	assert.NoErrf(t, err)

	assert.Equal(t, event.Opcode, 0)
	assert.DerefEqual(t, event.Name, "TEST_EVENT")
	assert.DerefEqual(t, event.SequenceNumber, 42)
	assert.DerefEqual(t, conn.LastSequenceNumber, 42)
}

func TestSendEvent(t *testing.T) {
	eventReceived := make(chan Event, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		var event Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return
		}
		eventReceived <- event
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	assert.NoErrf(t, err)
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	conn := &Connection{
		connection: wsConn,
		token:      "test-token",
	}

	testData := json.RawMessage(`{"hello": "world"}`)
	testEvent := Event{
		Opcode: 4,
		Data:   &testData,
	}

	err = conn.SendEvent(ctx, testEvent)
	assert.NoErrf(t, err)

	receivedEvent := assert.Recv(t, eventReceived, 2*time.Second)
	assert.Equal(t, receivedEvent.Opcode, 4)
	assert.NotNilf(t, receivedEvent.Data)
}

func TestHeartbeat(t *testing.T) {
	heartbeatReceived := make(chan Event, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		ctx := r.Context()

		var event Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			return
		}

		if event.Opcode == 1 {
			heartbeatReceived <- event
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	assert.NoErrf(t, err)
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	seqNum := 123
	conn := &Connection{
		connection:         wsConn,
		token:              "test-token",
		LastSequenceNumber: &seqNum,
	}

	conn.startHeartbeat(ctx, 100)

	event := assert.Recv(t, heartbeatReceived, 2*time.Second)
	assert.Equal(t, event.Opcode, 1)
	assert.NotNilf(t, event.Data)
	var heartbeatSeq int
	err = json.Unmarshal(*event.Data, &heartbeatSeq)
	assert.NoErrf(t, err)
	assert.Equal(t, heartbeatSeq, 123)
}
