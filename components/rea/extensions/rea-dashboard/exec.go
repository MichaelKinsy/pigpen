package readashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// inspectLimit caps what a quick inspect keeps of the CLI's output.
	inspectLimit = 64 << 10
	// doctorLimit caps the doctor report; the real one is about 10 KB.
	doctorLimit   = 512 << 10
	doctorTimeout = 90 * time.Second
	// inspectTimeout bounds a quick inspect.
	inspectTimeout = 3 * time.Minute
)

// runner runs the embedded node with rea.mjs and args, and returns at most limit bytes of stdout. The process
// is stopped when the output passes the limit; truncated says so.
type runner func(ctx context.Context, srv reaServer, args []string, limit int) (out []byte, truncated bool, err error)

// capWriter keeps the first limit bytes and calls stop when more arrive.
type capWriter struct {
	buf       bytes.Buffer
	limit     int
	stop      func()
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); len(p) > room {
		w.buf.Write(p[:max(room, 0)])
		if !w.truncated {
			w.truncated = true
			w.stop()
		}
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// execREA is the runner the dashboard uses. It is the stateless CLI one-shot of the faithfulness boundary:
// `<node> <rea.mjs> <args…>`, no shell, the environment of this process unchanged (cmd.Env stays nil, so the
// registered server and the one-shot see the same environment), no second MCP session.
func execREA(ctx context.Context, srv reaServer, args []string, limit int) ([]byte, bool, error) {
	if !srv.runnable() {
		return nil, false, errors.New("REA is not registered")
	}
	timeout := doctorTimeout
	if len(args) > 0 && args[0] != "doctor" {
		timeout = inspectTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &capWriter{limit: limit, stop: cancel}
	errs := &capWriter{limit: 4 << 10, stop: func() {}}
	cmd := exec.CommandContext(cctx, srv.Node, append([]string{srv.Entry}, args...)...)
	cmd.Stdout, cmd.Stderr = out, errs
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if out.truncated {
		return out.buf.Bytes(), true, nil
	}
	if err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return out.buf.Bytes(), false, fmt.Errorf("timed out after %s", timeout)
		}
		if msg := strings.TrimSpace(errs.buf.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, short(msg, 200))
		}
		return out.buf.Bytes(), false, err
	}
	return out.buf.Bytes(), false, nil
}

// runDoctor runs `doctor --json` and parses it. The report is used even when the exit status is not zero:
// an unhealthy environment is a normal doctor answer.
func runDoctor(ctx context.Context, run runner, srv reaServer) (*doctorReport, error) {
	out, truncated, err := run(ctx, srv, []string{"doctor", "--json"}, doctorLimit)
	if truncated {
		return nil, errors.New("doctor output is larger than expected")
	}
	if report, perr := parseDoctor(out); perr == nil {
		return report, nil
	} else if err == nil {
		err = perr
	}
	return nil, err
}

// inspectResult is a quick inspect's output, summarised for the overlay.
type inspectResult struct {
	OK        bool
	Summary   []string // evidence and limitations first
	Raw       []string // the output, indented when it is JSON
	Bytes     int
	Truncated bool
}

// runInspect runs `inspect-artifact --json <path>`; path must be a local absolute path (never an option).
func runInspect(ctx context.Context, run runner, srv reaServer, path string) inspectResult {
	if !localPath(path) {
		return inspectResult{Summary: []string{"quick inspect needs a local absolute path: " + path}}
	}
	out, truncated, err := run(ctx, srv, []string{"inspect-artifact", "--json", path}, inspectLimit)
	res := formatInspect(out, truncated)
	if err != nil {
		res.OK = false
		res.Summary = append([]string{"failed: " + short(err.Error(), 200)}, res.Summary...)
	}
	return res
}

// formatInspect summarises an Evidence bundle (evidence_id, subject, limitations …) or an error object.
func formatInspect(out []byte, truncated bool) inspectResult {
	res := inspectResult{Bytes: len(out), Truncated: truncated}
	var doc map[string]any
	valid := !truncated && json.Unmarshal(out, &doc) == nil && doc != nil
	text := string(out)
	if valid {
		var indented bytes.Buffer
		if json.Indent(&indented, out, "", "  ") == nil {
			text = indented.String()
		}
	}
	res.Raw = strings.Split(strings.TrimRight(text, "\n"), "\n")
	switch {
	case truncated:
		res.Summary = []string{fmt.Sprintf("output cut at %d KB: not shown whole, use the agent for the full analysis", inspectLimit>>10)}
	case !valid:
		if len(out) == 0 {
			res.Summary = []string{"no output"}
		} else {
			res.Summary = []string{"output is not JSON"}
		}
	case doc["error"] != nil:
		res.Summary = append(res.Summary, "error: "+errorMessage(doc["error"])+orEmpty(" · ", firstString(doc, "code", "message")))
		if m := firstString(doc, "message"); m != "" {
			res.Summary = append(res.Summary, m)
		}
	default:
		res.OK = true
		sum := summarizeResult(map[string]any{"structuredContent": doc})
		res.Summary = append(res.Summary, fmt.Sprintf("evidence %s · %d limitation(s)", orElse(firstString(doc, "evidence_id"), "none"), sum.Limitations))
		for _, l := range sum.LimitationSample {
			res.Summary = append(res.Summary, "limitation: "+l)
		}
		if subject, ok := doc["subject"].(map[string]any); ok {
			res.Summary = append(res.Summary, "subject: "+firstString(subject, "name", "local_path")+orEmpty(" · ", firstString(subject, "format")))
		}
		if provider, ok := doc["provider"].(map[string]any); ok {
			res.Summary = append(res.Summary, "provider: "+firstString(provider, "name", "id"))
		}
		if v := firstString(doc, "confidence"); v != "" {
			res.Summary = append(res.Summary, "confidence: "+v)
		}
	}
	return res
}

func orEmpty(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
