package voice

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Equal(t, tc.expected, result)
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
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.42", parsedIP)
		assert.Equal(t, 12345, parsedPort)
	})

	t.Run("invalid response too short", func(t *testing.T) {
		response := make([]byte, 10)
		_, _, err := parseIPDiscovery(response)
		assert.Error(t, err)
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
	require.NoError(t, err)
	require.NotNil(t, conn.aead)

	nonce := make([]byte, conn.aead.NonceSize())
	plaintext := []byte("test data")
	ciphertext := conn.aead.Seal(nil, nonce, plaintext, nil)
	assert.Greater(t, len(ciphertext), len(plaintext))
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
	require.NoError(t, err)
	require.NotNil(t, conn.aead)

	nonce := make([]byte, conn.aead.NonceSize())
	plaintext := []byte("test data")
	ciphertext := conn.aead.Seal(nil, nonce, plaintext, nil)
	assert.Greater(t, len(ciphertext), len(plaintext))
}

func TestBindCipher_UnsupportedMode(t *testing.T) {
	conn := &Connection{
		mode:      "unsupported_mode",
		secretKey: make([]byte, 32),
	}

	err := conn.bindCipher()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported encryption mode")
}

func TestBindCipher_MissingKey(t *testing.T) {
	conn := &Connection{
		mode:      "aead_aes256_gcm_rtpsize",
		secretKey: nil,
	}

	err := conn.bindCipher()
	assert.Error(t, err)
}

func TestSendRTPPacket(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
	require.NoError(t, err)

	buf := make([]byte, 1024)
	listener.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := listener.Read(buf)
	require.NoError(t, err)
	assert.Greater(t, n, 12)

	assert.Equal(t, byte(0x80), buf[0])
	assert.Equal(t, byte(payloadTypeOpus), buf[1])
	assert.Equal(t, uint16(100), binary.BigEndian.Uint16(buf[2:4]))
	assert.Equal(t, uint32(1000), binary.BigEndian.Uint32(buf[4:8]))
	assert.Equal(t, uint32(12345), binary.BigEndian.Uint32(buf[8:12]))

	assert.Equal(t, uint16(101), conn.sequence)
	assert.Equal(t, uint32(1960), conn.timestamp)
}

func TestWaitUntilConnected(t *testing.T) {
	t.Run("returns immediately when ready", func(t *testing.T) {
		conn := &Connection{
			Ready: true,
		}

		err := conn.WaitUntilConnected(1 * time.Second)
		assert.NoError(t, err)
	})

	t.Run("times out when not ready", func(t *testing.T) {
		conn := &Connection{
			Ready: false,
		}

		start := time.Now()
		err := conn.WaitUntilConnected(300 * time.Millisecond)
		elapsed := time.Since(start)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
		assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond)
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

		assert.NoError(t, err)
		assert.Less(t, elapsed, 500*time.Millisecond)
	})
}

func TestClose(t *testing.T) {
	t.Run("closes all resources", func(t *testing.T) {
		// Create a real UDP listener to test cleanup
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
		require.NoError(t, err)
		listener, err := net.ListenUDP("udp", addr)
		require.NoError(t, err)
		defer listener.Close()

		udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
		require.NoError(t, err)

		closeChan := make(chan struct{})
		conn := &Connection{
			Ready:    true,
			speaking: true,
			udpConn:  udpConn,
			close:    closeChan,
		}

		conn.Close()

		assert.False(t, conn.Ready)
		assert.False(t, conn.speaking)
		assert.Nil(t, conn.udpConn)

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

		assert.False(t, conn.Ready)
	})
}

func TestSpeaking(t *testing.T) {
	t.Run("returns error when no websocket", func(t *testing.T) {
		conn := &Connection{
			wsConn: nil,
		}

		err := conn.Speaking(true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no voice websocket")
	})
}

func TestOpusSender_ReceivesFromChannel(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)

	// Create UDP listener
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
	require.NoError(t, err)
	assert.Greater(t, n, 12)

	// Verify RTP header
	assert.Equal(t, byte(0x80), buf[0])
	assert.Equal(t, byte(payloadTypeOpus), buf[1])

	// Clean up
	close(closeChan)
}

func TestOpusSender_StopsOnClose(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
	assert.Equal(t, []byte{0xF8, 0xFF, 0xFE}, frame)
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

	assert.Equal(t, "123456", cfg.UserID)
	assert.Equal(t, "guild123", cfg.State.GuildID)
	assert.Equal(t, "token123", cfg.Server.Token)
}

func TestSendRTPPacket_NotReady(t *testing.T) {
	conn := &Connection{
		udpConn: nil,
		aead:    nil,
	}

	err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestSendRTPPacket_IncrementCounters(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
		require.NoError(t, err)
	}

	// Verify counters incremented correctly
	assert.Equal(t, uint16(103), conn.sequence)
	assert.Equal(t, uint32(1000+3*frameSize), conn.timestamp)
	assert.Equal(t, uint32(53), conn.nonce)
}

func TestVoiceHeartbeatPayloadSerialization(t *testing.T) {
	payload := voiceHeartbeatPayload{T: 1501184119561, SeqAck: 10}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, float64(1501184119561), parsed["t"])
	assert.Equal(t, float64(10), parsed["seq_ack"])

	// Verify it matches the v8 format: {"t": <nonce>, "seq_ack": <seq>}
	assert.Len(t, parsed, 2)

	// Verify negative seq_ack (initial state) serializes correctly
	payload = voiceHeartbeatPayload{T: 42, SeqAck: -1}
	data, err = json.Marshal(payload)
	require.NoError(t, err)

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, float64(-1), parsed["seq_ack"])
}

func TestVoiceEventSeqDeserialization(t *testing.T) {
	t.Run("parses seq field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "seq": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		require.NoError(t, err)
		require.NotNil(t, event.Seq)
		assert.Equal(t, 5, *event.Seq)
	})

	t.Run("does not parse s field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "s": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		require.NoError(t, err)
		assert.Nil(t, event.Seq)
	})

	t.Run("seq omitted", func(t *testing.T) {
		raw := `{"op": 6, "d": null}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		require.NoError(t, err)
		assert.Nil(t, event.Seq)
	})
}

func TestSendRTPPacket_ConcurrentSafe(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	listener, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
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
				assert.NoError(t, err)
			}
		}()
	}

	wg.Wait()

	// Verify total packets sent (counters should reflect all sends)
	totalPackets := numGoroutines * packetsPerGoroutine
	assert.Equal(t, uint16(totalPackets), conn.sequence)
	assert.Equal(t, uint32(totalPackets*frameSize), conn.timestamp)
	assert.Equal(t, uint32(totalPackets), conn.nonce)
}
