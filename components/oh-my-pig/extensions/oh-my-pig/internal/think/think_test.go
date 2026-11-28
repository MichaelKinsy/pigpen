package think

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

func saveAndRestoreHooks(t *testing.T) {
	t.Helper()
	origSetting := Setting
	origIsSubagent := IsSubagent
	t.Cleanup(func() {
		Setting = origSetting
		IsSubagent = origIsSubagent
	})
}

func TestIsEnabledDefaultOffAndToggle(t *testing.T) {
	saveAndRestoreHooks(t)
	var zero sdk.Context

	Setting = nil
	if IsEnabled(zero) {
		t.Fatal("expected IsEnabled=false when Setting hook is nil")
	}

	Setting = func(_ sdk.Context, key string) any {
		if key == SettingExternalThinking {
			return false
		}
		return nil
	}
	if IsEnabled(zero) {
		t.Fatal("expected IsEnabled=false when externalThinking=false")
	}

	Setting = func(_ sdk.Context, key string) any {
		if key == SettingExternalThinking {
			return true
		}
		return nil
	}
	if !IsEnabled(zero) {
		t.Fatal("expected IsEnabled=true when externalThinking=true")
	}
}

func TestExecuteReturnsExpectedContentAndDetails(t *testing.T) {
	var zero sdk.Context
	res, err := Execute(zero, Params{Thoughts: "step 1: analyze"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Content != "------" {
		t.Fatalf("expected content %q, got %q", "------", res.Content)
	}
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected map details, got %T", res.Details)
	}
	if recorded, ok := details["recorded"].(bool); !ok || !recorded {
		t.Fatalf("expected recorded=true, got %#v", details["recorded"])
	}
}

func TestExecuteThinkValidation(t *testing.T) {
	var zero sdk.Context
	if _, err := executeThink(zero, map[string]any{}); err == nil {
		t.Fatal("expected error for missing thoughts")
	}
	if _, err := executeThink(zero, map[string]any{"thoughts": 123}); err == nil {
		t.Fatal("expected error for non-string thoughts")
	}
	if _, err := executeThink(zero, map[string]any{"thoughts": "ok", "i": "should reject"}); err == nil {
		t.Fatal("expected error for unexpected property (intent omitted, additionalProperties: false)")
	}
	out, err := executeThink(zero, map[string]any{"thoughts": "reasoning privately"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, ok := out.(*sdk.ToolResult)
	if !ok || res.Content != "------" {
		t.Fatalf("unexpected result: %#v", out)
	}
}

func TestRegisterDefaultActiveFalseAndOmitIntent(t *testing.T) {
	saveAndRestoreHooks(t)
	toolcmd.ResetForTests()
	t.Cleanup(toolcmd.ResetForTests)

	ext := sdk.New("think-test")
	Register(ext)

	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()

	// Read length-prefixed JSON register frame.
	var lenBuf [4]byte
	if _, err := io.ReadFull(hostSide, lenBuf[:]); err != nil {
		t.Fatalf("read frame length: %v", err)
	}
	frameLen := binary.BigEndian.Uint32(lenBuf[:])
	payload := make([]byte, frameLen)
	if _, err := io.ReadFull(hostSide, payload); err != nil {
		t.Fatalf("read frame body: %v", err)
	}
	_ = hostSide.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	var frame struct {
		Type     string `json:"type"`
		Register *struct {
			Tools []struct {
				Name          string         `json:"name"`
				Description   string         `json:"description"`
				DefaultActive *bool          `json:"default_active"`
				Parameters    map[string]any `json:"parameters"`
			} `json:"tools"`
		} `json:"register"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("unmarshal register frame: %v", err)
	}
	if frame.Type != "register" || frame.Register == nil || len(frame.Register.Tools) != 1 {
		t.Fatalf("unexpected register frame: %+v", frame)
	}
	tl := frame.Register.Tools[0]
	if tl.Name != "think" {
		t.Fatalf("expected tool name think, got %q", tl.Name)
	}
	if tl.Description != "private scratchpad; not shown to user" {
		t.Fatalf("unexpected description: %q", tl.Description)
	}
	if tl.DefaultActive == nil || *tl.DefaultActive {
		t.Fatalf("expected DefaultActive=false, got %v", tl.DefaultActive)
	}
	props, _ := tl.Parameters["properties"].(map[string]any)
	if _, hasI := props["i"]; hasI {
		t.Fatal("think tool must NOT inject intent field `i`")
	}
	if _, hasThoughts := props["thoughts"]; !hasThoughts {
		t.Fatal("think tool missing `thoughts` property")
	}
	if addProps, ok := tl.Parameters["additionalProperties"].(bool); !ok || addProps {
		t.Fatalf("expected additionalProperties=false, got %#v", tl.Parameters["additionalProperties"])
	}
}
