package bambulab

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

type CameraConfig struct {
	Address      string // A1/P1 use host:6000
	AccessCode   string
	TLSConfig    *tls.Config
	Timeout      time.Duration // connect and per-frame timeout; defaults to 30 seconds
	MaxFrameSize uint32        // defaults to 16 MiB
}
type Camera struct {
	conn         net.Conn
	timeout      time.Duration
	maxFrameSize uint32
	mu           sync.Mutex
}

func cameraAuth(code string) ([]byte, error) {
	code = strings.TrimSpace(code)
	if len(code) == 0 || len(code) > 32 {
		return nil, fmt.Errorf("bambulab: camera access code must be 1–32 ASCII bytes")
	}
	for _, b := range []byte(code) {
		if b < 0x20 || b > 0x7e {
			return nil, fmt.Errorf("bambulab: camera access code must be printable ASCII")
		}
	}
	packet := make([]byte, 80)
	binary.LittleEndian.PutUint32(packet[0:4], 0x40)
	binary.LittleEndian.PutUint32(packet[4:8], 0x3000)
	copy(packet[16:48], "bblp")
	copy(packet[48:80], code)
	return packet, nil
}
func DialCamera(ctx context.Context, config CameraConfig) (*Camera, error) {
	packet, err := cameraAuth(config.AccessCode)
	if err != nil {
		return nil, err
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return nil, err
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < 0 {
		return nil, fmt.Errorf("bambulab: timeout must be positive")
	}
	if config.MaxFrameSize == 0 {
		config.MaxFrameSize = 16 << 20
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	conn, err := (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", config.Address)
	if err != nil {
		return nil, err
	}
	cleanup := connectionContext(ctx, conn)
	_, err = io.Copy(conn, strings.NewReader(string(packet)))
	cleanup()
	if err != nil {
		conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return &Camera{conn: conn, timeout: config.Timeout, maxFrameSize: config.MaxFrameSize}, nil
}

// ReadFrame reads a complete JPEG. Any framing/I/O error closes the stream,
// because a partially consumed frame cannot be resumed safely.
func (c *Camera) ReadFrame(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleanup := connectionContext(ctx, c.conn)
	defer cleanup()
	frame, err := readJPEGFrame(c.conn, c.maxFrameSize)
	if err != nil {
		c.conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return frame, err
}
func readJPEGFrame(reader io.Reader, limit uint32) ([]byte, error) {
	var header [16]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:4])
	if size < 4 || size > limit {
		return nil, fmt.Errorf("bambulab: invalid camera frame size %d", size)
	}
	frame := make([]byte, int(size))
	if _, err := io.ReadFull(reader, frame); err != nil {
		return nil, err
	}
	if frame[0] != 0xff || frame[1] != 0xd8 || frame[len(frame)-2] != 0xff || frame[len(frame)-1] != 0xd9 {
		return nil, fmt.Errorf("bambulab: camera frame is not JPEG")
	}
	return frame, nil
}
func (c *Camera) Close() error { return c.conn.Close() }

// RTSPURL returns an X1 stream URL for an external RTSPS-capable player. It
// contains the access code; do not log it. This SDK does not decode RTSP video.
func RTSPURL(host, accessCode string) (string, error) {
	if host == "" || strings.ContainsAny(host, "/@?#") || strings.TrimSpace(accessCode) == "" {
		return "", fmt.Errorf("bambulab: printer host and access code are required")
	}
	u := url.URL{Scheme: "rtsps", Host: net.JoinHostPort(host, "322"), Path: "/streaming/live/1", User: url.UserPassword("bblp", strings.TrimSpace(accessCode))}
	return u.String(), nil
}
