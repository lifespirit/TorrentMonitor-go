package browserbroker

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
}

func dialWebSocket(rawURL string, timeout time.Duration) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported CDP websocket scheme %q", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", host)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, u.Host, key)
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	r := bufio.NewReader(conn)
	status, err := r.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.Contains(status, " 101 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", strings.TrimSpace(status))
	}
	var accept string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(v)
		}
	}
	if accept != "" && accept != websocketAccept(key) {
		_ = conn.Close()
		return nil, errors.New("websocket accept key mismatch")
	}
	return &wsConn{conn: conn, r: r}, nil
}

func websocketAccept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

func (w *wsConn) Close() error { return w.conn.Close() }

func (w *wsConn) writeText(payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(payload) > 0x7fffffff {
		return errors.New("websocket payload too large")
	}
	header := []byte{0x81, 0x80}
	if len(payload) < 126 {
		header[1] |= byte(len(payload))
	} else if len(payload) <= 0xffff {
		header[1] |= 126
		ext := make([]byte, 2)
		binary.BigEndian.PutUint16(ext, uint16(len(payload)))
		header = append(header, ext...)
	} else {
		header[1] |= 127
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(len(payload)))
		header = append(header, ext...)
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	header = append(header, mask...)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := w.conn.Write(header); err != nil {
		return err
	}
	_, err := w.conn.Write(masked)
	return err
}

func (w *wsConn) writePong(payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	header := []byte{0x8a, 0x80 | byte(len(payload))}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	_, err := w.conn.Write(append(append(header, mask...), masked...))
	return err
}

func (w *wsConn) readMessage() ([]byte, error) {
	for {
		b1, err := w.r.ReadByte()
		if err != nil {
			return nil, err
		}
		b2, err := w.r.ReadByte()
		if err != nil {
			return nil, err
		}
		opcode := b1 & 0x0f
		masked := b2&0x80 != 0
		ln := uint64(b2 & 0x7f)
		if ln == 126 {
			buf := make([]byte, 2)
			if _, err := io.ReadFull(w.r, buf); err != nil {
				return nil, err
			}
			ln = uint64(binary.BigEndian.Uint16(buf))
		} else if ln == 127 {
			buf := make([]byte, 8)
			if _, err := io.ReadFull(w.r, buf); err != nil {
				return nil, err
			}
			ln = binary.BigEndian.Uint64(buf)
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return nil, err
			}
		}
		if ln > 128*1024*1024 {
			return nil, errors.New("websocket message too large")
		}
		payload := make([]byte, ln)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x1, 0x2:
			return payload, nil
		case 0x8:
			return nil, io.EOF
		case 0x9:
			_ = w.writePong(payload)
		case 0xA:
			continue
		default:
			continue
		}
	}
}
