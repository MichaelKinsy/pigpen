package warden

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Desktop notifications (src/notify.ts) for the moments the user has to come back to the terminal: a held call
// the agent will ask about, a confirm dialog waiting for an answer, a stopped runaway. The notifier is detected
// once per session with a probe (never inside a tool_call handler); sending is fire-and-forget and a failure is
// silent. Commands run through os/exec in the extension process, as upstream's execFile does: the host's Exec has
// no environment option, and PowerShell receives the text in PI_WARDEN_TITLE / PI_WARDEN_BODY.

// NotifierName names a built-in notifier.
type NotifierName string

// Notifiers.
const (
	NotifierOsascript     NotifierName = "osascript"
	NotifierNotifySend    NotifierName = "notify-send"
	NotifierDunstify      NotifierName = "dunstify"
	NotifierGdbus         NotifierName = "gdbus"
	NotifierKdialog       NotifierName = "kdialog"
	NotifierZenity        NotifierName = "zenity"
	NotifierPowershell    NotifierName = "powershell"
	NotifierWSLPowershell NotifierName = "wsl-powershell"
)

// Notification is one notification.
type Notification struct{ Title, Body string }

// NotifierCommand is the command that shows one notification.
type NotifierCommand struct {
	File string
	Args []string
	// Env: text travels in the environment where the shell needs no quoting (PowerShell).
	Env map[string]string
}

// NotifierCandidate is one notifier and its probe: the command exists and answers.
type NotifierCandidate struct {
	Name      NotifierName
	ProbeFile string
	ProbeArgs []string
}

// powershellAppID is Windows PowerShell's own AppUserModelId: a toast needs a registered application, and this one
// exists on every install.
const powershellAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

var toastScript = strings.Join([]string{
	"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null",
	"[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null",
	"$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)",
	"$text = $template.GetElementsByTagName('text')",
	"$text.Item(0).AppendChild($template.CreateTextNode($env:PI_WARDEN_TITLE)) | Out-Null",
	"$text.Item(1).AppendChild($template.CreateTextNode($env:PI_WARDEN_BODY)) | Out-Null",
	"$toast = [Windows.UI.Notifications.ToastNotification]::new($template)",
	"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('" + powershellAppID + "').Show($toast)",
}, "; ")

// nodePlatform is Node's process.platform for this build.
func nodePlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

// NotifierCandidates is the platform order: the native route first, then the desktop-agnostic tools, then the
// shells that reach another desktop (WSL). platform is Node's process.platform ("darwin", "win32", "linux", ...).
func NotifierCandidates(platform string) []NotifierCandidate {
	pwshProbe := []string{"-NoProfile", "-NonInteractive", "-Command", "exit 0"}
	switch platform {
	case "darwin":
		return []NotifierCandidate{{NotifierOsascript, "osascript", []string{"-e", "return 0"}}}
	case "win32":
		return []NotifierCandidate{{NotifierPowershell, "powershell", pwshProbe}}
	}
	return []NotifierCandidate{
		{NotifierNotifySend, "notify-send", []string{"--version"}},
		{NotifierDunstify, "dunstify", []string{"--help"}},
		{NotifierGdbus, "gdbus", []string{"help"}},
		{NotifierKdialog, "kdialog", []string{"--version"}},
		{NotifierZenity, "zenity", []string{"--version"}},
		// WSL: the Linux side has no notification daemon, but Windows PowerShell is on PATH and shows a toast.
		{NotifierWSLPowershell, "powershell.exe", pwshProbe},
	}
}

// appleScriptString is an AppleScript string literal: only backslash and double quote need escaping.
func appleScriptString(text string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`
}

// gvariantString is a GVariant string literal for gdbus: single quotes with backslash escapes.
func gvariantString(text string) string {
	return `'` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `'`, `\'`) + `'`
}

// NotifierTarget is a built-in notifier by name, or a user-configured argv in which `{title}` and `{body}` in any
// argument are replaced (the text is also in PI_WARDEN_TITLE / PI_WARDEN_BODY).
type NotifierTarget struct {
	Name NotifierName
	// Argv is the configured command; Custom marks it (an empty configured argv sends nothing).
	Argv   []string
	Custom bool
}

// NamedNotifier targets a built-in notifier.
func NamedNotifier(name NotifierName) NotifierTarget { return NotifierTarget{Name: name} }

// CustomNotifier targets a configured argv.
func CustomNotifier(argv []string) NotifierTarget { return NotifierTarget{Argv: argv, Custom: true} }

// BuildNotifierCommand is the command that shows one notification with the given tool. Text goes as arguments or
// environment, never through a shell.
func BuildNotifierCommand(target NotifierTarget, n Notification) NotifierCommand {
	title, body := n.Title, n.Body
	if target.Custom {
		cmd := NotifierCommand{Env: map[string]string{"PI_WARDEN_TITLE": title, "PI_WARDEN_BODY": body}}
		if len(target.Argv) == 0 {
			cmd.Args = []string{}
			return cmd
		}
		cmd.File = target.Argv[0]
		cmd.Args = make([]string, 0, len(target.Argv)-1)
		for _, arg := range target.Argv[1:] {
			cmd.Args = append(cmd.Args, strings.ReplaceAll(strings.ReplaceAll(arg, "{title}", title), "{body}", body))
		}
		return cmd
	}
	switch target.Name {
	case NotifierOsascript:
		return NotifierCommand{File: "osascript", Args: []string{"-e", "display notification " + appleScriptString(body) + " with title " + appleScriptString(title) + ` sound name "Submarine"`}}
	case NotifierNotifySend:
		return NotifierCommand{File: "notify-send", Args: []string{"--app-name=pi-warden", "--urgency=critical", "--icon=dialog-warning", title, body}}
	case NotifierDunstify:
		return NotifierCommand{File: "dunstify", Args: []string{"--appname=pi-warden", "--urgency=critical", title, body}}
	case NotifierGdbus:
		return NotifierCommand{File: "gdbus", Args: []string{"call", "--session", "--dest", "org.freedesktop.Notifications", "--object-path", "/org/freedesktop/Notifications", "--method", "org.freedesktop.Notifications.Notify",
			"pi-warden", "0", "dialog-warning", gvariantString(title), gvariantString(body), "[]", "{'urgency': <byte 2>}", "-1"}}
	case NotifierKdialog:
		return NotifierCommand{File: "kdialog", Args: []string{"--title", title, "--passivepopup", body, "15"}}
	case NotifierZenity:
		return NotifierCommand{File: "zenity", Args: []string{"--notification", "--text=" + title + ": " + body}}
	case NotifierPowershell, NotifierWSLPowershell:
		file := "powershell"
		if target.Name == NotifierWSLPowershell {
			file = "powershell.exe"
		}
		return NotifierCommand{File: file, Args: []string{"-NoProfile", "-NonInteractive", "-Command", toastScript}, Env: map[string]string{"PI_WARDEN_TITLE": title, "PI_WARDEN_BODY": body}}
	}
	return NotifierCommand{}
}

// NotifyRunner runs a command detached from the terminal and reports whether it exited cleanly. It never fails.
type NotifyRunner func(file string, args []string, timeout time.Duration, env map[string]string) bool

// StartNotifierCommand spawns the command now and returns the function that waits for it: Node's execFile spawns
// in the calling tick, so the process is already running when the hook returns. Output is discarded, the
// environment is the process's plus env, and the window stays hidden on Windows. A command that cannot start
// reports false from wait.
func StartNotifierCommand(file string, args []string, timeout time.Duration, env map[string]string) (wait func() bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, file, args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		return func() bool { return false }
	}
	return func() bool {
		defer cancel()
		return cmd.Wait() == nil
	}
}

// RunNotifierCommand is the real NotifyRunner: spawn and wait.
func RunNotifierCommand(file string, args []string, timeout time.Duration, env map[string]string) bool {
	return StartNotifierCommand(file, args, timeout, env)()
}

// DetectNotifier is the first notifier whose probe answers on this machine, or "" when the desktop cannot be
// reached.
func DetectNotifier(platform string, run NotifyRunner, timeout time.Duration) NotifierName {
	for _, c := range NotifierCandidates(platform) {
		if run(c.ProbeFile, c.ProbeArgs, timeout, nil) {
			return c.Name
		}
	}
	return ""
}

// ClipBody keeps the reason first: notifications truncate around a hundred characters.
func ClipBody(text string, limit int) string {
	flat := jsTrim(jsCollapseSpace(text))
	if utf16Len(flat) <= limit {
		return flat
	}
	return utf16Slice(flat, 0, limit-1) + "…"
}

// SendNotification shows one notification and reports whether the command exited cleanly. An empty configured
// command sends nothing.
func SendNotification(target NotifierTarget, n Notification, run NotifyRunner, timeout time.Duration) bool {
	command := BuildNotifierCommand(target, Notification{Title: n.Title, Body: ClipBody(n.Body, 180)})
	if command.File == "" {
		return false
	}
	return run(command.File, command.Args, timeout, command.Env)
}
