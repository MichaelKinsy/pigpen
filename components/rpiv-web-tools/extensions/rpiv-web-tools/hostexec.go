package rpiv_web_tools

import (
	"context"
	"errors"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The original starts gh and git with node:child_process.execFile and treats every failure alike: a
// command that cannot start, a non-zero exit, a timeout and an output past maxBuffer all reach the callback
// as err. Here the commands run through the host (Context.Exec, which the request's cancellation aborts) and
// the same four outcomes are one error. The runner travels in the tool's context, so a test installs a canned
// one and nothing is global.

// execOptions are the options of one command: timeout (0: none) and the largest stdout accepted (0: Node's
// default for execFile, 1 MiB).
type execOptions struct {
	Timeout   time.Duration
	MaxBuffer int
}

const defaultMaxBuffer = 1024 * 1024

// execFunc runs name with args (no shell) and returns its stdout; any failure is the error.
type execFunc func(ctx context.Context, name string, args []string, o execOptions) (string, error)

type execCtxKey struct{}

func withExec(ctx context.Context, fn execFunc) context.Context {
	return context.WithValue(ctx, execCtxKey{}, fn)
}

var errNoExec = errors.New("no host command runner")

func runExec(ctx context.Context, name string, args []string, o execOptions) (string, error) {
	fn, _ := ctx.Value(execCtxKey{}).(execFunc)
	if fn == nil {
		return "", errNoExec
	}
	return fn(ctx, name, args, o)
}

// hostExec is the runner over the SDK context of a tool call.
func hostExec(c sdk.Context) execFunc {
	return func(_ context.Context, name string, args []string, o execOptions) (string, error) {
		r, err := c.ExecWithOptions(name, args, sdk.ExecOptions{Timeout: float64(o.Timeout / time.Millisecond)})
		if err != nil {
			return "", err
		}
		if r == nil {
			return "", errors.New("no result from the host")
		}
		if r.Killed || r.ExitCode != 0 {
			return "", errors.New(name + " failed")
		}
		limit := o.MaxBuffer
		if limit == 0 {
			limit = defaultMaxBuffer
		}
		if len(r.Stdout) > limit {
			return "", errors.New("stdout maxBuffer length exceeded")
		}
		return r.Stdout, nil
	}
}
