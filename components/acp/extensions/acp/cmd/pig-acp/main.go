// Command pig-acp is the Agent Client Protocol entrypoint for PiG: an editor starts it,
// speaks ACP on its stdin and stdout, and pig-acp drives `pig --mode rpc`. It is a Go port of
// pi-acp by Sergii Kozak (MIT); see the Package's CREDITS.md.
//
// Only protocol messages go to stdout. Diagnostics go to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/acp"
	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/pirpc"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0"

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, " ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

const usage = `pig-acp: Agent Client Protocol (ACP) adapter for PiG

An editor (Zed, JetBrains and other ACP clients) starts pig-acp and speaks ACP on its
stdin and stdout. pig-acp starts 'pig --mode rpc' for every session.

Usage:
  pig-acp [--pig <command>] [--piglet <name-or-file>] [--pig-arg <arg>]...
  pig-acp --terminal-login [--pig <command>]
  pig-acp --version | --help

Flags:
  --pig <command>       the pig executable or a Piglet Binary to drive (default: pig)
  --piglet <value>      start pig with --piglet <value> (a Piglet name or a piglet.yaml)
  --pig-arg <arg>       an extra argument for pig; repeatable
  --terminal-login      start pig interactively, for signing in or configuring keys
  --version             print the version and the pinned ACP protocol version
  --help                print this text

Environment:
  PIG_ACP_PIG_COMMAND              the pig executable when --pig is not given
                                   (PI_ACP_PI_COMMAND is accepted for pi-acp users)
  PIG_ACP_ENABLE_EMBEDDED_CONTEXT  "true" advertises embedded context support in prompts
                                   (PI_ACP_ENABLE_EMBEDDED_CONTEXT is accepted)
  PIG_HOME, PIG_CODING_AGENT_DIR   where pig keeps its state; the session map is <PIG_HOME>/pig-acp/
`

func run(args []string, in io.Reader, out, errw io.Writer) int {
	return runContext(context.Background(), args, in, out, errw)
}

func runContext(ctx context.Context, args []string, in io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("pig-acp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var pigCmd, piglet string
	var pigArgs multiFlag
	var showVersion, showHelp, terminalLogin bool
	fs.StringVar(&pigCmd, "pig", "", "")
	fs.StringVar(&piglet, "piglet", "", "")
	fs.Var(&pigArgs, "pig-arg", "")
	fs.BoolVar(&showVersion, "version", false, "")
	fs.BoolVar(&showHelp, "help", false, "")
	fs.BoolVar(&terminalLogin, "terminal-login", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(out, usage)
			return 0
		}
		msg := strings.Replace(err.Error(), "not defined: -", "not defined: --", 1)
		fmt.Fprintf(errw, "pig-acp: %s\n\n%s", msg, usage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errw, "pig-acp: unexpected argument %q\n\n%s", fs.Arg(0), usage)
		return 2
	}
	switch {
	case showHelp:
		fmt.Fprint(out, usage)
		return 0
	case showVersion:
		fmt.Fprintf(out, "pig-acp %s (ACP protocol version %d)\n", version, acp.ProtocolVersion)
		return 0
	}

	if pigCmd == "" {
		for _, name := range []string{"PIG_ACP_PIG_COMMAND", "PI_ACP_PI_COMMAND"} {
			if v := os.Getenv(name); v != "" {
				pigCmd = v
				break
			}
		}
	}
	extra := append([]string{}, pigArgs...)
	if piglet != "" {
		extra = append([]string{"--piglet", piglet}, extra...)
	}
	// A terminal login started by the editor must start the same agent the sessions do: the same pig,
	// Piglet and extra arguments.
	login := []string{}
	if pigCmd != "" {
		login = append(login, "--pig", pigCmd)
	}
	if piglet != "" {
		login = append(login, "--piglet", piglet)
	}
	for _, a := range pigArgs {
		login = append(login, "--pig-arg", a)
	}
	acp.TerminalLoginArgs = append(login, "--terminal-login")

	if terminalLogin {
		return terminalLoginRun(pigCmd, extra, in, out, errw)
	}

	acp.Version = version
	err := acp.Serve(in, out, acp.ServeOptions{
		Done: ctx.Done(),
		NewAgent: func(conn acp.Conn) *acp.Agent {
			a := acp.NewAgent(conn)
			a.Configure(pigCmd, extra)
			return a
		},
	})
	if err != nil {
		fmt.Fprintf(errw, "pig-acp: %v\n", err)
		return 1
	}
	return 0
}

// terminalLoginRun starts pig interactively (the ACP terminal auth entrypoint).
func terminalLoginRun(pigCmd string, extra []string, in io.Reader, out, errw io.Writer) int {
	cmd := pirpc.Command(pigCmd)
	c := exec.Command(cmd, extra...)
	c.Stdin, c.Stdout, c.Stderr = in, out, errw
	c.Env = os.Environ()
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if code := exitErr.ExitCode(); code >= 0 {
				return code
			}
			return 1
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(errw, "pig-acp: could not start pig (command not found: %s). Install PiG (https://github.com/MichaelKinsy/PiG) or ensure `pig` is on your PATH, or pass --pig <path>.\n", cmd)
			return 1
		}
		fmt.Fprintf(errw, "pig-acp: could not start pig (%s): %v\n", cmd, err)
		return 1
	}
	return 0
}

// buildPigArgs is the argument list of the pig child.
func buildPigArgs(extra []string, sessionPath string) []string {
	return pirpc.BuildArgs(extra, sessionPath)
}

func main() {
	// A client that closes our stdout must end the run quietly, not kill the process with SIGPIPE.
	ignoreSIGPIPE()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
