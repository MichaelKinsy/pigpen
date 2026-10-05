package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The extension links neither WaxTap nor WaxFlow nor oto. What needs them besides playing (listing a public playlist and the
// doctor's stream probe) runs in the pigmusic program as a one-shot helper: `pigmusic native playlist <id>` and
// `pigmusic native probe <video id>` print one JSON document on stdout and exit. wax.Helper is the other end.

// HelperTimeout bounds one helper run.
const HelperTimeout = 75 * time.Second

// maxHelperOutput bounds what a helper may print: 500 playlist entries fit many times over.
const maxHelperOutput = 4 << 20

// HelperVersion changes when a helper's arguments or reply stop being compatible. The pigmusic that answers is found on its
// own (PIG_MUSIC_SERVE, nativePath, PATH) and built separately, so it may be older or newer than this code.
const HelperVersion = 1

// helperReply is the document a helper prints: the result, or an error message fit to show a person.
type helperReply struct {
	Version int             `json:"version"`
	Error   string          `json:"error,omitempty"`
	Entries []PlaylistEntry `json:"entries,omitempty"`
	Check   *Check          `json:"check,omitempty"`
}

// Helper runs `<pigmusic> native <args...>` and returns what it printed.
type Helper struct {
	// Binary is the pigmusic program; empty finds it (FindServeBinary with Configured).
	Binary     string
	Configured string
	// Timeout bounds one run; zero means HelperTimeout.
	Timeout  time.Duration
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

func (h Helper) run(ctx context.Context, args ...string) (helperReply, error) {
	bin := h.Binary
	if bin == "" {
		var err error
		if bin, err = FindServeBinary(h.Configured, h.Getenv, h.LookPath); err != nil {
			return helperReply{}, err
		}
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = HelperTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"native"}, args...)...)
	cmd.Env = os.Environ()
	var out, errOut bytes.Buffer
	stdout := &limitWriter{w: &out, n: maxHelperOutput, stop: true}
	cmd.Stdout = stdout
	cmd.Stderr = &limitWriter{w: &errOut, n: 64 << 10}
	cmd.WaitDelay = time.Second // a grandchild holding the pipes open cannot keep this call waiting
	err := cmd.Run()
	switch {
	case stdout.over:
		return helperReply{}, fmt.Errorf("the pigmusic helper (%s) printed more than %d MiB and was stopped", bin, maxHelperOutput>>20)
	case err != nil && ctx.Err() != nil:
		return helperReply{}, fmt.Errorf("the pigmusic helper did not answer in time: %w", ctx.Err())
	case err != nil && strings.Contains(errOut.String(), `unknown command "native"`):
		return helperReply{}, fmt.Errorf("the pigmusic program %s is older than this pig-music (it has no `native` command): %s", bin, BuildPigmusicHint)
	}
	reply, ok := parseHelper(out.Bytes())
	switch {
	case ok && reply.Version != HelperVersion:
		return helperReply{}, fmt.Errorf("the pigmusic program %s answers helper version %d, this pig-music expects %d; build pigmusic from the same pig-music: %s", bin, reply.Version, HelperVersion, BuildPigmusicHint)
	case ok && reply.Error != "":
		return helperReply{}, errors.New(reply.Error)
	case err != nil:
		return helperReply{}, fmt.Errorf("the pigmusic helper failed: %v", RedactError(err))
	case !ok:
		return helperReply{}, errors.New("the pigmusic helper printed something this version cannot read; update pigmusic and pig-music together")
	}
	return reply, nil
}

func parseHelper(b []byte) (helperReply, bool) {
	var r helperReply
	if err := json.Unmarshal(bytes.TrimSpace(b), &r); err != nil {
		return helperReply{}, false
	}
	return r, true
}

// limitWriter keeps the first n bytes and drops the rest, so a runaway helper cannot fill memory. With stop, going over
// the limit is an error: the pipe closes and the helper is stopped by its next write instead of running to the timeout.
type limitWriter struct {
	w    io.Writer
	n    int
	stop bool
	over bool
}

var errHelperOutput = errors.New("helper output over the limit")

func (l *limitWriter) Write(p []byte) (int, error) {
	k := min(len(p), max(l.n, 0))
	if k > 0 {
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	if k < len(p) && l.stop {
		l.over = true
		return k, errHelperOutput
	}
	return len(p), nil
}

// Playlist implements PlaylistLister.
func (h Helper) Playlist(ctx context.Context, id string) ([]PlaylistEntry, error) {
	r, err := h.run(ctx, "playlist", id)
	if err != nil {
		return nil, err
	}
	return r.Entries, nil
}

// Probe is the doctor's stream probe: the helper resolves one video and reads 64 KiB of it.
func (h Helper) Probe(ctx context.Context, videoID string) Check {
	c := Check{Name: "stream probe"}
	r, err := h.run(ctx, "probe", videoID)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	if r.Check == nil {
		c.Detail = "the pigmusic helper returned no result"
		return c
	}
	return *r.Check
}

// WriteHelperReply prints a helper's document: the end of the protocol the wax package serves.
func WriteHelperReply(w io.Writer, entries []PlaylistEntry, check *Check, err error) {
	r := helperReply{Version: HelperVersion, Entries: entries, Check: check}
	if err != nil {
		r.Error = RedactError(err).Error()
	}
	_ = json.NewEncoder(w).Encode(r)
}
