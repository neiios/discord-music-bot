package voice

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/gateway"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	payloadTypeOpus = 0x78
	frameSize       = 960 // 20ms at 48kHz
	frameDuration   = 20 * time.Millisecond
)

var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

type voiceEvent struct {
	Opcode int             `json:"op"`
	Data   json.RawMessage `json:"d"`
	Seq    *int            `json:"seq,omitempty"`
	Type   *string         `json:"t,omitempty"`
}

type voiceHeartbeatPayload struct {
	T      int64 `json:"t"`
	SeqAck int   `json:"seq_ack"`
}

type voiceHello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type voiceIdentify struct {
	ServerID  string `json:"server_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	Video     bool   `json:"video"`
}

type voiceReady struct {
	SSRC  uint32   `json:"ssrc"`
	IP    string   `json:"ip"`
	Port  int      `json:"port"`
	Modes []string `json:"modes"`
}

type selectProtocol struct {
	Protocol string             `json:"protocol"`
	Data     selectProtocolData `json:"data"`
}

type selectProtocolData struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Mode    string `json:"mode"`
}

type sessionDescription struct {
	Mode      string `json:"mode"`
	SecretKey []byte `json:"secret_key"`
}

type speakingPayload struct {
	Speaking int    `json:"speaking"`
	Delay    int    `json:"delay"`
	SSRC     uint32 `json:"ssrc"`
}

// Connection represents a voice connection to Discord.
// It manages both WebSocket and UDP connections for voice communication.
type Connection struct {
	sync.RWMutex

	// Public state
	Ready bool // If true, voice is ready to send audio

	// Channel for sending opus audio frames
	OpusSend chan []byte

	// Internal connections
	wsConn  *websocket.Conn
	wsMutex sync.Mutex
	udpConn *net.UDPConn
	udpAddr *net.UDPAddr

	// Encryption
	mode      string
	secretKey []byte
	aead      cipher.AEAD

	// RTP state
	ssrc      uint32
	sequence  uint16
	timestamp uint32
	nonce     uint32

	// Sequence tracking for v8 heartbeats
	seqAck atomic.Int64

	// State flags
	speaking     bool
	reconnecting bool

	// Signal channel for closing goroutines
	close chan struct{}

	// Connection info for reconnection
	userID    string
	guildID   string
	channelID string
	sessionID string
	token     string
	endpoint  string

	// References for reconnection
	mainGateway *gateway.Connection
}

// ConnectionConfig holds the configuration for establishing a voice connection.
type ConnectionConfig struct {
	UserID      string
	State       gateway.VoiceState
	Server      gateway.VoiceServerUpdate
	MainGateway *gateway.Connection
}

// Connect establishes a new voice connection to Discord.
func Connect(ctx context.Context, cfg ConnectionConfig) (*Connection, error) {
	endpoint := cfg.Server.Endpoint
	if !strings.HasPrefix(endpoint, "wss://") && !strings.HasPrefix(endpoint, "ws://") {
		endpoint = "wss://" + endpoint
	}
	if !strings.Contains(endpoint, "?") {
		endpoint = endpoint + "?v=8"
	} else if !strings.Contains(endpoint, "v=8") {
		endpoint = endpoint + "&v=8"
	}

	wsConn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial voice gateway: %w", err)
	}
	slog.Info("connected to voice gateway", "endpoint", endpoint)

	c := &Connection{
		wsConn:      wsConn,
		userID:      cfg.UserID,
		guildID:     cfg.State.GuildID,
		channelID:   cfg.State.ChannelID,
		sessionID:   cfg.State.SessionID,
		token:       cfg.Server.Token,
		endpoint:    cfg.Server.Endpoint,
		mainGateway: cfg.MainGateway,
		close:       make(chan struct{}),
		OpusSend:    make(chan []byte, 2),
	}
	c.seqAck.Store(-1)

	if err := c.establish(ctx, cfg.State, cfg.Server); err != nil {
		c.Close()
		return nil, err
	}

	return c, nil
}

// Close closes the voice connection and all associated resources.
func (c *Connection) Close() {
	c.Lock()
	defer c.Unlock()

	c.Ready = false
	c.speaking = false

	// Signal all goroutines to stop
	if c.close != nil {
		select {
		case <-c.close:
			// Already closed
		default:
			close(c.close)
		}
	}

	if c.udpConn != nil {
		c.udpConn.Close()
		c.udpConn = nil
	}

	if c.wsConn != nil {
		c.wsConn.Close(websocket.StatusNormalClosure, "closing")
		c.wsConn = nil
	}
}

// WaitUntilConnected blocks until the connection is ready or the timeout expires.
func (c *Connection) WaitUntilConnected(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c.RLock()
		ready := c.Ready
		c.RUnlock()

		if ready {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for voice connection")
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// Speaking sends a speaking notification to Discord over the voice websocket.
func (c *Connection) Speaking(enabled bool) error {
	c.RLock()
	wsConn := c.wsConn
	ssrc := c.ssrc
	c.RUnlock()

	if wsConn == nil {
		return fmt.Errorf("no voice websocket connection")
	}

	speaking := 0
	if enabled {
		speaking = 1
	}

	payload := speakingPayload{Speaking: speaking, Delay: 0, SSRC: ssrc}
	if err := c.sendEvent(context.Background(), 5, payload); err != nil {
		return err
	}

	c.Lock()
	c.speaking = enabled
	c.Unlock()

	return nil
}

func (c *Connection) establish(ctx context.Context, state gateway.VoiceState, server gateway.VoiceServerUpdate) error {
	helloEvent, err := c.readEvent(ctx)
	if err != nil {
		return fmt.Errorf("failed to read hello: %w", err)
	}
	if helloEvent.Opcode != 8 {
		return fmt.Errorf("expected hello event (op 8), got opcode %d", helloEvent.Opcode)
	}

	var hello voiceHello
	if err := json.Unmarshal(helloEvent.Data, &hello); err != nil {
		return fmt.Errorf("failed to unmarshal hello: %w", err)
	}

	identify := voiceIdentify{
		ServerID:  state.GuildID,
		UserID:    c.userID,
		SessionID: state.SessionID,
		Token:     server.Token,
		Video:     false,
	}
	if err := c.sendEvent(ctx, 0, identify); err != nil {
		return fmt.Errorf("failed to send identify: %w", err)
	}

	// Start heartbeat
	go c.wsHeartbeat(time.Duration(hello.HeartbeatInterval) * time.Millisecond)

	var ready voiceReady
	modeSelected := ""

	for {
		event, err := c.readEvent(ctx)
		if err != nil {
			return fmt.Errorf("failed to read event during establish: %w", err)
		}

		switch event.Opcode {
		case 2: // READY
			if err := json.Unmarshal(event.Data, &ready); err != nil {
				return fmt.Errorf("failed to unmarshal ready: %w", err)
			}
			slog.Info("voice ready", "ssrc", ready.SSRC, "ip", ready.IP, "port", ready.Port)

			modeSelected = chooseMode(ready.Modes)
			if modeSelected == "" {
				return fmt.Errorf("no supported encryption modes available")
			}

			if err := c.setupUDP(ctx, ready, modeSelected); err != nil {
				return fmt.Errorf("failed to setup UDP: %w", err)
			}

		case 4: // SESSION DESCRIPTION
			var desc sessionDescription
			if err := json.Unmarshal(event.Data, &desc); err != nil {
				return fmt.Errorf("failed to unmarshal session description: %w", err)
			}
			c.mode = desc.Mode
			c.secretKey = desc.SecretKey

			if err := c.bindCipher(); err != nil {
				return fmt.Errorf("failed to bind cipher: %w", err)
			}

			// Start background goroutines
			go c.wsListen()
			go c.udpKeepAlive()
			go c.opusSender()

			// Mark as ready
			c.Lock()
			c.Ready = true
			c.Unlock()

			slog.Info("voice connection established", "mode", c.mode)
			return nil

		case 6: // HEARTBEAT ACK
			slog.Debug("voice heartbeat ack during establish")

		default:
			slog.Debug("voice gateway event during establish", "opcode", event.Opcode)
		}
	}
}

func (c *Connection) setupUDP(ctx context.Context, ready voiceReady, mode string) error {
	address := fmt.Sprintf("%s:%d", ready.IP, ready.Port)
	remoteAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		return fmt.Errorf("failed to dial UDP: %w", err)
	}

	// IP Discovery
	discovery := make([]byte, 74)
	binary.BigEndian.PutUint16(discovery[0:], 0x1)
	binary.BigEndian.PutUint16(discovery[2:], 70)
	binary.BigEndian.PutUint32(discovery[4:], ready.SSRC)

	response := make([]byte, 74)
	var ip string
	var port int

	for attempt := 0; attempt < 3; attempt++ {
		if err := udpConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			udpConn.Close()
			return fmt.Errorf("failed to set UDP deadline: %w", err)
		}

		if _, err := udpConn.Write(discovery); err != nil {
			udpConn.Close()
			return fmt.Errorf("failed to write discovery: %w", err)
		}

		n, err := udpConn.Read(response)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				slog.Warn("UDP discovery timed out, retrying", "attempt", attempt+1)
				continue
			}
			udpConn.Close()
			return fmt.Errorf("failed to read discovery response: %w", err)
		}

		parsedIP, parsedPort, err := parseIPDiscovery(response[:n])
		if err != nil {
			udpConn.Close()
			return fmt.Errorf("failed to parse IP discovery: %w", err)
		}

		ip = parsedIP
		port = parsedPort
		break
	}

	if ip == "" {
		udpConn.Close()
		return fmt.Errorf("UDP discovery failed after retries")
	}

	// Send select protocol
	payload := selectProtocol{
		Protocol: "udp",
		Data: selectProtocolData{
			Address: ip,
			Port:    port,
			Mode:    mode,
		},
	}
	if err := c.sendEvent(ctx, 1, payload); err != nil {
		udpConn.Close()
		return fmt.Errorf("failed to send select protocol: %w", err)
	}

	// Clear deadline
	if err := udpConn.SetDeadline(time.Time{}); err != nil {
		udpConn.Close()
		return fmt.Errorf("failed to clear UDP deadline: %w", err)
	}

	c.udpConn = udpConn
	c.udpAddr = remoteAddr
	c.ssrc = ready.SSRC

	// Initialize RTP state with random values
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	c.sequence = uint16(seed.Uint32())
	c.timestamp = seed.Uint32()
	c.nonce = seed.Uint32()

	slog.Info("UDP connection established", "localIP", ip, "localPort", port)
	return nil
}

// wsHeartbeat sends regular heartbeats to keep the voice connection alive.
func (c *Connection) wsHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	nonce := rand.Int63()

	for {
		select {
		case <-c.close:
			return
		case <-ticker.C:
			nonce++
			payload := voiceHeartbeatPayload{T: nonce, SeqAck: int(c.seqAck.Load())}
			if err := c.sendEvent(context.Background(), 3, payload); err != nil {
				slog.Error("voice heartbeat failed", "error", err)
				go c.reconnect()
				return
			}
			slog.Debug("voice heartbeat sent")
		}
	}
}

// wsListen listens for voice websocket events.
func (c *Connection) wsListen() {
	for {
		c.RLock()
		wsConn := c.wsConn
		c.RUnlock()

		if wsConn == nil {
			return
		}

		event, err := c.readEvent(context.Background())
		if err != nil {
			select {
			case <-c.close:
				return
			default:
				status := websocket.CloseStatus(err)
				if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
					return
				}
				// Check for code 4014 (manual disconnection)
				if status == 4014 {
					slog.Info("received 4014 manual disconnection")
					c.Close()
					return
				}
				slog.Error("voice gateway read failed", "error", err)
				go c.reconnect()
				return
			}
		}

		switch event.Opcode {
		case 6: // HEARTBEAT ACK
			slog.Debug("voice heartbeat ack")
		case 9: // RESUMED
			slog.Info("voice resumed")
		case 13: // CLIENT DISCONNECT
			slog.Info("voice client disconnect")
		default:
			slog.Debug("voice gateway event", "opcode", event.Opcode)
		}
	}
}

// udpKeepAlive sends UDP keepalive packets to maintain the connection.
func (c *Connection) udpKeepAlive() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var sequence uint64
	packet := make([]byte, 8)

	for {
		select {
		case <-c.close:
			return
		case <-ticker.C:
			c.RLock()
			udpConn := c.udpConn
			c.RUnlock()

			if udpConn == nil {
				return
			}

			binary.LittleEndian.PutUint64(packet, sequence)
			sequence++

			if _, err := udpConn.Write(packet); err != nil {
				slog.Error("UDP keepalive failed", "error", err)
				return
			}
			slog.Debug("UDP keepalive sent")
		}
	}
}

// opusSender reads opus frames from OpusSend and sends them over UDP.
func (c *Connection) opusSender() {
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	for {
		select {
		case <-c.close:
			return
		case frame, ok := <-c.OpusSend:
			if !ok {
				return
			}

			// Auto-send speaking notification
			c.RLock()
			speaking := c.speaking
			c.RUnlock()

			if !speaking {
				if err := c.Speaking(true); err != nil {
					slog.Error("failed to send speaking notification", "error", err)
				}
			}

			// Send the frame
			if err := c.sendRTPPacket(frame); err != nil {
				slog.Error("failed to send RTP packet", "error", err)
				go c.reconnect()
				return
			}

			// Wait for next tick to maintain timing
			select {
			case <-c.close:
				return
			case <-ticker.C:
				// Continue
			}
		}
	}
}

func (c *Connection) sendRTPPacket(payload []byte) error {
	c.Lock()
	defer c.Unlock()

	if c.udpConn == nil || c.aead == nil {
		return fmt.Errorf("voice connection not ready")
	}

	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = payloadTypeOpus
	binary.BigEndian.PutUint16(header[2:], c.sequence)
	binary.BigEndian.PutUint32(header[4:], c.timestamp)
	binary.BigEndian.PutUint32(header[8:], c.ssrc)

	c.sequence++
	c.timestamp += frameSize

	// Encrypt with nonce
	nonceSuffix := make([]byte, 4)
	binary.LittleEndian.PutUint32(nonceSuffix, c.nonce)
	nonceBuf := make([]byte, c.aead.NonceSize())
	binary.LittleEndian.PutUint32(nonceBuf, c.nonce)
	c.nonce++

	ciphertext := c.aead.Seal(nil, nonceBuf, payload, header)
	packet := append(header, ciphertext...)
	packet = append(packet, nonceSuffix...)

	_, err := c.udpConn.Write(packet)
	return err
}

// reconnect attempts to reconnect the voice connection with exponential backoff.
func (c *Connection) reconnect() {
	c.Lock()
	if c.reconnecting {
		c.Unlock()
		slog.Info("already reconnecting, skipping")
		return
	}
	c.reconnecting = true
	c.Unlock()

	defer func() {
		c.Lock()
		c.reconnecting = false
		c.Unlock()
	}()

	slog.Info("attempting voice reconnection")

	// Close existing connections
	c.Close()

	wait := time.Duration(1) * time.Second
	maxWait := time.Duration(600) * time.Second

	for attempt := 1; ; attempt++ {
		time.Sleep(wait)

		c.RLock()
		mainGateway := c.mainGateway
		c.RUnlock()

		if mainGateway == nil {
			slog.Error("cannot reconnect: no main gateway reference")
			return
		}

		slog.Info("reconnection attempt", "attempt", attempt, "wait", wait)

		// Request new voice connection via main gateway
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Send voice state update to re-join
		data, err := json.Marshal(VoiceStateUpdateData{
			ChannelID: c.channelID,
			GuildID:   c.guildID,
			SelfMute:  false,
			SelfDeaf:  false,
		})
		if err != nil {
			cancel()
			slog.Error("failed to marshal voice state update", "error", err)
			continue
		}

		rawData := json.RawMessage(data)
		event := gateway.Event{Opcode: 4, Data: &rawData}
		if err := mainGateway.SendEvent(ctx, event); err != nil {
			cancel()
			slog.Error("failed to send voice state update for reconnection", "error", err)
		} else {
			slog.Info("sent voice state update for reconnection")
		}
		cancel()

		// Exponential backoff
		wait *= 2
		if wait > maxWait {
			wait = maxWait
		}
	}
}

func (c *Connection) readEvent(ctx context.Context) (voiceEvent, error) {
	var event voiceEvent
	if err := wsjson.Read(ctx, c.wsConn, &event); err != nil {
		return voiceEvent{}, err
	}
	if event.Seq != nil {
		c.seqAck.Store(int64(*event.Seq))
	}
	return event, nil
}

func (c *Connection) sendEvent(ctx context.Context, opcode int, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw := json.RawMessage(data)
	event := voiceEvent{Opcode: opcode, Data: raw}

	c.wsMutex.Lock()
	defer c.wsMutex.Unlock()

	return wsjson.Write(ctx, c.wsConn, event)
}

func chooseMode(modes []string) string {
	preferred := []string{"aead_aes256_gcm_rtpsize", "aead_xchacha20_poly1305_rtpsize"}
	for _, candidate := range preferred {
		for _, mode := range modes {
			if mode == candidate {
				return mode
			}
		}
	}
	if len(modes) == 0 {
		return ""
	}
	return modes[0]
}

func (c *Connection) bindCipher() error {
	if len(c.secretKey) == 0 {
		return fmt.Errorf("voice secret key missing")
	}

	switch c.mode {
	case "aead_aes256_gcm_rtpsize":
		block, err := aes.NewCipher(c.secretKey)
		if err != nil {
			return err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		c.aead = aead
	case "aead_xchacha20_poly1305_rtpsize":
		aead, err := chacha20poly1305.NewX(c.secretKey)
		if err != nil {
			return err
		}
		c.aead = aead
	default:
		return fmt.Errorf("unsupported encryption mode: %s", c.mode)
	}

	return nil
}

func parseIPDiscovery(response []byte) (string, int, error) {
	if len(response) < 74 {
		return "", 0, fmt.Errorf("invalid IP discovery response: too short")
	}

	zero := bytes.IndexByte(response[8:len(response)-2], 0)
	ipBytes := response[8 : len(response)-2]
	if zero != -1 {
		ipBytes = response[8 : 8+zero]
	}

	addr, err := netip.ParseAddr(string(ipBytes))
	if err != nil {
		return "", 0, fmt.Errorf("failed to parse IP: %w", err)
	}

	port := int(binary.BigEndian.Uint16(response[len(response)-2:]))
	return addr.String(), port, nil
}

// GetSilenceFrame returns the opus silence frame for ending audio streams.
func GetSilenceFrame() []byte {
	return silenceFrame
}
