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
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/neiios/discord-music-bot/internal/gateway"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	payloadTypeOpus = 0x78
)

var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

type voiceEvent struct {
	Opcode int             `json:"op"`
	Data   json.RawMessage `json:"d"`
	Seq    *int            `json:"s,omitempty"`
	Type   *string         `json:"t,omitempty"`
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

type Connection struct {
	conn            *websocket.Conn
	udpConn         *net.UDPConn
	udpAddr         *net.UDPAddr
	mode            string
	secretKey       []byte
	aead            cipher.AEAD
	ssrc            uint32
	sequence        uint16
	timestamp       uint32
	nonce           uint32
	sendMu          sync.Mutex
	heartbeatCancel context.CancelFunc
}

func Connect(ctx context.Context, userID string, state gateway.VoiceState, server gateway.VoiceServerUpdate) (*Connection, error) {
	endpoint := server.Endpoint
	if !strings.HasPrefix(endpoint, "wss://") {
		endpoint = "wss://" + endpoint
	}
	if !strings.Contains(endpoint, "?") {
		endpoint = endpoint + "?v=8"
	} else if !strings.Contains(endpoint, "v=8") {
		endpoint = endpoint + "&v=8"
	}

	wsConn, res, err := websocket.Dial(ctx, endpoint, nil)
	slog.Info("connected to voice gateway", "endpoint", endpoint, "res", res)
	if err != nil {
		return nil, err
	}

	c := &Connection{conn: wsConn}
	if err := c.establish(ctx, userID, state, server); err != nil {
		c.Close()
		return nil, err
	}

	go c.listen(ctx)

	return c, nil
}

func (c *Connection) Close() {
	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
	}
	if c.conn != nil {
		c.conn.Close(websocket.StatusNormalClosure, "closing")
	}
	if c.udpConn != nil {
		c.udpConn.Close()
	}
}

func (c *Connection) establish(ctx context.Context, userID string, state gateway.VoiceState, server gateway.VoiceServerUpdate) error {
	helloEvent, err := c.readEvent(ctx)
	if err != nil {
		return err
	}
	if helloEvent.Opcode != 8 {
		return fmt.Errorf("expected hello event, got opcode %d", helloEvent.Opcode)
	}

	var hello voiceHello
	if err := json.Unmarshal(helloEvent.Data, &hello); err != nil {
		return err
	}

	identify := voiceIdentify{
		ServerID:  state.GuildID,
		UserID:    userID,
		SessionID: state.SessionID,
		Token:     server.Token,
		Video:     false,
	}
	if err := c.sendEvent(ctx, 0, identify); err != nil {
		return err
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	c.heartbeatCancel = cancel
	c.startHeartbeat(heartbeatCtx, time.Duration(hello.HeartbeatInterval)*time.Millisecond)

	var ready voiceReady
	readyReceived := false
	modeSelected := ""
	for {
		event, err := c.readEvent(ctx)
		if err != nil {
			return err
		}

		switch event.Opcode {
		case 2:
			if err := json.Unmarshal(event.Data, &ready); err != nil {
				return err
			}
			readyReceived = true
			slog.Info("voice ready", "ssrc", ready.SSRC, "ip", ready.IP, "port", ready.Port)
			modeSelected = chooseMode(ready.Modes)
			if modeSelected == "" {
				return fmt.Errorf("no supported encryption modes")
			}

			if err := c.setupUDP(ctx, ready, modeSelected); err != nil {
				return err
			}
		case 4:
			var desc sessionDescription
			if err := json.Unmarshal(event.Data, &desc); err != nil {
				return err
			}
			c.mode = desc.Mode
			c.secretKey = desc.SecretKey
			if err := c.bindCipher(); err != nil {
				return err
			}
			return nil
		case 6:
			slog.Debug("voice heartbeat ack")
		case 9:
			slog.Info("voice resumed")
		default:
			slog.Info("voice gateway event", "opcode", event.Opcode)
		}

		if readyReceived && c.udpConn == nil {
			return fmt.Errorf("voice ready received without UDP setup")
		}
	}
}

func (c *Connection) startHeartbeat(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		nonce := rand.Int63()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				nonce++
				if err := c.sendEvent(ctx, 3, nonce); err != nil {
					slog.Error("voice heartbeat failed", "error", err)
					return
				}
			}
		}
	}()
}

func (c *Connection) setupUDP(ctx context.Context, ready voiceReady, mode string) error {
	address := fmt.Sprintf("%s:%d", ready.IP, ready.Port)
	remoteAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}

	udpConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		return err
	}

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
			return err
		}

		if _, err := udpConn.Write(discovery); err != nil {
			udpConn.Close()
			return err
		}

		n, err := udpConn.Read(response)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				slog.Warn("udp discovery timed out, retrying", "attempt", attempt+1)
				continue
			}
			udpConn.Close()
			return err
		}

		parsedIP, parsedPort, err := parseIPDiscovery(response[:n])
		if err != nil {
			udpConn.Close()
			return err
		}

		ip = parsedIP
		port = parsedPort
		break
	}

	if ip == "" {
		udpConn.Close()
		return fmt.Errorf("udp discovery failed after retries")
	}

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
		return err
	}

	if err := udpConn.SetDeadline(time.Time{}); err != nil {
		udpConn.Close()
		return err
	}

	c.udpConn = udpConn
	c.udpAddr = remoteAddr
	c.ssrc = ready.SSRC

	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	c.sequence = uint16(seed.Uint32())
	c.timestamp = seed.Uint32()
	c.nonce = seed.Uint32()

	return nil
}

func (c *Connection) SendOpusPackets(ctx context.Context, packets [][]byte) error {
	if c.aead == nil {
		return fmt.Errorf("voice connection not ready")
	}

	if err := c.sendSpeaking(ctx, true); err != nil {
		return err
	}
	defer c.sendSpeaking(context.Background(), false)

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for i, packet := range packets {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}

		if err := c.sendRTPPacket(packet); err != nil {
			return err
		}
	}

	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		if err := c.sendRTPPacket(silenceFrame); err != nil {
			return err
		}
	}

	return nil
}

func (c *Connection) sendRTPPacket(payload []byte) error {
	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = payloadTypeOpus
	binary.BigEndian.PutUint16(header[2:], c.sequence)
	binary.BigEndian.PutUint32(header[4:], c.timestamp)
	binary.BigEndian.PutUint32(header[8:], c.ssrc)

	c.sequence++
	c.timestamp += 960

	nonceSuffix := make([]byte, 4)
	binary.BigEndian.PutUint32(nonceSuffix, c.nonce)
	nonceBuf := make([]byte, c.aead.NonceSize())
	binary.BigEndian.PutUint32(nonceBuf, c.nonce)
	c.nonce++

	ciphertext := c.aead.Seal(nil, nonceBuf, payload, header)
	packet := append(header, ciphertext...)
	packet = append(packet, nonceSuffix...)

	_, err := c.udpConn.Write(packet)
	return err
}

func (c *Connection) sendSpeaking(ctx context.Context, enabled bool) error {
	speaking := 0
	if enabled {
		speaking = 1
	}

	return c.sendEvent(ctx, 5, speakingPayload{Speaking: speaking, Delay: 0, SSRC: c.ssrc})
}

func (c *Connection) readEvent(ctx context.Context) (voiceEvent, error) {
	var event voiceEvent
	if err := wsjson.Read(ctx, c.conn, &event); err != nil {
		return voiceEvent{}, err
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

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	return wsjson.Write(ctx, c.conn, event)
}

func (c *Connection) listen(ctx context.Context) {
	for {
		event, err := c.readEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				return
			}
			slog.Error("voice gateway read failed", "error", err)
			return
		}

		switch event.Opcode {
		case 6:
			slog.Debug("voice heartbeat ack")
		case 9:
			slog.Info("voice resumed")
		case 13:
			slog.Info("voice client disconnect")
		default:
			slog.Info("voice gateway event", "opcode", event.Opcode)
		}
	}
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
		cipher, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		c.aead = cipher
	case "aead_xchacha20_poly1305_rtpsize":
		cipher, err := chacha20poly1305.NewX(c.secretKey)
		if err != nil {
			return err
		}
		c.aead = cipher
	default:
		return fmt.Errorf("unsupported encryption mode: %s", c.mode)
	}

	return nil
}

func parseIPDiscovery(response []byte) (string, int, error) {
	if len(response) < 74 {
		return "", 0, fmt.Errorf("invalid ip discovery response")
	}

	zero := bytes.IndexByte(response[8:len(response)-2], 0)
	ipBytes := response[8 : len(response)-2]
	if zero != -1 {
		ipBytes = response[8 : 8+zero]
	}

	addr, err := netip.ParseAddr(string(ipBytes))
	if err != nil {
		return "", 0, err
	}

	port := int(binary.BigEndian.Uint16(response[len(response)-2:]))
	return addr.String(), port, nil
}
