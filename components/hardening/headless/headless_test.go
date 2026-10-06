package headless

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func on() profile.Flags {
	agentDir := ""
	f, err := profile.Load("warden", agentDir, func(k string) string {
		if k == "PIGPEN_WARDEN_ENTERPRISE_HEADLESS" {
			return "1"
		}
		return ""
	})
	if err != nil {
		panic(err)
	}
	return f
}

func TestUIIsFalseUnderTheFlagWhateverTheHostSays(t *testing.T) {
	if UI(on(), true) || UI(on(), false) {
		t.Fatal("headless saw a UI")
	}
	if !UI(profile.Flags{}, true) || UI(profile.Flags{}, false) {
		t.Fatal("the flag-off answer is the host's")
	}
}

func TestBlockingIsATypedErrorWithAFixedText(t *testing.T) {
	err := Blocking("warden", "setup-confirm")
	var pe *profile.Error
	if !errors.As(err, &pe) || pe.Code != profile.UIUnavailable || pe.Flag != profile.FlagHeadless || pe.Package != "warden" || pe.Site != "setup-confirm" {
		t.Fatalf("%v", err)
	}
	if err.Error() != "pigpen warden: headless: ui_unavailable: a dialog is needed and none can be shown (setup-confirm)" {
		t.Fatal(err.Error())
	}
	hostile := Blocking("warden", "Delete everything? [y/N] /home/user/.ssh/id_rsa")
	if strings.Contains(hostile.Error(), "Delete") || strings.Contains(hostile.Error(), "ssh") {
		t.Fatalf("echoed the input: %v", hostile)
	}
	if !errors.Is(err, &profile.Error{Code: profile.UIUnavailable}) {
		t.Fatal("errors.Is")
	}
}

func TestCancellationTakesPrecedence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if Cancelled(ctx, "warden") != nil || Cancelled(nil, "warden") != nil { //nolint:staticcheck // a nil context is tolerated
		t.Fatal("a live context is cancelled")
	}
	cancel()
	err := Cancelled(ctx, "warden")
	if !profile.IsCode(err, profile.Cancelled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if err := Dialog(ctx, "warden", "gate-confirm"); !profile.IsCode(err, profile.Cancelled) {
		t.Fatalf("cancelled context at a dialog site: %v", err)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	defer dcancel()
	if err := Dialog(dctx, "warden", "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if err := Dialog(context.Background(), "warden", "gate-confirm"); !profile.IsCode(err, profile.UIUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestGuardDoesNothingWhenTheFlagIsOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Guard(ctx, profile.Flags{}, "warden", "gate-confirm"); err != nil {
		t.Fatalf("flag off: %v", err)
	}
	if err := Guard(context.Background(), on(), "warden", "gate-confirm"); !profile.IsCode(err, profile.UIUnavailable) {
		t.Fatalf("flag on: %v", err)
	}
	if err := Guard(ctx, on(), "warden", "gate-confirm"); !profile.IsCode(err, profile.Cancelled) {
		t.Fatalf("flag on, cancelled: %v", err)
	}
}
