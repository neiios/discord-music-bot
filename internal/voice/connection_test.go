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
	"github.com/neiios/discord-music-bot/internal/assert"
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
			assert.Equal(t, result, tc.expected)
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
		assert.NoErrf(t, err)
		assert.Equal(t, parsedIP, "203.0.113.42")
		assert.Equal(t, parsedPort, 12345)
	})

	t.Run("invalid response too short", func(t *testing.T) {
		response := make([]byte, 10)
		_, _, err := parseIPDiscovery(response)
		assert.IsErr(t, err)
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
	assert.NoErrf(t, err)
	assert.NotNilf(t, conn.aead)

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
	assert.NoErrf(t, err)
	assert.NotNilf(t, conn.aead)

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
	assert.ErrContains(t, err, "unsupported encryption mode")
}

func TestBindCipher_MissingKey(t *testing.T) {
	conn := &Connection{
		mode:      "aead_aes256_gcm_rtpsize",
		secretKey: nil,
	}

	err := conn.bindCipher()
	assert.IsErr(t, err)
}

func TestSendRTPPacket(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	assert.NoErrf(t, err)
	aead, err := cipher.NewGCM(block)
	assert.NoErrf(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
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
	assert.NoErrf(t, err)

	buf := make([]byte, 1024)
	listener.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := listener.Read(buf)
	assert.NoErrf(t, err)
	assert.Greater(t, n, 12)

	assert.Equal(t, buf[0], byte(0x80))
	assert.Equal(t, buf[1], byte(payloadTypeOpus))
	assert.Equal(t, binary.BigEndian.Uint16(buf[2:4]), uint16(100))
	assert.Equal(t, binary.BigEndian.Uint32(buf[4:8]), uint32(1000))
	assert.Equal(t, binary.BigEndian.Uint32(buf[8:12]), uint32(12345))

	assert.Equal(t, conn.sequence, uint16(101))
	assert.Equal(t, conn.timestamp, uint32(1960))
}

func TestWaitUntilConnected(t *testing.T) {
	t.Run("returns immediately when ready", func(t *testing.T) {
		readyCh := make(chan struct{})
		close(readyCh)
		conn := &Connection{
			Ready:   true,
			readyCh: readyCh,
		}

		err := conn.WaitUntilConnected(1 * time.Second)
		assert.NoErr(t, err)
	})

	t.Run("times out when not ready", func(t *testing.T) {
		conn := &Connection{
			Ready:   false,
			readyCh: make(chan struct{}),
		}

		start := time.Now()
		err := conn.WaitUntilConnected(300 * time.Millisecond)
		elapsed := time.Since(start)

		assert.ErrContains(t, err, "timeout")
		assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond)
	})

	t.Run("returns when becomes ready", func(t *testing.T) {
		readyCh := make(chan struct{})
		conn := &Connection{
			Ready:   false,
			readyCh: readyCh,
		}

		go func() {
			time.Sleep(200 * time.Millisecond)
			conn.Lock()
			conn.Ready = true
			close(readyCh)
			conn.Unlock()
		}()

		start := time.Now()
		err := conn.WaitUntilConnected(1 * time.Second)
		elapsed := time.Since(start)

		assert.NoErr(t, err)
		assert.Less(t, elapsed, 500*time.Millisecond)
	})
}

func TestClose(t *testing.T) {
	t.Run("closes all resources", func(t *testing.T) {
		addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
		assert.NoErrf(t, err)
		listener, err := net.ListenUDP("udp", addr)
		assert.NoErrf(t, err)
		defer listener.Close()

		udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
		assert.NoErrf(t, err)

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
		assert.ChanClosed(t, closeChan)
	})

	t.Run("handles double close gracefully", func(t *testing.T) {
		closeChan := make(chan struct{})
		conn := &Connection{
			Ready: true,
			close: closeChan,
		}

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
		assert.ErrContains(t, err, "no voice websocket")
	})
}

func TestOpusSender_ReceivesFromChannel(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	assert.NoErrf(t, err)
	aead, err := cipher.NewGCM(block)
	assert.NoErrf(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
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

	go conn.opusSender()

	testFrame := []byte{0xFC, 0xFF, 0xFE}
	opusSend <- testFrame

	buf := make([]byte, 1024)
	listener.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := listener.Read(buf)
	assert.NoErrf(t, err)
	assert.Greater(t, n, 12)

	assert.Equal(t, buf[0], byte(0x80))
	assert.Equal(t, buf[1], byte(payloadTypeOpus))

	close(closeChan)
}

func TestOpusSender_StopsOnClose(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	assert.NoErrf(t, err)
	aead, err := cipher.NewGCM(block)
	assert.NoErrf(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
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

	close(closeChan)

	done := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		done <- struct{}{}
	}()

	assert.Recv(t, done, 500*time.Millisecond)
}

func TestUDPKeepAlive_SendsPackets(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
	defer udpConn.Close()

	closeChan := make(chan struct{})
	conn := &Connection{
		udpConn: udpConn,
		close:   closeChan,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		conn.udpKeepAlive()
		wg.Done()
	}()

	time.Sleep(50 * time.Millisecond)

	close(closeChan)

	done := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		done <- struct{}{}
	}()

	assert.Recv(t, done, 6*time.Second)
}

func TestGetSilenceFrame(t *testing.T) {
	frame := GetSilenceFrame()
	expected := []byte{0xF8, 0xFF, 0xFE}
	assert.SlicesEqual(t, frame, expected)
}

func TestConnectionConfig(t *testing.T) {
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

	assert.Equal(t, cfg.UserID, "123456")
	assert.Equal(t, cfg.State.GuildID, "guild123")
	assert.Equal(t, cfg.Server.Token, "token123")
}

func TestSendRTPPacket_NotReady(t *testing.T) {
	conn := &Connection{
		udpConn: nil,
		aead:    nil,
	}

	err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
	assert.ErrContains(t, err, "not ready")
}

func TestSendRTPPacket_IncrementCounters(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	assert.NoErrf(t, err)
	aead, err := cipher.NewGCM(block)
	assert.NoErrf(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
	defer udpConn.Close()

	conn := &Connection{
		udpConn:   udpConn,
		aead:      aead,
		ssrc:      12345,
		sequence:  100,
		timestamp: 1000,
		nonce:     50,
	}

	for i := 0; i < 3; i++ {
		err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
		assert.NoErrf(t, err)
	}

	assert.Equal(t, conn.sequence, uint16(103))
	assert.Equal(t, conn.timestamp, uint32(1000+3*frameSize))
	assert.Equal(t, conn.nonce, uint32(53))
}

func TestVoiceHeartbeatPayloadSerialization(t *testing.T) {
	payload := voiceHeartbeatPayload{T: 1501184119561, SeqAck: 10}
	data, err := json.Marshal(payload)
	assert.NoErrf(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	assert.NoErrf(t, err)

	assert.Equal(t, parsed["t"], any(float64(1501184119561)))
	assert.Equal(t, parsed["seq_ack"], any(float64(10)))
	assert.Equal(t, len(parsed), 2)

	payload = voiceHeartbeatPayload{T: 42, SeqAck: -1}
	data, err = json.Marshal(payload)
	assert.NoErrf(t, err)

	err = json.Unmarshal(data, &parsed)
	assert.NoErrf(t, err)
	assert.Equal(t, parsed["seq_ack"], any(float64(-1)))
}

func TestVoiceEventSeqDeserialization(t *testing.T) {
	t.Run("parses seq field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "seq": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		assert.NoErrf(t, err)
		assert.DerefEqual(t, event.Seq, 5)
	})

	t.Run("does not parse s field", func(t *testing.T) {
		raw := `{"op": 6, "d": null, "s": 5}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		assert.NoErrf(t, err)
		assert.Nil(t, event.Seq)
	})

	t.Run("seq omitted", func(t *testing.T) {
		raw := `{"op": 6, "d": null}`
		var event voiceEvent
		err := json.Unmarshal([]byte(raw), &event)
		assert.NoErrf(t, err)
		assert.Nil(t, event.Seq)
	})
}

func TestSendRTPPacket_ConcurrentSafe(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	block, err := aes.NewCipher(key)
	assert.NoErrf(t, err)
	aead, err := cipher.NewGCM(block)
	assert.NoErrf(t, err)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	listener, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)
	defer listener.Close()

	udpConn, err := net.DialUDP("udp", nil, listener.LocalAddr().(*net.UDPAddr))
	assert.NoErrf(t, err)
	defer udpConn.Close()

	conn := &Connection{
		udpConn:   udpConn,
		aead:      aead,
		ssrc:      12345,
		sequence:  0,
		timestamp: 0,
		nonce:     0,
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	packetsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < packetsPerGoroutine; j++ {
				err := conn.sendRTPPacket([]byte{0xFC, 0xFF, 0xFE})
				assert.NoErr(t, err)
			}
		}()
	}

	wg.Wait()

	totalPackets := numGoroutines * packetsPerGoroutine
	assert.Equal(t, conn.sequence, uint16(totalPackets))
	assert.Equal(t, conn.timestamp, uint32(totalPackets*frameSize))
	assert.Equal(t, conn.nonce, uint32(totalPackets))
}

func startMockUDPServer(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	assert.NoErrf(t, err)
	udpConn, err := net.ListenUDP("udp", addr)
	assert.NoErrf(t, err)

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

		ssrc := binary.BigEndian.Uint32(buf[4:8])

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

	hello := voiceHello{HeartbeatInterval: m.heartbeatInterval}
	helloData, _ := json.Marshal(hello)
	helloEvent := voiceEvent{Opcode: 8, Data: json.RawMessage(helloData)}
	if err := wsjson.Write(ctx, wsConn, helloEvent); err != nil {
		return
	}

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

	for {
		var event voiceEvent
		if err := wsjson.Read(ctx, wsConn, &event); err != nil {
			return
		}
		m.storeEvent(event)

		switch event.Opcode {
		case 3:
			ackEvent := voiceEvent{Opcode: 6, Data: event.Data}
			if err := wsjson.Write(ctx, wsConn, ackEvent); err != nil {
				return
			}
		case 1:
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

			ackEvent := voiceEvent{Opcode: 6, Data: event.Data}
			if err := wsjson.Write(ctx, wsConn, ackEvent); err != nil {
				return
			}
		}
	}
}

func TestVoiceConnectionFlow(t *testing.T) {
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
	assert.NoErrf(t, err)
	defer conn.Close()

	assert.True(t, conn.Ready)

	identify := assert.Recv(t, mockServer.identifyReceived, 2*time.Second)
	assert.Equal(t, identify.ServerID, "guild456")
	assert.Equal(t, identify.UserID, "user123")
	assert.Equal(t, identify.SessionID, "session000")
	assert.Equal(t, identify.Token, "voicetoken")
	assert.False(t, identify.Video)

	sp := assert.Recv(t, mockServer.selectReceived, 2*time.Second)
	assert.Equal(t, sp.Protocol, "udp")
	assert.Equal(t, sp.Data.Address, "127.0.0.1")
	assert.Equal(t, sp.Data.Port, udpAddr.Port)
	assert.Equal(t, sp.Data.Mode, "aead_aes256_gcm_rtpsize")

	assert.Equal(t, conn.ssrc, uint32(12345))
	assert.Equal(t, conn.mode, "aead_aes256_gcm_rtpsize")
	assert.SlicesEqual(t, conn.secretKey, secretKey)
	assert.NotNil(t, conn.aead)
	assert.NotNil(t, conn.udpConn)

	assert.Equal(t, conn.seqAck.Load(), int64(2))

	hb := assert.Recv(t, mockServer.heartbeats, 500*time.Millisecond)
	assert.True(t, hb.T != 0, "expected non-zero T")
	assert.Equal(t, hb.SeqAck, 2)
}

func TestSeqAckTracking(t *testing.T) {
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
	assert.NoErrf(t, err)
	defer conn.Close()

	assert.Equal(t, conn.seqAck.Load(), int64(10))

	hb := assert.Recv(t, mockServer.heartbeats, 500*time.Millisecond)
	assert.True(t, hb.T != 0, "expected non-zero T")
	assert.Equal(t, hb.SeqAck, 10)
}
