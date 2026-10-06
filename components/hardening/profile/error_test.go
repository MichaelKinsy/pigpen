package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEveryCodeHasAFixedText(t *testing.T) {
	want := []Code{"audit_unavailable", "cancelled", "credential_expired", "credential_malformed", "credential_unavailable",
		"egress_address_denied", "egress_denied", "egress_proxy_ignored", "judge_unavailable", "jwks_unavailable",
		"policy_denied", "policy_error", "policy_forbidden", "policy_unavailable", "profile_misconfigured",
		"token_alg_denied", "token_invalid", "ui_unavailable"}
	got := Codes()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("codes = %v\nwant    %v", got, want)
	}
	for _, c := range got {
		if !c.Valid() || c.Text() == "" {
			t.Errorf("%s has no text", c)
		}
	}
	if Code("made_up").Valid() || Code("").Valid() {
		t.Fatal("a code outside the set is valid")
	}
}

func TestErrorTextIsFixedPerCode(t *testing.T) {
	e := NewError(CredentialExpired, FlagCredentialFile, "websearch")
	if e.Error() != "pigpen websearch: credentialFile: credential_expired: the credential has expired" {
		t.Fatal(e.Error())
	}
	if NewError(ProfileMisconfigured, "", "").Error() != "pigpen: profile_misconfigured: the enterprise profile is invalid or incomplete" {
		t.Fatal(NewError(ProfileMisconfigured, "", "").Error())
	}
}

func TestErrorFieldsAreValidatedNotEchoed(t *testing.T) {
	e := NewError(EgressDenied, "https://evil/?token=SECRET", "/home/user/.pig/SECRET")
	for _, s := range []string{e.Error(), e.Flag, e.Package} {
		if strings.Contains(s, "SECRET") || strings.Contains(s, "/") {
			t.Fatalf("echoed input: %q", s)
		}
	}
	if e.Package != "invalid" || e.Flag != "" {
		t.Fatalf("package=%q flag=%q", e.Package, e.Flag)
	}
	if got := NewError(Code("TOKEN=abc"), "", "x").Code; got != PolicyError {
		t.Fatalf("an unknown code became %q", got)
	}
	if s := NewError(UIUnavailable, FlagHeadless, "warden").WithSite("a b/c").Site; s != "" {
		t.Fatalf("site %q", s)
	}
	if s := NewError(UIUnavailable, FlagHeadless, "warden").WithSite("setup-confirm").Site; s != "setup-confirm" {
		t.Fatalf("site %q", s)
	}
}

func TestErrorsIsAndAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", NewError(CredentialExpired, FlagCredentialFile, "a2a"))
	if !errors.Is(err, &Error{Code: CredentialExpired}) {
		t.Fatal("code match")
	}
	if !errors.Is(err, &Error{Code: CredentialExpired, Flag: FlagCredentialFile, Package: "a2a"}) {
		t.Fatal("full match")
	}
	if errors.Is(err, &Error{Code: CredentialMalformed}) || errors.Is(err, &Error{Code: CredentialExpired, Package: "warden"}) ||
		errors.Is(err, &Error{Code: CredentialExpired, Flag: FlagEgressPolicy}) || errors.Is(err, &Error{Code: CredentialExpired, Site: "x"}) {
		t.Fatal("matched the wrong error")
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CredentialExpired {
		t.Fatal("errors.As")
	}
	if CodeOf(err) != CredentialExpired || CodeOf(errors.New("x")) != "" || !IsCode(err, CredentialExpired) {
		t.Fatal("CodeOf")
	}
}

func TestOnlyAContextErrorIsWrapped(t *testing.T) {
	c := NewError(Cancelled, "", "websearch").WithCause(context.Canceled)
	if !errors.Is(c, context.Canceled) {
		t.Fatal("cancelled does not unwrap to context.Canceled")
	}
	leaky := NewError(CredentialUnavailable, FlagCredentialFile, "websearch").WithCause(errors.New("open /secret/path: denied"))
	if leaky.Unwrap() != nil || strings.Contains(leaky.Error(), "secret") {
		t.Fatal("a non-context cause was kept")
	}
}
