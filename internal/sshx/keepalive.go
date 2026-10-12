package sshx

import (
	"context"
	"fmt"
	"time"
)

// Keepalive configures keepalive@openssh.com requests, like OpenSSH's
// ServerAliveInterval and ServerAliveCountMax. A zero Interval turns them
// off.
type Keepalive struct {
	// Interval is the time between requests.
	Interval time.Duration
	// CountMax is how many requests in a row may go unanswered before the
	// connection counts as dead; less than 1 means 1.
	CountMax int
}

// KeepaliveOr returns the ssh config's ServerAlive settings when it turns
// keepalives on (ServerAliveInterval above 0), and def otherwise.
// ServerAliveCountMax alone doesn't count: ssh -G always prints it, 3 by
// default.
func (hc *HostConfig) KeepaliveOr(def Keepalive) Keepalive {
	if hc.ServerAliveInterval <= 0 {
		return def
	}
	k := Keepalive{Interval: hc.ServerAliveInterval, CountMax: def.CountMax}
	if hc.ServerAliveCountMax > 0 {
		k.CountMax = hc.ServerAliveCountMax
	}
	return k
}

// keepalive sends one request at a time, every k.Interval, until ctx ends.
// A request still unanswered at a tick is a miss; after k.CountMax misses
// in a row, or a request that fails to send, it closes the client so Wait
// returns. Close stops it and waits for it.
func (c *Conn) keepalive(ctx context.Context, k Keepalive) {
	defer c.kaWG.Done()
	countMax := max(1, k.CountMax)
	t := time.NewTicker(k.Interval)
	defer t.Stop()
	var pending chan error
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-pending:
			pending = nil
			if err != nil {
				c.die(fmt.Errorf("%s: a keepalive request failed: %w", c.alias, err))
				return
			}
			misses = 0
		case <-t.C:
			if pending != nil {
				misses++
				if misses >= countMax {
					c.die(fmt.Errorf("%s stopped answering: %d keepalive requests, %s apart, got no reply", c.alias, misses, k.Interval))
					return
				}
				continue
			}
			pending = make(chan error, 1)
			c.kaWG.Add(1)
			go func(done chan<- error) {
				defer c.kaWG.Done()
				_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
				done <- err
			}(pending)
		}
	}
}

// die records why the connection is being dropped, for Wait, and closes it.
func (c *Conn) die(reason error) {
	c.reason.CompareAndSwap(nil, &reason)
	c.log.Info("connection dropped", "host", c.alias, "reason", reason)
	_ = c.client.Close()
}
