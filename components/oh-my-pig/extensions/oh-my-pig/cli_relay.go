// `oh-my-pig browser-relay` CLI command implementation (install, sync, native-host).
//
// upstream: packages/coding-agent/src/cli/browser-relay-cli.ts (omp v19.1.1)
// upstream: packages/coding-agent/src/commands/browser-relay.ts (omp v19.1.1)
// upstream: packages/coding-agent/src/cli/command-help.ts (omp v19.1.1)
package ohmypig

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

// browserRelayCLIOptions holds parsed arguments for `oh-my-pig browser-relay`.
// upstream: packages/coding-agent/src/cli/browser-relay-cli.ts:21-29
type browserRelayCLIOptions struct {
	action   string
	dir      string
	noChrome bool
	profile  string
	help     bool
	// rest are trailing args after `native-host` (Chrome passes the calling extension's origin).
	rest []string
}

func browserRelayHelpText() string {
	return fmt.Sprintf(`Install or sync the %s Chrome extension; its native host serves the browser relay

USAGE
  $ %s %s ACTION [FLAGS]

ARGUMENTS
  ACTION   Action: install | sync | native-host (native-host is launched by Chrome) (install|sync|native-host)

FLAGS
      --dir=<value>      Extension install directory (install; default %s)
      --no-chrome        Print the manual Chrome install steps (default and only mode in this port)
      --profile=<value>  Refused: unattended Chrome profile installation is not part of this port
  -h, --help             Show this help message

EXAMPLES
  %s %s install    # install the %s extension, register its native host, and print manual Chrome steps
  %s %s sync       # sync every registered extension directory
`,
		relay.ExtensionName,
		relay.ExtensionName, relay.CLICommand,
		relay.DefaultExtensionDir(),
		relay.ExtensionName, relay.CLICommand, relay.ExtensionName,
		relay.ExtensionName, relay.CLICommand,
	)
}

func parseBrowserRelayArgs(args []string) (browserRelayCLIOptions, error) {
	var opts browserRelayCLIOptions
	if len(args) == 0 {
		opts.help = true
		return opts, nil
	}
	if args[0] == "native-host" {
		opts.action = "native-host"
		for _, a := range args[1:] {
			if a == "-h" || a == "--help" {
				opts.help = true
				return opts, nil
			}
		}
		opts.rest = args[1:]
		return opts, nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
			return opts, nil
		case arg == "--no-chrome":
			opts.noChrome = true
		case arg == "--dir":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("flag --dir requires a value")
			}
			i++
			opts.dir = args[i]
		case strings.HasPrefix(arg, "--dir="):
			opts.dir = strings.TrimPrefix(arg, "--dir=")
		case arg == "--profile":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("flag --profile requires a value")
			}
			i++
			opts.profile = args[i]
		case strings.HasPrefix(arg, "--profile="):
			opts.profile = strings.TrimPrefix(arg, "--profile=")
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag: %s", arg)
		default:
			if opts.action == "" {
				opts.action = arg
			} else {
				return opts, fmt.Errorf("unexpected argument: %s", arg)
			}
		}
	}
	if opts.action == "" {
		opts.help = true
		return opts, nil
	}
	switch opts.action {
	case "install", "sync", "native-host":
		return opts, nil
	default:
		return opts, fmt.Errorf("Unknown action: %s", opts.action)
	}
}

// runBrowserRelayCLI executes `oh-my-pig browser-relay ...` from the CLI hook.
// upstream: packages/coding-agent/src/cli/browser-relay-cli.ts:32-121
func runBrowserRelayCLI(h clihook.Env) int {
	opts, err := parseBrowserRelayArgs(h.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(h.Stderr, "%s %s: %v\n", relay.ExtensionName, relay.CLICommand, err)
		_, _ = fmt.Fprintf(h.Stderr, "Run `%s %s --help` for usage.\n", relay.ExtensionName, relay.CLICommand)
		return 2
	}
	if opts.help {
		_, _ = io.WriteString(h.Stdout, browserRelayHelpText())
		return 0
	}

	switch opts.action {
	case "install":
		return runBrowserRelayInstall(opts, h.Stdout, h.Stderr, relay.InstallLocations{}, relay.NativeHostOptions{}, relay.ChromiumLocations{})
	case "sync":
		return runBrowserRelaySync(h.Stdout, h.Stderr, relay.InstallSyncOptions{})
	case "native-host":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return relay.RunNativeHost(ctx, relay.HostIO{
			Stdin:  h.Stdin,
			Stdout: h.Stdout,
			Stderr: h.Stderr,
			Getenv: h.Getenv,
			Args:   opts.rest,
		})
	default:
		_, _ = fmt.Fprintf(h.Stderr, "%s %s: Unknown action: %s\n", relay.ExtensionName, relay.CLICommand, opts.action)
		return 2
	}
}

// runBrowserRelayInstall installs the extension, registers the native messaging host, and prints manual Chrome steps.
// Unattended Chrome profile installation (`--profile`) is explicitly refused.
// upstream: packages/coding-agent/src/cli/browser-relay-cli.ts:61-121
func runBrowserRelayInstall(
	opts browserRelayCLIOptions,
	stdout, stderr io.Writer,
	installLoc relay.InstallLocations,
	nativeHostOpts relay.NativeHostOptions,
	chromiumLoc relay.ChromiumLocations,
) int {
	if opts.profile != "" {
		_, _ = fmt.Fprintf(
			stderr,
			"%s %s install: --profile is not supported; unattended Chrome profile installation is not part of this port. Run without --profile and load the unpacked directory manually in chrome://extensions.\n",
			relay.ExtensionName, relay.CLICommand,
		)
		return 2
	}

	targetDir := opts.dir
	if targetDir == "" {
		if installLoc.DefaultDir != "" {
			targetDir = installLoc.DefaultDir
		} else {
			targetDir = relay.DefaultExtensionDir()
		}
	}

	dir, err := relay.InstallRelayExtension(targetDir, installLoc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s %s install: %v\n", relay.ExtensionName, relay.CLICommand, err)
		return 1
	}

	ids, err := relay.RegisteredRelayExtensionIDs(installLoc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s %s install: %v\n", relay.ExtensionName, relay.CLICommand, err)
		return 1
	}
	if err := relay.RegisterRelayNativeHostWithOptions(ids, nativeHostOpts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s %s install: %v\n", relay.ExtensionName, relay.CLICommand, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "Installed the %s extension to %s\n", relay.ExtensionName, dir)

	extensionID := relay.ExtensionIDForPath(dir, runtime.GOOS)
	profiles := relay.FindUnpackedInstalls(extensionID, chromiumLoc)

	var loaded []relay.ChromiumProfileInstall
	developerMode := false
	for _, p := range profiles {
		if p.Loaded {
			loaded = append(loaded, p)
		}
		if p.DeveloperMode {
			developerMode = true
		}
	}
	for _, p := range loaded {
		if p.Enabled {
			_, _ = fmt.Fprintf(stdout, "%s (%s) has it loaded and reloads into this build by itself once idle.\n", p.Browser, p.Profile)
		} else {
			_, _ = fmt.Fprintf(stdout, "%s (%s) has it loaded but switched off; turn it on in chrome://extensions.\n", p.Browser, p.Profile)
		}
	}
	if len(loaded) > 0 {
		_, _ = io.WriteString(stdout, "\n")
		_, _ = io.WriteString(stdout, "Once loaded, this extension can read and act on every site in the Chrome profile it is loaded into, with that profile's signed-in accounts, when an oh-my-pig session uses the relay.\n")
		_, _ = fmt.Fprintf(stdout, "Optional: set `browser.relay: true` in settings to make the browser tool drive this Chrome by default.\n")
		return 0
	}

	_, _ = io.WriteString(stdout, "\n")
	_, _ = io.WriteString(stdout, "No Chrome profile has it loaded yet. Chrome reserves that for you, once:\n")
	devModeStep := ", turn on Developer mode"
	if developerMode {
		devModeStep = ""
	}
	_, _ = fmt.Fprintf(stdout, "  open chrome://extensions%s, click \"Load unpacked\" and pick %s\n", devModeStep, dir)
	_, _ = fmt.Fprintf(stdout, "From then on %s updates and reloads it by itself. Its toolbar badge shows 'on' while its native host serves the relay.\n", relay.ExtensionName)
	_, _ = io.WriteString(stdout, "Once loaded, this extension can read and act on every site in the Chrome profile it is loaded into, with that profile's signed-in accounts, when an oh-my-pig session uses the relay.\n")
	_, _ = fmt.Fprintf(stdout, "Optional: set `browser.relay: true` in settings to make the browser tool drive this Chrome by default.\n")
	return 0
}

// runBrowserRelaySync updates every registered extension directory and refreshes existing native host manifests.
// upstream: packages/coding-agent/src/cli/browser-relay-cli.ts:43-59
func runBrowserRelaySync(stdout, stderr io.Writer, opts relay.InstallSyncOptions) int {
	results, err := relay.SyncRelayInstalls(opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s %s sync: %v\n", relay.ExtensionName, relay.CLICommand, err)
		return 1
	}
	if len(results) == 0 {
		_, _ = fmt.Fprintf(stdout, "No %s extension directories registered. Use `%s %s install` to register one.\n", relay.ExtensionName, relay.ExtensionName, relay.CLICommand)
		return 0
	}
	failed := false
	for _, r := range results {
		if r.Status == relay.SyncStatusFailed {
			failed = true
			_, _ = fmt.Fprintf(stderr, "%s: failed (%s)\n", r.Dir, r.Error)
		} else {
			_, _ = fmt.Fprintf(stdout, "%s: %s\n", r.Dir, r.Status)
		}
	}
	if failed {
		return 1
	}
	return 0
}
