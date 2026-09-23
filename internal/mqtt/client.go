package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type client struct {
	conn   net.Conn
	mu     sync.Mutex
	nextID uint16
}

func dial(ctx context.Context, config Config) (*client, error) {
	broker, err := parseBrokerURL(config.BrokerURL)
	if err != nil {
		return nil, err
	}
	host := broker.Hostname()
	port := broker.Port()
	if port == "" {
		if broker.Scheme == "mqtts" || broker.Scheme == "ssl" {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	if broker.Scheme == "mqtts" || broker.Scheme == "ssl" {
		tlsConfig := config.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = host
		}
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, err
		}
		connection = tlsConnection
	}
	result := &client{conn: connection, nextID: 1}
	if err := result.connect(config); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return result, nil
}

func (c *client) connect(config Config) error {
	flags := byte(0x02) // Clean Session.
	if config.Username != "" {
		flags |= 0x80
	}
	if config.Password != "" {
		flags |= 0x40
	}
	// An ungraceful disconnect updates retained availability to offline.
	flags |= 0x04 | 0x08 | 0x20 // Will flag, QoS 1, retain.
	variable := append(encodeString("MQTT"), 0x04, flags, 0x00, 60)
	payload := append(encodeString(config.ClientID), encodeString(config.AvailabilityTopic)...)
	payload = append(payload, encodeString("offline")...)
	if config.Username != "" {
		payload = append(payload, encodeString(config.Username)...)
	}
	if config.Password != "" {
		payload = append(payload, encodeString(config.Password)...)
	}
	if err := c.writePacket(0x10, append(variable, payload...)); err != nil {
		return err
	}
	typeByte, body, err := c.readPacket()
	if err != nil {
		return err
	}
	if typeByte != 0x20 || len(body) != 2 || body[1] != 0 {
		return fmt.Errorf("MQTT connection refused")
	}
	return nil
}

func (c *client) publish(topic string, payload []byte, qos byte, retain bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if qos > 2 {
		return fmt.Errorf("invalid MQTT QoS %d", qos)
	}
	body := encodeString(topic)
	id := uint16(0)
	if qos > 0 {
		id = c.packetID()
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], id)
		body = append(body, encoded[:]...)
	}
	body = append(body, payload...)
	header := byte(0x30 | qos<<1)
	if retain {
		header |= 1
	}
	if err := c.writePacket(header, body); err != nil || qos == 0 {
		return err
	}
	if qos == 1 {
		typeByte, body, err := c.readPacket()
		if err != nil {
			return err
		}
		if typeByte != 0x40 || !matchesPacketID(body, id) {
			return fmt.Errorf("invalid MQTT PUBACK")
		}
		return nil
	}
	typeByte, body, err := c.readPacket()
	if err != nil {
		return err
	}
	if typeByte != 0x50 || !matchesPacketID(body, id) {
		return fmt.Errorf("invalid MQTT PUBREC")
	}
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], id)
	if err := c.writePacket(0x62, encoded[:]); err != nil {
		return err
	}
	typeByte, body, err = c.readPacket()
	if err != nil {
		return err
	}
	if typeByte != 0x70 || !matchesPacketID(body, id) {
		return fmt.Errorf("invalid MQTT PUBCOMP")
	}
	return nil
}

func (c *client) packetID() uint16 {
	id := c.nextID
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	return id
}

func (c *client) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.writePacket(0xE0, nil)
	return c.conn.Close()
}

// abort closes a failed connection without attempting a graceful MQTT write.
func (c *client) abort() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func (c *client) writePacket(header byte, body []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	packet := append([]byte{header}, encodeRemainingLength(len(body))...)
	packet = append(packet, body...)
	_, err := c.conn.Write(packet)
	return err
}

func (c *client) readPacket() (byte, []byte, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return 0, nil, err
	}
	var first [1]byte
	if _, err := io.ReadFull(c.conn, first[:]); err != nil {
		return 0, nil, err
	}
	length, err := decodeRemainingLength(c.conn)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return 0, nil, err
	}
	return first[0], body, nil
}

func encodeString(value string) []byte {
	if len(value) > 65535 {
		panic("MQTT string is too long")
	}
	result := make([]byte, 2+len(value))
	binary.BigEndian.PutUint16(result, uint16(len(value)))
	copy(result[2:], value)
	return result
}

func encodeRemainingLength(length int) []byte {
	result := make([]byte, 0, 4)
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		result = append(result, digit)
		if length == 0 {
			return result
		}
	}
}

func decodeRemainingLength(reader io.Reader) (int, error) {
	multiplier, result := 1, 0
	for count := 0; count < 4; count++ {
		var input [1]byte
		if _, err := io.ReadFull(reader, input[:]); err != nil {
			return 0, err
		}
		result += int(input[0]&127) * multiplier
		if input[0]&128 == 0 {
			return result, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("invalid MQTT remaining length")
}

func matchesPacketID(body []byte, id uint16) bool {
	return len(body) == 2 && binary.BigEndian.Uint16(body) == id
}

// BrokerAddress is retained as a small public helper for CLI diagnostics.
func BrokerAddress(value string) (string, error) {
	parsed, err := parseBrokerURL(value)
	if err != nil {
		return "", err
	}
	port := parsed.Port()
	if port == "" {
		if strings.HasSuffix(parsed.Scheme, "s") || parsed.Scheme == "ssl" {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	return net.JoinHostPort(parsed.Hostname(), port), nil
}

// ValidateClientID exposes the MQTT wire-size limit as an input error.
func ValidateClientID(value string) error {
	if len(value) > 65535 {
		return fmt.Errorf("MQTT client ID is too long: %s bytes", strconv.Itoa(len(value)))
	}
	return nil
}
