package pi_subagents

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// hcConn is a herdr-core JSON-RPC connection: one request line out, one response line in.
type hcConn struct {
	conn net.Conn
	r    *bufio.Reader
	id   int
}

type hcError struct{ Kind, Message string }

func (e *hcError) Error() string { return fmt.Sprintf("herdr-core: %s (%s)", e.Message, e.Kind) }

type hcTerminal struct {
	Address string `json:"address"`
	State   string `json:"state"`
	Status  *struct {
		State string `json:"state"`
	} `json:"status"`
	Turn     uint64            `json:"turn"`
	ExitCode *int              `json:"exit_code"`
	Labels   map[string]string `json:"labels"`
}

func dialHC(socket string) (*hcConn, error) {
	c, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return &hcConn{conn: c, r: bufio.NewReader(c)}, nil
}

func (h *hcConn) Close() error { return h.conn.Close() }

// call sends one request and decodes the result into result (which may be nil). Not safe for concurrent use.
func (h *hcConn) call(method string, params, result any) error {
	h.id++
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": h.id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := h.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := h.conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := h.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return &hcError{Kind: resp.Error.Data.Kind, Message: resp.Error.Message}
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, result)
}
