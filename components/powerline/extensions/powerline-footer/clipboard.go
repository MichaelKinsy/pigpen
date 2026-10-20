package powerline_footer

import (
	"encoding/base64"
	"errors"
	"os"
	"regexp"
	"runtime"
	"strings"
)

// copyToClipboard of pi-coding-agent (utils/clipboard.js): the platform's clipboard command, WSL's Windows clipboard, and OSC 52
// where there is no other route. Differences from Pi, all forced by the SDK:
//   - Pi first tries its native clipboard module outside Linux; a Go extension has none, so macOS and Windows go straight to
//     pbcopy and clip.
//   - OSC 52 is an escape sequence Pi writes to the terminal; a Go extension has no terminal output (its stdout is the wire),
//     so the remote-session and headless branches that rely on it report "Clipboard unavailable" instead of copying.
//   - Commands run through the host's exec (`sh -c`), the only way to feed a command's stdin.

// clipboardRunner runs one clipboard command, feeding input to its stdin when it is not empty (runClipboardCommand). It
// returns the command's stdout and whether it succeeded.
type clipboardRunner interface {
	run(command string, args []string, input string, timeoutMs float64) (stdout string, ok bool)
}

// clipboardEnv is what copyToClipboard reads from the process and the machine.
type clipboardEnv struct {
	goos     string
	getenv   func(string) string
	isWSL    func() bool
	tempFile func(text string) (path string, remove func(), err error)
}

func osClipboardEnv() clipboardEnv {
	return clipboardEnv{goos: runtime.GOOS, getenv: os.Getenv, isWSL: isWSL, tempFile: writeClipboardTempFile}
}

var wslRelease = regexp.MustCompile(`(?i)microsoft|wsl`)

// isWSL: Windows Subsystem for Linux (utils/wsl.js).
func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSLENV") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	return err == nil && wslRelease.Match(data)
}

func writeClipboardTempFile(text string) (string, func(), error) {
	f, err := os.CreateTemp("", "pi-wsl-clip-*.txt")
	if err != nil {
		return "", func() {}, err
	}
	remove := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		remove()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		remove()
		return "", func() {}, err
	}
	return f.Name(), remove, nil
}

func copyViaWindowsClipboard(run clipboardRunner, env clipboardEnv, text string) bool {
	path, remove, err := env.tempFile(text)
	if err != nil {
		return false
	}
	defer remove()
	out, ok := run.run("wslpath", []string{"-w", path}, "", 1000)
	winPath := jsTrim(out)
	if !ok || winPath == "" {
		return false
	}
	script := "Set-Clipboard -Value ([System.IO.File]::ReadAllText('" + strings.ReplaceAll(winPath, "'", "''") + "', [System.Text.Encoding]::UTF8))"
	_, ok = run.run("powershell.exe", []string{"-NoProfile", "-Command", script}, "", 5000)
	return ok
}

const maxOSC52EncodedLength = 100_000

// copyToClipboard copies text; the error is Pi's "Clipboard unavailable" family.
func copyToClipboard(run clipboardRunner, env clipboardEnv, text string) error {
	p := env.goos
	get := env.getenv
	copied := false

	if !copied {
		type command struct {
			name string
			args []string
		}
		var commands []command
		switch p {
		case "darwin":
			commands = append(commands, command{"pbcopy", nil})
		case "windows":
			commands = append(commands, command{"clip", nil})
		default:
			if get("TERMUX_VERSION") != "" {
				commands = append(commands, command{"termux-clipboard-set", nil})
			}
			if get("WAYLAND_DISPLAY") != "" {
				commands = append(commands, command{"wl-copy", nil})
			}
			if get("DISPLAY") != "" {
				commands = append(commands, command{"xclip", []string{"-selection", "clipboard"}}, command{"xsel", []string{"--clipboard", "--input"}})
			}
		}
		for _, c := range commands {
			if _, ok := run.run(c.name, c.args, text, 5000); ok {
				copied = true
				break
			}
		}
	}

	// Windows Terminal (WT_SESSION) would get OSC 52 first; it cannot be emitted (see the file comment), so WSL goes straight to
	// the Windows clipboard.
	if !copied && p == "linux" && env.isWSL() {
		copied = copyViaWindowsClipboard(run, env, text)
	}
	if copied {
		return nil
	}

	remote := get("SSH_CONNECTION") != "" || get("SSH_CLIENT") != "" || get("MOSH_CONNECTION") != "" || get("PI_CLIPBOARD_OSC52") == "1"
	headless := p == "linux" && get("DISPLAY") == "" && get("WAYLAND_DISPLAY") == "" && get("TERMUX_VERSION") == ""
	if remote || headless {
		if len(base64.StdEncoding.EncodeToString([]byte(text))) > maxOSC52EncodedLength {
			return errors.New("Clipboard unavailable: text exceeds the OSC 52 size limit")
		}
		return errors.New("Clipboard unavailable: the terminal clipboard (OSC 52) cannot be written from a Go extension")
	}
	if get("TERMUX_VERSION") != "" {
		return errors.New("Clipboard unavailable: install the Termux:API app and `termux-api` package")
	}
	if p == "linux" {
		if get("WAYLAND_DISPLAY") != "" {
			return errors.New("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
		}
		if get("DISPLAY") != "" {
			return errors.New("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
		}
	}
	return errors.New("Clipboard unavailable")
}

// clipboardScript is the `sh -c` line that runs command with input on its stdin. The input travels as base64 (its alphabet needs
// no quoting); macOS's base64 decodes with -D, the others with -d.
func clipboardScript(input, goos string) string {
	decode := "-d"
	if goos == "darwin" {
		decode = "-D"
	}
	return "printf %s '" + base64.StdEncoding.EncodeToString([]byte(input)) + "' | base64 " + decode + ` | exec "$0" "$@"`
}
