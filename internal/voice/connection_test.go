package voice

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/gateway"
	"golang.org/x/crypto/chacha20poly1305"
)

func TestChooseMode(t *testing.T) {
	tests := []struct {
		name     string
		modes    []string
		expected string
	}{
		{
			name:     "prefers aead_aes256_gcm_rtpsize",
			modes:    []string{"xsalsa20_poly1305", "aead_aes256_gcm_rtpsize", "aead_xchacha20_poly1305_rtpsize"},
			expected: "aead_aes256_gcm_rtpsize",
		},
		{
			name:     "falls back to aead_xchacha20_poly1305_rtpsize",
			modes:    []string{"xsalsa20_poly1305", "aead_xchacha20_poly1305_rtpsize"},
			expected: "aead_xchacha20_poly1305_rtpsize",
		},
		{
			name:     "uses first mode if no preferred",
			modes:    []string{"xsalsa20_poly1305", "xsalsa20_poly1305_lite"},
			expected: "xsalsa20_poly1305",
		},
		{
			name:     "returns empty for empty modes",
			modes:    []string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := chooseMode(tc.modes)
			if result != tc.expected {
				t.Errorf("chooseMode(%v) = %q, want %q", tc.modes, result, tc.expected)
			}
		})
	}
}

func TestParseIPDiscovery(t *testing.T) {
	t.Run("valid response", func(t *testing.T) {
		response := make([]byte, 74)
		binary.BigEndian.PutUint16(response[0:], 0x2)
		binary.BigEndian.PutUint16(response[2:], 70)
		binary.BigEndian.PutUint32(response[4:], 12345)

		ip := "203.0.113.42"
		copy(response[8:], ip)

		binary.BigEndian.PutUint16(response[72:], 12345)

		parsedIP, parsedPort, err := parseIPDiscovery(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsedIP != "203.0.113.42" {
			t.Errorf("IP = %q, want %q", parsedIP, "203.0.113.42")
		}
		if parsedPort != 12345 {
			t.Errorf("port = %d, want %d", parsedPort, 12345)
		}
	})

	t.Run("invalid response too short", func(t *testing.T) {
		response := make([]byte, 10)
		_, _, err := parseIPDiscovery(response)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})
}

func TestBindCipher_AES(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	conn := &Connection{
		mode:      "aead_aes256_gcm_rtpsize",
		secretKey: key,
	}

	err := conn.bindCipher()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.aead == nil {
		t.Fatalf("expected non-nil aead")
	}

	nonce := make([]byte, conn.aead.NonceSize())
	plaintext := []byte("test data")
	ciphertext := conn.aead.Seal(nil, nonce, plaintext, nil)
	if len(ciphertext) <= len(plaintext) {
		t.Errorf("ciphertext length %d should be > plaintext length %d", len(ciphertext), len(plaintext))
	}
}

func TestBindCipher_ChaCha(t *testing.T) {
	key := make([]byte, chacha20poly1305.KeySize)
	for i := range key {
		key[i] = byte(i)
	}

	conn := &Connection{
		mode:      "aead_xchacha20_poly1305_rtpsize",
		secretKey: key,
	}

	err := conn.bindCipher()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.aead == nil {
		t.Fatalf("expected non-nil aead")
	}

	nonce := make([]byte, conn.aead.NonceSize())
	plaintext := []byte("test data")
	ciphertext := conn.aead.Seal(nil, nonce, plaintext, nil)
	if len(ciphertext) <= len(plaintext) {
		t.Errorf("ciphertext length %d should be > plaintext length %d", len(ciphertext), len(plaintext))
	}
}

func TestBindCipher_UnsupportedMode(t *testing.T) {
	conn := &Connection{
		mode:      "unsupported_mode",
		secretKey: make([]byte, 32),
	}

	err := conn.bindCipher()
	if err == nil {
		t.Errorf("expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "unsupported encryption mode") {
		t.Errorf("error %q should contain %q", err.Error(), "unsupported encryption mode")
	}
}

func TestBindCipher_MissingKey(t *testing.T) {
	conn := &Connection{
		mode:      "aead_aes256_gcm_rtpsize",
		secretKey: nil,
	}

	err := conn.bindCipher()
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestSendRTPPacket(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	conn := &Connection{
		udpConn:   udpConn,
		aead:      aead,
		ssrc:      12345,
		sequence:  100,
		timestamp: 1000,
		nonce:     0,
	}

	opusPayload := []byte{0xFC, 0xFF, 0xFE}
	err = conn.sendRTPPacket(opusPayload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, 1024)
	listener.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := listener.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n <= 12 {
		t.Errorf("packet size %d should be > 12", n)
	}

	if buf[0] != 0x80 {
		t.Errorf("buf[0] = 0x%02X, want 0x80", buf[0])
	}
	if buf[1] != byte(payloadTypeOpus) {
		t.Errorf("buf[1] = 0x%02X, want 0x%02X", buf[1], byte(payloadTypeOpus))
	}
	if got := binary.BigEndian.Uint16(buf[2:4]); got != 100 {
		t.Errorf("sequence = %d, want 100", got)
	}
	if got := binary.BigEndian.Uint32(buf[4:8]); got != 1000 {
		t.Errorf("timestamp = %d, want 1000", got)
	}
	if got := binary.BigEndian.Uint32(buf[8:12]); got != 12345 {
		t.Errorf("ssrc = %d, want 12345", got)
	}

	if conn.sequence != 101 {
		t.Errorf("sequence = %d, want 101", conn.sequence)
	}
	if conn.timestamp != 1960 {
		t.Errorf("timestamp = %d, want 1960", conn.timestamp)
	}
}

func TestWaitUntilConnected(t *testing.T) {
	t.Run("returns immediately when ready", func(t *testing.T) {
		conn := &Connection{
			Ready: true,
		}

		err := conn.WaitUntilConnected(1 * time.Second)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("times out when not ready", func(t *testing.T) {
		conn := &Connection{
			Ready: false,
		}

		start := time.Now()
		err := conn.WaitUntilConnected(300 * time.Millisecond)
		elapsed := time.Since(start)

		if err == nil {
			t.Errorf("expected error, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "timeout") {
			t.Errorf("error %q should contain %q", err.Error(), "timeout")
		}
		if elapsed < 300*time.Millisecond {
			t.Errorf("elapsed %v should be >= 300ms", elapsed)
		}
	})

	t.Run("returns when becomes ready", func(t *testing.T) {
		conn := &Connection{
			Ready: false,
		}

		// Make connection ready after 200ms
		go func() {
			time.Sleep(200 * time.Millisecond)
			conn.Lock()
			conn.Ready = true
			conn.Unlock()
		}()

		start := time.Now()
		err := conn.WaitUntilConnected(1 * time.Second)
		elapsed := time.Since(start)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if elapsed >= 500*time.Millisecond {
			t.Errorf("elapsed %v should be < 500ms", elapsed)
		}
	})
}

func TestClose(t *testing.T) {
	t.Run("closes all resources", func(t *testing.T) {
		// Create a real UDP listener to test cleanup
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listener, err := net.ListenUDP("udp", addr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer listener.Close()

		udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		closeChan := make(chan struct{})
		conn := &Connection{
			Ready:    true,
			speaking: true,
			udpConn:  udpConn,
			close:    closeChan,
		}

		conn.Close()

		if conn.Ready {
			t.Errorf("expected Ready to be false")
		}
		if conn.speaking {
			t.Errorf("expected speaking to be false")
		}
		if conn.udpConn != nil {
			t.Errorf("expected udpConn to be nil")
		}

		// Verify close channel is closed
		select {
		case <-closeChan:
			// Expected - channel is closed
		default:
			t.Error("close channel should be closed")
		}
	})

	t.Run("handles double close gracefully", func(t *testing.T) {
		closeChan := make(chan struct{})
		conn := &Connection{
			Ready: true,
			close: closeChan,
		}

		// Should not panic
		conn.Close()
		conn.Close()

		if conn.Ready {
			t.Errorf("expected Ready to be false")
		}
	})
}

func TestSpeaking(t *testing.T) {
	t.Run("returns error when no websocket", func(t *testing.T) {
		conn := &Connection{
			wsConn: nil,
		}

		err := conn.Speaking(true)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "no voice websocket") {
			t.Errorf("error %q should contain %q", err.Error(), "no voice websocket")
		}
	})
}

func TestOpusSender_ReceivesFromChannel(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create UDP listener
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	closeChan := make(chan struct{})
	opusSend := make(chan []byte, 2)

	conn := &Connection{
		udpConn:  udpConn,
		aead:     aead,
		ssrc:     12345,
		sequence: 0,
		nonce:    0,
		close:    closeChan,
		OpusSend: opusSend,
		Ready:    true,
	}

	// Start the opus sender
	go conn.opusSender()

	// Send a test frame
	testFrame := []byte{0xFC, 0xFF, 0xFE}
	opusSend <- testFrame

	// Read from listener with timeout
	buf := make([]byte, 1024)
	listener.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := listener.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n <= 12 {
		t.Errorf("packet size %d should be > 12", n)
	}

	// Verify RTP header
	if buf[0] != 0x80 {
		t.Errorf("buf[0] = 0x%02X, want 0x80", buf[0])
	}
	if buf[1] != byte(payloadTypeOpus) {
		t.Errorf("buf[1] = 0x%02X, want 0x%02X", buf[1], byte(payloadTypeOpus))
	}

	// Clean up
	close(closeChan)
}

func TestOpusSender_StopsOnClose(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	closeChan := make(chan struct{})
	opusSend := make(chan []byte, 2)

	conn := &Connection{
		udpConn:  udpConn,
		aead:     aead,
		ssrc:     12345,
		close:    closeChan,
		OpusSend: opusSend,
		Ready:    true,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		conn.opusSender()
		wg.Done()
	}()

	// Close the channel
	close(closeChan)

	// Wait for goroutine to exit with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Expected - goroutine exited
	case <-time.After(500 * time.Millisecond):
		t.Error("opusSender did not stop after close signal")
	}
}

func TestUDPKeepAlive_SendsPackets(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	closeChan := make(chan struct{})
	conn := &Connection{
		udpConn: udpConn,
		close:   closeChan,
	}

	// We'll test by modifying the keepalive to use a shorter interval
	// For unit testing, we just verify the function can run and stop

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		conn.udpKeepAlive()
		wg.Done()
	}()

	// Let it run briefly
	time.Sleep(50 * time.Millisecond)

	// Close and verify it stops
	close(closeChan)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(6 * time.Second):
		t.Error("udpKeepAlive did not stop after close signal")
	}
}

func TestGetSilenceFrame(t *testing.T) {
	frame := GetSilenceFrame()
	expected := []byte{0xF8, 0xFF, 0xFE}
	if len(frame) != len(expected) {
		t.Fatalf("frame length = %d, want %d", len(frame), len(expected))
	}
	for i := range expected {
		if frame[i] != expected[i] {
			t.Errorf("frame[%d] = 0x%02X, want 0x%02X", i, frame[i], expected[i])
		}
	}
}

func TestConnectionConfig(t *testing.T) {
	// Test that ConnectionConfig can be created with all fields
	cfg := ConnectionConfig{
		UserID: "123456",
		State: struct {
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
		}{
			GuildID:   "guild123",
			ChannelID: "channel456",
			SessionID: "session789",
		},
		Server: struct {
			Token    string `json:"token"`
			GuildID  string `json:"guild_id"`
			Endpoint string `json:"endpoint"`
		}{
			Token:    "token123",
			GuildID:  "guild123",
			Endpoint: "wss://voice.discord.gg",
		},
		MainGateway: nil,
	}

	if cfg.UserID != "123456" {
		t.Errorf("UserID = %q, want %q", cfg.UserID, "123456")
	}
	if cfg.State.GuildID != "guild123" {
		t.Errorf("State.GuildID = %q, want %q", cfg.State.GuildID, "guild123")
	}
	if cfg.Server.Token != "token123" {
		t.Errorf("Server.Token = %q, want %q", cfg.Server.Token, "token123")
	}
}

func TestSendRTPPacket_NotReady(t *testing.T) {
	conn := &Connection{
		udpConn: nil,
		aead:    nil,
	}

	err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
	if err == nil {
		t.Errorf("expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "not ready") {
		t.Errorf("error %q should contain %q", err.Error(), "not ready")
	}
}

func TestSendRTPPacket_IncrementCounters(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	conn := &Connection{
		udpConn:   udpConn,
		aead:      aead,
		ssrc:      12345,
		sequence:  100,
		timestamp: 1000,
		nonce:     50,
	}

	// Send multiple packets
	for i := 0; i < 3; i++ {
		err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
		if err != nil {
			t.Fatalf("unexpected error on packet %d: %v", i, err)
		}
	}

	// Verify counters incremented correctly
	if conn.sequence != 103 {
		t.Errorf("sequence = %d, want 103", conn.sequence)
	}
	if conn.timestamp != 1000+3*frameSize {
		t.Errorf("timestamp = %d, want %d", conn.timestamp, 1000+3*frameSize)
	}
	if conn.nonce != 53 {
		t.Errorf("nonce = %d, want 53", conn.nonce)
	}
}

func TestVoiceHeartbeatPayloadSerialization(t *testing.T) {
	payload := voiceHeartbeatPayload{T: 1501184119561, SeqAck: 10}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed["t"] != float64(1501184119561) {
		t.Errorf("t = %v, want %v", parsed["t"], float64(1501184119561))
	}
	if parsed["seq_ack"] != float64(10) {
		t.Errorf("seq_ack = %v, want %v", parsed["seq_ack"], float64(10))
	}

	// Verify it matches the v8 format: {"t": <nonce>, "seq_ack": <seq>}
	if len(parsed) != 2 {
		t.Errorf("parsed map length = %d, want 2", len(parsed))
	}

	// Verify negative seq_ack (initial state) serializes correctly
	payload = voiceHeartbeatPayload{T: 42, SeqAck: -1}
	data, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed["seq_ack"] != float64(-1) {
		t.Errorf("seq_ack = %v, want %v", parsed["seq_ack"], float64(-1))
	}
}

func TestVoiceEventSeqDeserialization(t *testing.T) {
	t.Run("parses seq field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "seq": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.Seq == nil {
			t.Fatalf("expected non-nil Seq")
		}
		if *event.Seq != 5 {
			t.Errorf("Seq = %d, want 5", *event.Seq)
		}
	})

	t.Run("does not parse s field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "s": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.Seq != nil {
			t.Errorf("expected nil Seq, got %d", *event.Seq)
		}
	})

	t.Run("seq omitted", func(t *testing.T) {
		raw := `{"op": 6, "d": null}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.Seq != nil {
			t.Errorf("expected nil Seq, got %d", *event.Seq)
		}
	})
}

func TestSendRTPPacket_ConcurrentSafe(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer udpConn.Close()

	conn := &Connection{
		udpConn:   udpConn,
		aead:      aead,
		ssrc:      12345,
		sequence:  0,
		timestamp: 0,
		nonce:     0,
	}

	// Launch multiple goroutines sending packets concurrently
	var wg sync.WaitGroup
	numGoroutines := 10
	packetsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < packetsPerGoroutine; j++ {
				err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// Verify total packets sent (counters should reflect all sends)
	totalPackets := numGoroutines * packetsPerGoroutine
	if conn.sequence != uint16(totalPackets) {
		t.Errorf("sequence = %d, want %d", conn.sequence, totalPackets)
	}
	if conn.timestamp != uint32(totalPackets*frameSize) {
		t.Errorf("timestamp = %d, want %d", conn.timestamp, totalPackets*frameSize)
	}
	if conn.nonce != uint32(totalPackets) {
		t.Errorf("nonce = %d, want %d", conn.nonce, totalPackets)
	}
}

// startMockUDPServer starts a UDP listener that handles IP discovery requests.
// Returns the UDP connection (for cleanup) and its address (for the READY payload).
func startMockUDPServer(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	go func() {
		buf := make([]byte, 74)
		_ = udpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, remoteAddr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n != 74 {
			return
		}

		// Parse request: type 0x0001, length 70, SSRC at bytes 4-7
		ssrc := binary.BigEndian.Uint32(buf[4:8])

		// Build response: type 0x0002, length 70, echoed SSRC, IP at offset 8, port at bytes 72-73
		resp := make([]byte, 74)
		binary.BigEndian.PutUint16(resp[0:], 0x0002)
		binary.BigEndian.PutUint16(resp[2:], 70)
		binary.BigEndian.PutUint32(resp[4:], ssrc)
		copy(resp[8:], "127.0.0.1")
		localPort := udpConn.LocalAddr().(*net.UDPAddr).Port
		binary.BigEndian.PutUint16(resp[72:], uint16(localPort))

		_, _ = udpConn.WriteToUDP(resp, remoteAddr)
	}()

	return udpConn, udpConn.LocalAddr().(*net.UDPAddr)
}

type mockVoiceServer struct {
	heartbeatInterval int
	ssrc              uint32
	udpIP             string
	udpPort           int
	mode              string
	secretKey         []byte

	// Seq values to send with READY and SESSION DESCRIPTION
	readySeq   int
	sessionSeq int

	mu             sync.Mutex
	receivedEvents []voiceEvent

	identifyReceived chan voiceIdentify
	selectReceived   chan selectProtocol
	heartbeats       chan voiceHeartbeatPayload
}

func (m *mockVoiceServer) storeEvent(event voiceEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receivedEvents = append(m.receivedEvents, event)
}

func (m *mockVoiceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wsConn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "done")

	ctx := r.Context()

	// 1. Send HELLO (op 8)
	hello := voiceHello{HeartbeatInterval: m.heartbeatInterval}
	helloData, _ := json.Marshal(hello)
	helloEvent := voiceEvent{Opcode: 8, Data: json.RawMessage(helloData)}
	if err := wsjson.Write(ctx, wsConn, helloEvent); err != nil {
		return
	}

	// 2. Read IDENTIFY (op 0)
	var identifyEvent voiceEvent
	if err := wsjson.Read(ctx, wsConn, &identifyEvent); err != nil {
		return
	}
	m.storeEvent(identifyEvent)
	var identify voiceIdentify
	if err := json.Unmarshal(identifyEvent.Data, &identify); err != nil {
		return
	}
	select {
	case m.identifyReceived <- identify:
	default:
	}

	// 3. Send READY (op 2) with seq
	ready := voiceReady{
		SSRC:  m.ssrc,
		IP:    m.udpIP,
		Port:  m.udpPort,
		Modes: []string{m.mode},
	}
	readyData, _ := json.Marshal(ready)
	readySeq := m.readySeq
	readyEvent := voiceEvent{Opcode: 2, Data: json.RawMessage(readyData), Seq: &readySeq}
	if err := wsjson.Write(ctx, wsConn, readyEvent); err != nil {
		return
	}

	// 4. Read loop until SELECT PROTOCOL (op 1), handling interleaved heartbeats
	for {
		var event voiceEvent
		if err := wsjson.Read(ctx, wsConn, &event); err != nil {
			return
		}
		m.storeEvent(event)

		switch event.Opcode {
		case 3: // Heartbeat - send ACK (op 6)
			ackEvent := voiceEvent{Opcode: 6, Data: event.Data}
			if err := wsjson.Write(ctx, wsConn, ackEvent); err != nil {
				return
			}
		case 1: // SELECT PROTOCOL
			var sp selectProtocol
			if err := json.Unmarshal(event.Data, &sp); err != nil {
				return
			}
			select {
			case m.selectReceived <- sp:
			default:
			}
			goto sendSessionDesc
		}
	}

sendSessionDesc:
	// 5. Send SESSION DESCRIPTION (op 4) with seq
	desc := sessionDescription{
		Mode:      m.mode,
		SecretKey: m.secretKey,
	}
	descData, _ := json.Marshal(desc)
	sessionSeq := m.sessionSeq
	descEvent := voiceEvent{Opcode: 4, Data: json.RawMessage(descData), Seq: &sessionSeq}
	if err := wsjson.Write(ctx, wsConn, descEvent); err != nil {
		return
	}

	// 6. Read loop: handle heartbeats, exit on error
	for {
		var event voiceEvent
		if err := wsjson.Read(ctx, wsConn, &event); err != nil {
			return
		}
		m.storeEvent(event)

		if event.Opcode == 3 {
			var hb voiceHeartbeatPayload
			if err := json.Unmarshal(event.Data, &hb); err != nil {
				return
			}
			select {
			case m.heartbeats <- hb:
			default:
			}

			// Send ACK (op 6)
			ackEvent := voiceEvent{Opcode: 6, Data: event.Data}
			if err := wsjson.Write(ctx, wsConn, ackEvent); err != nil {
				return
			}
		}
	}
}

func TestVoiceConnectionFlow(t *testing.T) {
	// Start mock UDP server
	udpConn, udpAddr := startMockUDPServer(t)
	defer udpConn.Close()

	secretKey := make([]byte, 32)
	for i := range secretKey {
		secretKey[i] = byte(i)
	}

	mockServer := &mockVoiceServer{
		heartbeatInterval: 100,
		ssrc:              12345,
		udpIP:             "127.0.0.1",
		udpPort:           udpAddr.Port,
		mode:              "aead_aes256_gcm_rtpsize",
		secretKey:         secretKey,
		readySeq:          1,
		sessionSeq:        2,
		identifyReceived:  make(chan voiceIdentify, 1),
		selectReceived:    make(chan selectProtocol, 1),
		heartbeats:        make(chan voiceHeartbeatPayload, 10),
	}

	server := httptest.NewServer(mockServer)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Connect(ctx, ConnectionConfig{
		UserID: "user123",
		State: gateway.VoiceState{
			GuildID:   "guild456",
			ChannelID: "channel789",
			SessionID: "session000",
		},
		Server: gateway.VoiceServerUpdate{
			Token:    "voicetoken",
			GuildID:  "guild456",
			Endpoint: wsURL,
		},
		MainGateway: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()

	if !conn.Ready {
		t.Errorf("expected Ready to be true")
	}

	// Verify IDENTIFY payload
	select {
	case identify := <-mockServer.identifyReceived:
		if identify.ServerID != "guild456" {
			t.Errorf("ServerID = %q, want %q", identify.ServerID, "guild456")
		}
		if identify.UserID != "user123" {
			t.Errorf("UserID = %q, want %q", identify.UserID, "user123")
		}
		if identify.SessionID != "session000" {
			t.Errorf("SessionID = %q, want %q", identify.SessionID, "session000")
		}
		if identify.Token != "voicetoken" {
			t.Errorf("Token = %q, want %q", identify.Token, "voicetoken")
		}
		if identify.Video {
			t.Errorf("expected Video to be false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for IDENTIFY")
	}

	// Verify SELECT PROTOCOL payload
	select {
	case sp := <-mockServer.selectReceived:
		if sp.Protocol != "udp" {
			t.Errorf("Protocol = %q, want %q", sp.Protocol, "udp")
		}
		if sp.Data.Address != "127.0.0.1" {
			t.Errorf("Address = %q, want %q", sp.Data.Address, "127.0.0.1")
		}
		if sp.Data.Port != udpAddr.Port {
			t.Errorf("Port = %d, want %d", sp.Data.Port, udpAddr.Port)
		}
		if sp.Data.Mode != "aead_aes256_gcm_rtpsize" {
			t.Errorf("Mode = %q, want %q", sp.Data.Mode, "aead_aes256_gcm_rtpsize")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SELECT PROTOCOL")
	}

	// Verify internal state
	if conn.ssrc != 12345 {
		t.Errorf("ssrc = %d, want 12345", conn.ssrc)
	}
	if conn.mode != "aead_aes256_gcm_rtpsize" {
		t.Errorf("mode = %q, want %q", conn.mode, "aead_aes256_gcm_rtpsize")
	}
	if string(conn.secretKey) != string(secretKey) {
		t.Errorf("secretKey mismatch")
	}
	if conn.aead == nil {
		t.Errorf("expected non-nil aead")
	}
	if conn.udpConn == nil {
		t.Errorf("expected non-nil udpConn")
	}

	// Verify seq tracking: READY had seq 1, SESSION DESCRIPTION had seq 2
	if conn.seqAck.Load() != 2 {
		t.Errorf("seqAck = %d, want 2", conn.seqAck.Load())
	}

	// Verify heartbeat v8 format
	select {
	case hb := <-mockServer.heartbeats:
		if hb.T == 0 {
			t.Errorf("expected non-zero T")
		}
		if hb.SeqAck != 2 {
			t.Errorf("SeqAck = %d, want 2", hb.SeqAck)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for heartbeat")
	}
}

func TestSeqAckTracking(t *testing.T) {
	// Start mock UDP server
	udpConn, udpAddr := startMockUDPServer(t)
	defer udpConn.Close()

	secretKey := make([]byte, 32)
	for i := range secretKey {
		secretKey[i] = byte(i)
	}

	mockServer := &mockVoiceServer{
		heartbeatInterval: 100,
		ssrc:              54321,
		udpIP:             "127.0.0.1",
		udpPort:           udpAddr.Port,
		mode:              "aead_aes256_gcm_rtpsize",
		secretKey:         secretKey,
		readySeq:          5,
		sessionSeq:        10,
		identifyReceived:  make(chan voiceIdentify, 1),
		selectReceived:    make(chan selectProtocol, 1),
		heartbeats:        make(chan voiceHeartbeatPayload, 10),
	}

	server := httptest.NewServer(mockServer)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Connect(ctx, ConnectionConfig{
		UserID: "user999",
		State: gateway.VoiceState{
			GuildID:   "guild111",
			ChannelID: "channel222",
			SessionID: "session333",
		},
		Server: gateway.VoiceServerUpdate{
			Token:    "token444",
			GuildID:  "guild111",
			Endpoint: wsURL,
		},
		MainGateway: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()

	// After Connect: seqAck should be 10 (last seq from SESSION DESCRIPTION)
	if conn.seqAck.Load() != 10 {
		t.Errorf("seqAck = %d, want 10", conn.seqAck.Load())
	}

	// First heartbeat should echo seq_ack: 10
	select {
	case hb := <-mockServer.heartbeats:
		if hb.T == 0 {
			t.Errorf("expected non-zero T")
		}
		if hb.SeqAck != 10 {
			t.Errorf("SeqAck = %d, want 10", hb.SeqAck)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for heartbeat")
	}
}
