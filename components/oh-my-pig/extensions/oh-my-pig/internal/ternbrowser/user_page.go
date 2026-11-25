package ternbrowser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// PagePlacement is where OpenUserPage docks a page relative to the current pane.
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:16
type PagePlacement string

const (
	PlaceBeside PagePlacement = "beside"
	PlaceBelow  PagePlacement = "below"
	PlaceOver   PagePlacement = "over"
)

// UserPage is a browser block opened in Tern and handed to the user.
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:25
type UserPage struct {
	Block      int
	socketPath string

	mu     sync.Mutex
	client *ternwire.Client
}

// OpenUserPage opens url in a Tern browser block docked at how and releases it
// to the user. Returns (nil, nil) outside a Tern pane or when Tern cannot open it.
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:91
func OpenUserPage(ctx context.Context, url string, how PagePlacement) (*UserPage, error) {
	socket, pane, ok := ternwire.Pane()
	if !ok {
		return nil, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	client, err := ternwire.Dial(dialCtx, socket)
	if err != nil {
		return nil, nil
	}

	var opened struct {
		Block *int `json:"block"`
	}
	err = client.RequestWithOptions(dialCtx, "browser", opOpenReq{
		Op:    "open",
		Owner: pane,
		URL:   url,
		How:   string(how),
	}, &opened, ternwire.RequestOptions{Timeout: 15 * time.Second})
	if err != nil || opened.Block == nil {
		_ = client.Close()
		return nil, nil
	}

	relCtx, relCancel := context.WithTimeout(ctx, 5*time.Second)
	_ = client.RequestWithOptions(relCtx, "browser", opBlockOnlyReq{
		Op:    "release",
		Block: *opened.Block,
	}, nil, ternwire.RequestOptions{Timeout: 5 * time.Second})
	relCancel()

	return &UserPage{
		Block:      *opened.Block,
		socketPath: socket,
		client:     client,
	}, nil
}

// request sends one op, replacing the client if the connection dropped (B6).
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:71-82
func (p *UserPage) request(ctx context.Context, req any, out any) error {
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()

	if c == nil || !c.Connected() {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		nc, err := ternwire.Dial(dialCtx, p.socketPath)
		cancel()
		if err != nil {
			return err
		}
		p.mu.Lock()
		if p.client != nil {
			_ = p.client.Close()
		}
		p.client = nc
		c = nc
		p.mu.Unlock()
	}

	err := c.RequestWithOptions(ctx, "browser", req, out, ternwire.RequestOptions{Timeout: 5 * time.Second})
	var te *ternwire.Error
	if errors.As(err, &te) && (te.Kind == ternwire.ErrClosed || te.Kind == ternwire.ErrConnect) {
		p.mu.Lock()
		if p.client == c {
			_ = c.Close()
			p.client = nil
		}
		p.mu.Unlock()
	}
	return err
}

// Close closes the block and drops the connection.
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:53
func (p *UserPage) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	defer p.Disconnect()
	err := p.request(ctx, opBlockOnlyReq{Op: "close", Block: p.Block}, nil)
	var te *ternwire.Error
	if errors.As(err, &te) && te.Kind == ternwire.ErrNotFound {
		return nil
	}
	return err
}

// Disconnect drops the connection to Tern while leaving the block open.
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:66
func (p *UserPage) Disconnect() {
	if p == nil {
		return
	}
	p.mu.Lock()
	c := p.client
	p.client = nil
	p.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Closed polls Tern until the user closes the block or ctx is cancelled,
// reconnecting if the daemon connection drops (B6).
// upstream: packages/coding-agent/src/tools/browser/tern/user-page.ts:41
func (p *UserPage) Closed(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("nil UserPage")
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := p.request(ctx, opBlockOnlyReq{Op: "state", Block: p.Block}, nil)
			var te *ternwire.Error
			if errors.As(err, &te) && te.Kind == ternwire.ErrNotFound {
				return nil
			}
		}
	}
}
