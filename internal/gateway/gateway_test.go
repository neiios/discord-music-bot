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

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn == nil {
		t.Fatalf("expected non-nil connection")
	}

	if conn.SessionID != mockServer.sessionID {
		t.Errorf("SessionID = %q, want %q", conn.SessionID, mockServer.sessionID)
	}
	if conn.SelfID != mockServer.userID {
		t.Errorf("SelfID = %q, want %q", conn.SelfID, mockServer.userID)
	}
	if conn.resumeURL != mockServer.resumeURL {
		t.Errorf("resumeURL = %q, want %q", conn.resumeURL, mockServer.resumeURL)
	}

	if len(mockServer.receivedEvents) < 1 {
		t.Fatalf("expected >= 1 received events, got %d", len(mockServer.receivedEvents))
	}
	identifyEvent := mockServer.receivedEvents[0]
	if identifyEvent.Opcode != 2 {
		t.Errorf("identify opcode = %d, want 2", identifyEvent.Opcode)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	conn := &Connection{
		connection: wsConn,
		token:      "test-token",
	}

	event, err := conn.ReadEvent(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Opcode != 0 {
		t.Errorf("Opcode = %d, want 0", event.Opcode)
	}
	if event.Name == nil {
		t.Fatalf("expected non-nil Name")
	}
	if *event.Name != "TEST_EVENT" {
		t.Errorf("Name = %q, want %q", *event.Name, "TEST_EVENT")
	}
	if event.SequenceNumber == nil {
		t.Fatalf("expected non-nil SequenceNumber")
	}
	if *event.SequenceNumber != 42 {
		t.Errorf("SequenceNumber = %d, want 42", *event.SequenceNumber)
	}

	if conn.LastSequenceNumber == nil {
		t.Fatalf("expected non-nil LastSequenceNumber")
	}
	if *conn.LastSequenceNumber != 42 {
		t.Errorf("LastSequenceNumber = %d, want 42", *conn.LastSequenceNumber)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case receivedEvent := <-eventReceived:
		if receivedEvent.Opcode != 4 {
			t.Errorf("Opcode = %d, want 4", receivedEvent.Opcode)
		}
		if receivedEvent.Data == nil {
			t.Fatalf("expected non-nil Data")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	seqNum := 123
	conn := &Connection{
		connection:         wsConn,
		token:              "test-token",
		LastSequenceNumber: &seqNum,
	}

	conn.startHeartbeat(ctx, 100)

	select {
	case event := <-heartbeatReceived:
		if event.Opcode != 1 {
			t.Errorf("Opcode = %d, want 1", event.Opcode)
		}
		if event.Data == nil {
			t.Fatalf("expected non-nil Data")
		}
		var heartbeatSeq int
		err := json.Unmarshal(*event.Data, &heartbeatSeq)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if heartbeatSeq != 123 {
			t.Errorf("heartbeat seq = %d, want 123", heartbeatSeq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for heartbeat")
	}
}
