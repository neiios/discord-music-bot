package voice

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"net"
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
