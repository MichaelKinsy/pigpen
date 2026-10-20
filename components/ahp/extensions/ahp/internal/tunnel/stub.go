// Package tunnel drives Microsoft's `devtunnel` CLI (port of pi-ahp's src/tunnel/discovery.ts and
// devtunnel.ts). This file is the signature stub of the red commit: every function compiles and
// does nothing, so the twins fail on behaviour.
package tunnel

const (
	LauncherLabel = "vscode-server-launcher"
	ProtocolLabel = "protocolv5"
	Port          = 31546
	IdentityLabel = "_pi_ahp"
)

func DisplayLabel(labels []string) (string, bool) { return "", false }
func NameLabel(name string) (string, bool)        { return "", false }

type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

type CommandResult struct {
	Status *int
	Signal string
	Stdout string
	Stderr string
}

type Runner func(args []string) (CommandResult, error)

type Tunnel struct {
	TunnelID string
	Labels   []string
}

type CLI struct{}

func NewCLI(runner Runner) *CLI                         { return &CLI{} }
func (c *CLI) RequireLogin() error                      { return nil }
func (c *CLI) FindTunnel() (*Tunnel, error)             { return nil, nil }
func (c *CLI) CreateTunnel(name string) (string, error) { return "", nil }
func (c *CLI) EnsurePort(id string) error               { return nil }
func (c *CLI) Rename(id, from, to string) error         { return nil }
