package extension_equivalence_test

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The fake-host template is what every port's layer-1 tests stand on, so its wire shapes are
// checked here against the SDK: a wrong shape silently produces "unknown command" (found by
// pigpen-pig-snake, whose Host.Command failed on a registered command).
func TestFakeHostRunsARegisteredCommandWithItsArguments(t *testing.T) {
	var got []string
	e := sdk.New("selftest")
	e.Command("greet", "says hello", func(ctx sdk.Context, args string) error {
		got = append(got, args)
		return nil
	})
	h := StartHost(t, e, HostOptions{})
	if failure := h.Command("greet", "to the herd"); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	if len(got) != 1 || got[0] != "to the herd" {
		t.Errorf("handler args = %q", got)
	}
}

func TestCustomResultIsTheShapeTheSDKReadsFromUICustom(t *testing.T) {
	got := CustomResult(map[string]any{"score": 3})
	if got["ok"] != true || got["result"].(map[string]any)["score"] != 3 || len(got) != 2 {
		t.Errorf("CustomResult = %v", got)
	}
}

// pigpen-jev: an extension that calls ctx.ModelRegistry().Complete or Stream sends the `modelStream`
// host call and reads `model_stream_event` notifications that the host delivers before the call
// result. Each port used to hand-write that plumbing; the template now scripts it.
func TestFakeHostScriptsAModelStream(t *testing.T) {
	var sent []map[string]any
	var final map[string]any
	e := sdk.New("selftest")
	e.Command("ask", "asks the model", func(ctx sdk.Context, args string) error {
		final = ctx.ModelRegistry().Complete(map[string]any{"provider": "p", "id": "m"}, map[string]any{"messages": []any{args}}, nil)
		return nil
	})
	h := StartHost(t, e, HostOptions{ModelStream: func(model, request map[string]any) []map[string]any {
		sent = append(sent, request)
		return ModelText("hello from the script")
	}})
	if failure := h.Command("ask", "hi"); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	if len(sent) != 1 || sent[0]["messages"].([]any)[0] != "hi" {
		t.Errorf("request seen by the script = %v", sent)
	}
	msg, _ := final["content"].([]any)
	if final["stopReason"] != "stop" || len(msg) != 1 || msg[0].(map[string]any)["text"] != "hello from the script" {
		t.Errorf("Complete returned %v", final)
	}
	if got := h.CallsTo("modelStream"); len(got) != 1 {
		t.Errorf("modelStream calls = %v", got)
	}
}

func TestFakeHostModelStreamCanFail(t *testing.T) {
	var final map[string]any
	e := sdk.New("selftest")
	e.Command("ask", "asks the model", func(ctx sdk.Context, args string) error {
		final = ctx.ModelRegistry().Complete(map[string]any{"provider": "p", "id": "m"}, map[string]any{}, nil)
		return nil
	})
	h := StartHost(t, e, HostOptions{ModelStream: func(model, request map[string]any) []map[string]any {
		return ModelError("provider down")
	}})
	if failure := h.Command("ask", ""); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	if final["stopReason"] != "error" || final["errorMessage"] != "provider down" {
		t.Errorf("Complete returned %v", final)
	}
}

// pigpen-websearch: OnCall returns only a map, so a host answer that is an array or a string
// (`sessionRead` getBranch, getCwd) could not be scripted and session-restore wire tests were
// blocked. OnCallValue answers any JSON value.
func TestFakeHostAnswersHostCallsWithArraysAndStrings(t *testing.T) {
	var branch []map[string]any
	var cwd string
	e := sdk.New("selftest")
	e.Command("restore", "reads the session", func(ctx sdk.Context, args string) error {
		var err error
		if branch, err = ctx.SessionManager().GetBranch(nil); err != nil {
			return err
		}
		cwd, err = ctx.SessionManager().GetCwd()
		return err
	})
	h := StartHost(t, e, HostOptions{OnCallValue: func(method string, args map[string]any) (any, string) {
		if method != "sessionRead" {
			return map[string]any{}, ""
		}
		switch args["method"] {
		case "getBranch":
			return []any{map[string]any{"id": "e1", "type": "message"}, map[string]any{"id": "e2", "type": "message"}}, ""
		case "getCwd":
			return "/work/dir", ""
		}
		return nil, "unexpected sessionRead " + args["method"].(string)
	}})
	if failure := h.Command("restore", ""); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	if len(branch) != 2 || branch[1]["id"] != "e2" || cwd != "/work/dir" {
		t.Errorf("branch %v cwd %q", branch, cwd)
	}
}
