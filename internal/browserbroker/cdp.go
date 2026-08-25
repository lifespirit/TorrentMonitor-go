package browserbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type cdpConn struct {
	ws      *wsConn
	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan cdpResponse
	events  chan cdpEvent
	closed  chan struct{}
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type cdpEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func newCDP(ctx context.Context, wsURL string) (*cdpConn, error) {
	ws, err := dialWebSocket(wsURL, 10*time.Second)
	if err != nil {
		return nil, err
	}
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpResponse{}, events: make(chan cdpEvent, 64), closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdpConn) Close() error { return c.ws.Close() }

func (c *cdpConn) Events() <-chan cdpEvent { return c.events }

func (c *cdpConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	ch := make(chan cdpResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.ws.writeText(payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		return nil, errors.New("cdp connection closed")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *cdpConn) readLoop() {
	defer close(c.closed)
	defer close(c.events)
	for {
		payload, err := c.ws.readMessage()
		if err != nil {
			return
		}
		var base struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(payload, &base); err != nil {
			continue
		}
		if base.ID != 0 {
			var resp cdpResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[resp.ID]
			delete(c.pending, resp.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
			continue
		}
		if base.Method != "" {
			var ev cdpEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				continue
			}
			select {
			case c.events <- ev:
			default:
			}
		}
	}
}
