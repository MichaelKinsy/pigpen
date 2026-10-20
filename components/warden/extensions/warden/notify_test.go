package warden

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNote = Notification{Title: "pi-warden", Body: "Held bash: destructive: recursive rm on an absolute path. The agent will ask you in chat."}

func candidateNames(platform string) []NotifierName {
	var names []NotifierName
	for _, c := range NotifierCandidates(platform) {
		names = append(names, c.Name)
	}
	return names
}

// twin: tests/notify.test.ts:8
func TestEachPlatformHasANativeRouteFirstAndNoShell(t *testing.T) {
	for platform, want := range map[string][]NotifierName{
		"darwin": {"osascript"}, "win32": {"powershell"},
		"linux": {"notify-send", "dunstify", "gdbus", "kdialog", "zenity", "wsl-powershell"},
	} {
		if got := candidateNames(platform); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v", platform, got)
		}
		for _, name := range want {
			cmd := BuildNotifierCommand(NamedNotifier(name), testNote)
			if cmd.File == "" || strings.ContainsAny(cmd.File, ";&|`$") || len(cmd.Args) == 0 {
				t.Fatalf("%s must run a plain executable: %+v", name, cmd)
			}
		}
	}
}

// twin: tests/notify.test.ts:21
func TestAppleScriptAndGVariantLiteralsEscapeQuotesAndBackslashes(t *testing.T) {
	tricky := Notification{Title: `a "quoted" \ title`, Body: `it's "done"`}
	apple := BuildNotifierCommand(NamedNotifier("osascript"), tricky)
	if apple.File != "osascript" || apple.Args[1] != `display notification "it's \"done\"" with title "a \"quoted\" \\ title" sound name "Submarine"` {
		t.Fatalf("%+v", apple)
	}
	dbus := BuildNotifierCommand(NamedNotifier("gdbus"), tricky)
	has := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	if !has(dbus.Args, `'a "quoted" \\ title'`) || !has(dbus.Args, `'it\'s "done"'`) {
		t.Fatalf("%q", dbus.Args)
	}
	toast := BuildNotifierCommand(NamedNotifier("powershell"), tricky)
	if toast.File != "powershell" || !reflect.DeepEqual(toast.Env, map[string]string{"PI_WARDEN_TITLE": tricky.Title, "PI_WARDEN_BODY": tricky.Body}) {
		t.Fatalf("%+v", toast)
	}
	if strings.Contains(strings.Join(toast.Args, " "), "quoted") {
		t.Fatal("no user text inside the script")
	}
	if last := toast.Args[len(toast.Args)-1]; !strings.Contains(last, "ToastNotificationManager") || !strings.Contains(last, "$env:PI_WARDEN_BODY") || strings.Index(last, "ToastNotificationManager") > strings.Index(last, "$env:PI_WARDEN_BODY") {
		t.Fatal("toast script")
	}
	if BuildNotifierCommand(NamedNotifier("wsl-powershell"), tricky).File != "powershell.exe" {
		t.Fatal("wsl")
	}
	send := BuildNotifierCommand(NamedNotifier("notify-send"), tricky)
	if !reflect.DeepEqual(send.Args[len(send.Args)-2:], []string{tricky.Title, tricky.Body}) {
		t.Fatal("title and body are separate arguments")
	}
}

// twin: tests/notify.test.ts:39
func TestAConfiguredCommandGetsPlaceholdersReplacedAndEmptyArgvSendsNothing(t *testing.T) {
	custom := BuildNotifierCommand(CustomNotifier([]string{"curl", "-d", "{body}", "-H", "Title: {title}", "https://ntfy.sh/topic"}), testNote)
	if custom.File != "curl" || !reflect.DeepEqual(custom.Args, []string{"-d", testNote.Body, "-H", "Title: " + testNote.Title, "https://ntfy.sh/topic"}) ||
		!reflect.DeepEqual(custom.Env, map[string]string{"PI_WARDEN_TITLE": testNote.Title, "PI_WARDEN_BODY": testNote.Body}) {
		t.Fatalf("%+v", custom)
	}
	var calls []string
	run := func(file string, _ []string, _ time.Duration, _ map[string]string) bool {
		calls = append(calls, file)
		return true
	}
	if SendNotification(CustomNotifier([]string{}), testNote, run, time.Second) || len(calls) != 0 {
		t.Fatal("an empty argv sends nothing")
	}
	if !SendNotification(CustomNotifier([]string{"my-notifier"}), testNote, run, time.Second) || !reflect.DeepEqual(calls, []string{"my-notifier"}) {
		t.Fatalf("%v", calls)
	}
}

// twin: tests/notify.test.ts:52
func TestDetectionTakesTheFirstCandidateWhoseProbeAnswers(t *testing.T) {
	var probed []string
	onlyZenity := func(file string, _ []string, _ time.Duration, _ map[string]string) bool {
		probed = append(probed, file)
		return file == "zenity"
	}
	if got := DetectNotifier("linux", onlyZenity, 3*time.Second); got != "zenity" {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(probed, []string{"notify-send", "dunstify", "gdbus", "kdialog", "zenity"}) {
		t.Fatalf("%v", probed)
	}
	if got := DetectNotifier("linux", func(string, []string, time.Duration, map[string]string) bool { return false }, time.Second); got != "" {
		t.Fatal(got)
	}
	if got := DetectNotifier("darwin", func(string, []string, time.Duration, map[string]string) bool { return true }, time.Second); got != "osascript" {
		t.Fatal(got)
	}
	// The runner contract is to return false, so a panic is the caller's bug to see.
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "probe exploded") {
			t.Fatalf("recovered %v", r)
		}
	}()
	DetectNotifier("darwin", func(string, []string, time.Duration, map[string]string) bool { panic("probe exploded") }, time.Second)
}

// twin: tests/notify.test.ts:63
func TestSendNotificationClipsTheBodyAndReportsTheExit(t *testing.T) {
	var seen []string
	run := func(_ string, args []string, _ time.Duration, _ map[string]string) bool { seen = args; return true }
	if !SendNotification(NamedNotifier("notify-send"), Notification{Title: "t", Body: strings.Repeat("x", 300) + "\n\nmore"}, run, time.Second) {
		t.Fatal("exit")
	}
	last := seen[len(seen)-1]
	if utf16Len(last) != 180 || !strings.HasSuffix(last, "…") {
		t.Fatalf("%q", last)
	}
	if got := ClipBody("  a   b\nc ", 180); got != "a b c" {
		t.Fatal(got)
	}
}
