package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client makes calls on one connection, one at a time.
type Client struct {
	path string
	mu   sync.Mutex
	conn net.Conn // nil after an I/O error; the next call dials again
	sc   *bufio.Scanner
	next int64
}

// Dial connects to the daemon at path; ErrNotRunning when none listens.
func Dial(ctx context.Context, path string) (*Client, error) {
	c := &Client{path: path}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) connect(ctx context.Context) error {
	conn, err := dial(ctx, c.path)
	if err != nil {
		return err
	}
	c.conn = conn
	c.sc = bufio.NewScanner(conn)
	c.sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return nil
}

// drop closes a connection that failed mid-call, so its reply can't be
// read as the next call's.
func (c *Client) drop() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Call sends method with params and decodes the result into result (nil
// ignores it). A failed call returns its *Error.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		if err := c.connect(ctx); err != nil {
			return err
		}
	}
	conn := c.conn
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Time{}
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	c.next++
	req := Request{V: Version, ID: c.next, Method: method}
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode %s: %w", method, err)
		}
		req.Params = data
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		c.drop()
		return c.ioErr(ctx, err)
	}
	if !c.sc.Scan() {
		err := c.sc.Err()
		if err == nil {
			err = fmt.Errorf("the daemon closed the connection")
		}
		c.drop()
		return c.ioErr(ctx, err)
	}
	var resp Response
	if err := json.Unmarshal(c.sc.Bytes(), &resp); err != nil {
		c.drop()
		return fmt.Errorf("read the daemon's reply to %s: %w", method, err)
	}
	if resp.ID != req.ID && resp.Error == nil {
		c.drop()
		return fmt.Errorf("the daemon answered call %d with a reply to %d; check it with `pfwd daemon status`", req.ID, resp.ID)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("decode the daemon's reply to %s: %w", method, err)
		}
	}
	return nil
}

// ioErr prefers ctx's error when ctx ended the call.
func (c *Client) ioErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("talk to the pfwd daemon: %w; check it with `pfwd daemon status`", err)
}

// Watch opens its own connection to the daemon at path and calls fn for
// each event until ctx ends (nil) or the daemon goes (an error).
func Watch(ctx context.Context, path string, fn func(Event)) error {
	c, err := Dial(ctx, path)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Call(ctx, "watch", nil, nil); err != nil {
		return err
	}
	conn := c.conn
	_ = conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	for c.sc.Scan() {
		var ev Event
		if err := json.Unmarshal(c.sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("read an event from the daemon: %w", err)
		}
		fn(ev)
	}
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("the pfwd daemon stopped")
}
