package tunnel_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/tunnel"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// fakeResponse is one scripted devtunnel answer; a nil Status is a successful exit 0, like the
// upstream test's `status === undefined ? 0 : status`.
type fakeResponse struct {
	status *int
	signal string
	stdout string
	stderr string
}

func intp(i int) *int { return &i }

// stdout is a response that succeeds with the given output.
func stdout(s string) fakeResponse { return fakeResponse{stdout: s} }

// fails is a response that exits non-zero with the given stderr.
func fails(status int, stderr string) fakeResponse {
	return fakeResponse{status: intp(status), stderr: stderr}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// withFakeDevtunnel is upstream's helper of the same name: it feeds the scripted responses to a
// DevTunnelCli, records the argument lists, and checks every response was consumed.
func withFakeDevtunnel(t *testing.T, responses []fakeResponse, run func(cli *tunnel.CLI)) [][]string {
	t.Helper()
	var calls [][]string
	index := 0
	cli := tunnel.NewCLI(func(args []string) (tunnel.CommandResult, error) {
		if index >= len(responses) {
			return tunnel.CommandResult{}, fmt.Errorf("unexpected devtunnel call: %s", strings.Join(args, " "))
		}
		response := responses[index]
		index++
		calls = append(calls, append([]string(nil), args...))
		status := response.status
		if status == nil {
			status = intp(0)
		}
		return tunnel.CommandResult{Status: status, Signal: response.signal, Stdout: response.stdout, Stderr: response.stderr}, nil
	})
	run(cli)
	if index != len(responses) {
		t.Errorf("every fake devtunnel response should be consumed: used %d of %d", index, len(responses))
	}
	return calls
}

func throws(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error matching %q, got none", pattern)
	}
	if !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %q", err.Error(), pattern)
	}
}

func isTunnelError(err error) bool {
	var te *tunnel.Error
	return errors.As(err, &te)
}

var (
	tunnelListCall = []string{"list", "--all-labels", tunnel.IdentityLabel, "--all-labels", tunnel.LauncherLabel, "--json"}
	createPortCall = []string{"port", "create", "sample.usw2", "-p", "31546", "--protocol", "http", "--json"}
	listPortsCall  = []string{"port", "list", "sample.usw2", "--json"}
	showPortCall   = []string{"port", "show", "sample.usw2", "-p", "31546", "--json"}
	wrongPort      = map[string]any{"portNumber": tunnel.Port, "protocol": "https"}
	wrongPortInfo  = map[string]any{"portNumber": tunnel.Port, "protocol": "https", "accessControl": []any{}}
)

// Twins of upstream test/tunnel.test.ts, "tunnel discovery contract".
func TestTunnelDiscoveryContract(t *testing.T) {
	twin.Run(t, "tunnel", "uses the shared discovery labels and port", func(t *testing.T) {
		if tunnel.LauncherLabel != "vscode-server-launcher" || tunnel.ProtocolLabel != "protocolv5" || tunnel.Port != 31546 {
			t.Fatalf("labels = %q %q port %d", tunnel.LauncherLabel, tunnel.ProtocolLabel, tunnel.Port)
		}
	})
	twin.Run(t, "tunnel", "keeps the identity label out of the display name", func(t *testing.T) {
		if !strings.HasPrefix(tunnel.IdentityLabel, "_") {
			t.Fatalf("identity label %q must start with an underscore", tunnel.IdentityLabel)
		}
		if got, ok := tunnel.DisplayLabel([]string{tunnel.LauncherLabel, tunnel.ProtocolLabel, tunnel.IdentityLabel}); ok {
			t.Fatalf("display label = %q, want none", got)
		}
		if got, ok := tunnel.DisplayLabel([]string{tunnel.LauncherLabel, tunnel.ProtocolLabel, tunnel.IdentityLabel, "devbox"}); !ok || got != "devbox" {
			t.Fatalf("display label = %q, %v", got, ok)
		}
		if got, ok := tunnel.DisplayLabel([]string{"banl", "protocolv5", tunnel.LauncherLabel, "_flag3"}); !ok || got != "banl" {
			t.Fatalf("display label = %q, %v", got, ok)
		}
	})
	twin.Run(t, "tunnel", "folds a name the way VS Code does", func(t *testing.T) {
		for _, c := range []struct {
			in, want string
			ok       bool
		}{
			{"my box", "mybox", true},
			{"--weird--", "weird--", true},
			{strings.Repeat("a", 40), strings.Repeat("a", 20), true},
			{"..", "", false},
		} {
			if got, ok := tunnel.NameLabel(c.in); got != c.want || ok != c.ok {
				t.Errorf("NameLabel(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
			}
		}
	})
}

// Twins of "devtunnel account and catalogue".
func TestDevtunnelAccountAndCatalogue(t *testing.T) {
	twin.Run(t, "tunnel", "reports when the CLI is not installed", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := tunnel.NewCLI(nil).RequireLogin()
		if err == nil || !isTunnelError(err) || !regexp.MustCompile("not found on PATH").MatchString(err.Error()) {
			t.Fatalf("error = %v", err)
		}
		want := strings.Join([]string{
			"`devtunnel` not found on PATH.",
			"",
			"Install it from https://aka.ms/devtunnels/download, then log in:",
			"",
			"    devtunnel user login -g      GitHub account",
			"    devtunnel user login         Microsoft account (the default)",
		}, "\n")
		if err.Error() != want {
			t.Fatalf("message = %q", err.Error())
		}
	})

	twin.Run(t, "tunnel", "requires a logged-in account", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{stdout(`{"status":"Logged in"}`)}, func(cli *tunnel.CLI) {
			if err := cli.RequireLogin(); err != nil {
				t.Fatal(err)
			}
		})
		if !reflect.DeepEqual(calls, [][]string{{"user", "show", "--json"}}) {
			t.Fatalf("calls = %v", calls)
		}
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"status":"Not logged in"}`)}, func(cli *tunnel.CLI) {
			throws(t, cli.RequireLogin(), "(?i)not logged in")
		})
	})

	twin.Run(t, "tunnel", "reports account inspection failures", func(t *testing.T) {
		for _, c := range []struct {
			response fakeResponse
			message  string
		}{
			{fails(2, "credential store failed"), "(?s)user show failed.*credential store failed"},
			{stdout("not json"), "(?s)user show returned invalid JSON"},
			{stdout("{}"), "(?s)user show returned an unexpected response"},
		} {
			withFakeDevtunnel(t, []fakeResponse{c.response}, func(cli *tunnel.CLI) { throws(t, cli.RequireLogin(), c.message) })
		}
	})

	twin.Run(t, "tunnel", "finds this host's first labelled tunnel", func(t *testing.T) {
		want := &tunnel.Tunnel{TunnelID: "sample.usw2", Labels: []string{tunnel.IdentityLabel, tunnel.LauncherLabel, "sample"}}
		calls := withFakeDevtunnel(t, []fakeResponse{stdout(jsonOf(t, map[string]any{"tunnels": []any{
			map[string]any{"tunnelId": "sample.usw2", "labels": want.Labels},
		}}))}, func(cli *tunnel.CLI) {
			got, err := cli.FindTunnel()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("tunnel = %+v, %v", got, err)
			}
		})
		if !reflect.DeepEqual(calls, [][]string{tunnelListCall}) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "returns no tunnel for a successful empty listing", func(t *testing.T) {
		for _, out := range []string{`{"warning":"No tunnels found."}`, `{"tunnels":[]}`} {
			withFakeDevtunnel(t, []fakeResponse{stdout(out)}, func(cli *tunnel.CLI) {
				got, err := cli.FindTunnel()
				if err != nil || got != nil {
					t.Fatalf("tunnel = %+v, %v", got, err)
				}
			})
		}
	})

	twin.Run(t, "tunnel", "reports tunnel listing failures and malformed responses", func(t *testing.T) {
		for _, c := range []struct {
			response fakeResponse
			message  string
		}{
			{fails(2, "listing failed"), "(?s)list failed.*listing failed"},
			{stdout("not json"), "(?s)list returned invalid JSON"},
			{stdout(`{"warning":"partial results"}`), "(?s)list returned an unexpected response"},
			{stdout(`{"tunnels":[{"tunnelId":42,"labels":[]}]}`), "(?s)list returned an unexpected response"},
			{stdout(`{"tunnels":[{"tunnelId":"other.usw2","labels":["unrelated"]}]}`), "(?s)list returned an unexpected response"},
		} {
			withFakeDevtunnel(t, []fakeResponse{c.response}, func(cli *tunnel.CLI) {
				_, err := cli.FindTunnel()
				throws(t, err, c.message)
			})
		}
	})

	twin.Run(t, "tunnel", "creates a tunnel with identity, protocol, launcher, and display labels", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{stdout(`{"tunnel":{"tunnelId":"sample.usw2"}}`)}, func(cli *tunnel.CLI) {
			id, err := cli.CreateTunnel("devbox")
			if err != nil || id != "sample.usw2" {
				t.Fatalf("id = %q, %v", id, err)
			}
		})
		want := [][]string{{"create", "--json", "-l", tunnel.LauncherLabel, "-l", tunnel.ProtocolLabel, "-l", tunnel.IdentityLabel, "-l", "devbox"}}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "rejects failed or malformed tunnel creation", func(t *testing.T) {
		for _, c := range []struct {
			response fakeResponse
			message  string
		}{
			{fails(1, "denied"), "(?s)create failed.*denied"},
			{stdout("not json"), "(?s)create returned invalid JSON"},
			{stdout("{}"), "(?s)create returned an unexpected response"},
		} {
			withFakeDevtunnel(t, []fakeResponse{c.response}, func(cli *tunnel.CLI) {
				_, err := cli.CreateTunnel("")
				throws(t, err, c.message)
			})
		}
	})

	twin.Run(t, "tunnel", "renames a tunnel without disturbing reserved labels", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{{}, {}}, func(cli *tunnel.CLI) {
			if err := cli.Rename("sample.usw2", "old", "new"); err != nil {
				t.Fatal(err)
			}
			if err := cli.Rename("sample.usw2", "", "first"); err != nil {
				t.Fatal(err)
			}
		})
		want := [][]string{
			{"update", "sample.usw2", "--add-labels", "new", "--remove-labels", "old"},
			{"update", "sample.usw2", "--add-labels", "first"},
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "reports a failed rename", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{fails(1, "denied")}, func(cli *tunnel.CLI) {
			throws(t, cli.Rename("sample.usw2", "old", "new"), "(?s)update failed.*denied")
		})
	})
}

// Twins of "devtunnel port".
func TestDevtunnelPort(t *testing.T) {
	twin.Run(t, "tunnel", "creates port 31546 when a successful listing is empty", func(t *testing.T) {
		for _, out := range []string{`{"ports":[]}`, `{"warning":"No ports found."}`} {
			calls := withFakeDevtunnel(t, []fakeResponse{stdout(out), {}}, func(cli *tunnel.CLI) {
				if err := cli.EnsurePort("sample.usw2"); err != nil {
					t.Fatal(err)
				}
			})
			if !reflect.DeepEqual(calls, [][]string{listPortsCall, createPortCall}) {
				t.Fatalf("calls = %v", calls)
			}
		}
	})

	twin.Run(t, "tunnel", "keeps an existing HTTP port", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{
			stdout(jsonOf(t, map[string]any{"ports": []any{map[string]any{"portNumber": tunnel.Port, "protocol": " HTTP "}}})),
		}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err != nil {
				t.Fatal(err)
			}
		})
		if !reflect.DeepEqual(calls, [][]string{listPortsCall}) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "replaces an old HTTPS port with an HTTP port", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{
			stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})),
			stdout(jsonOf(t, map[string]any{"port": wrongPortInfo})),
			{}, {},
		}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err != nil {
				t.Fatal(err)
			}
		})
		want := [][]string{listPortsCall, showPortCall, {"port", "delete", "sample.usw2", "-p", "31546", "--json"}, createPortCall}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "does not discard port-specific access control while migrating", func(t *testing.T) {
		details := map[string]any{"portNumber": tunnel.Port, "protocol": "https", "accessControl": []any{map[string]any{"subject": "example"}}}
		calls := withFakeDevtunnel(t, []fakeResponse{
			stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})),
			stdout(jsonOf(t, map[string]any{"port": details})),
		}, func(cli *tunnel.CLI) {
			throws(t, cli.EnsurePort("sample.usw2"), "has port-specific access control")
		})
		if !reflect.DeepEqual(calls, [][]string{listPortsCall, showPortCall}) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "uses current port details when the protocol changes during inspection", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{
			stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})),
			stdout(jsonOf(t, map[string]any{"port": map[string]any{"portNumber": tunnel.Port, "protocol": "http", "accessControl": []any{}}})),
		}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err != nil {
				t.Fatal(err)
			}
		})
		if !reflect.DeepEqual(calls, [][]string{listPortsCall, showPortCall}) {
			t.Fatalf("calls = %v", calls)
		}
	})

	twin.Run(t, "tunnel", "rejects malformed port inspection output", func(t *testing.T) {
		for _, responses := range [][]fakeResponse{
			{stdout("not json")},
			{stdout(`{"ports":[{"portNumber":31546}]}`)},
			{stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})), stdout(`{"port":{}}`)},
		} {
			withFakeDevtunnel(t, responses, func(cli *tunnel.CLI) {
				if err := cli.EnsurePort("sample.usw2"); err == nil || !isTunnelError(err) {
					t.Fatalf("error = %v, want a TunnelError", err)
				}
			})
		}
	})

	twin.Run(t, "tunnel", "reports listing, inspection, deletion, and creation failures", func(t *testing.T) {
		listWrong := stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}}))
		showWrong := stdout(jsonOf(t, map[string]any{"port": wrongPortInfo}))
		for _, c := range []struct {
			responses []fakeResponse
			message   string
		}{
			{[]fakeResponse{fails(1, "list failed")}, "port list failed"},
			{[]fakeResponse{listWrong, fails(1, "show failed")}, "port show failed"},
			{[]fakeResponse{listWrong, showWrong, fails(1, "delete failed")}, "port delete failed"},
			{[]fakeResponse{stdout(`{"ports":[]}`), fails(1, "create failed")}, "port create failed"},
		} {
			withFakeDevtunnel(t, c.responses, func(cli *tunnel.CLI) { throws(t, cli.EnsurePort("sample.usw2"), c.message) })
		}
	})
}

// Behaviour beyond upstream's twins: the exact wording of the refusals a user reads, and the
// decoders' edge cases.
func TestDevtunnelMessages(t *testing.T) {
	t.Run("not logged in names both login commands", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"status":"  NOT Logged In "}`)}, func(cli *tunnel.CLI) {
			err := cli.RequireLogin()
			want := strings.Join([]string{
				"`devtunnel` is not logged in. Run one of:",
				"",
				"    devtunnel user login -g      GitHub account",
				"    devtunnel user login         Microsoft account (the default)",
				"",
				"Add -d for device-code auth when there is no browser.",
			}, "\n")
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v", err)
			}
		})
	})
	t.Run("an unknown login status is an unexpected response", func(t *testing.T) {
		for _, out := range []string{`{"status":"Maybe"}`, `{"status":3}`, `[]`, `null`, `"x"`} {
			withFakeDevtunnel(t, []fakeResponse{stdout(out)}, func(cli *tunnel.CLI) {
				err := cli.RequireLogin()
				if err == nil || !isTunnelError(err) {
					t.Fatalf("%s: error = %v", out, err)
				}
			})
		}
	})
	t.Run("failure detail says how the process ended and what it printed", func(t *testing.T) {
		cases := []struct {
			response fakeResponse
			want     string
		}{
			{fakeResponse{status: intp(3), stderr: "  boom\n", stdout: " out "}, "devtunnel user show failed:\nexit status 3\nboom\nout"},
			{fakeResponse{status: intp(1)}, "devtunnel user show failed:\nexit status 1"},
			{fakeResponse{status: nil, signal: "SIGTERM", stderr: "x"}, "devtunnel user show failed:\nsignal SIGTERM\nx"},
		}
		for _, c := range cases {
			responses := []fakeResponse{c.response}
			index := 0
			cli := tunnel.NewCLI(func([]string) (tunnel.CommandResult, error) {
				r := responses[index]
				index++
				res := tunnel.CommandResult{Status: r.status, Signal: r.signal, Stdout: r.stdout, Stderr: r.stderr}
				return res, nil
			})
			if err := cli.RequireLogin(); err == nil || err.Error() != c.want {
				t.Errorf("error = %v, want %q", err, c.want)
			}
		}
		// A process that ended with neither status nor signal.
		cli := tunnel.NewCLI(func([]string) (tunnel.CommandResult, error) { return tunnel.CommandResult{}, nil })
		if err := cli.RequireLogin(); err == nil || err.Error() != "devtunnel user show failed:\nno exit status" {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("an empty body is named as such", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{stdout("  \n")}, func(cli *tunnel.CLI) {
			err := cli.RequireLogin()
			if err == nil || err.Error() != "devtunnel user show returned invalid JSON:\n<empty output>" {
				t.Fatalf("error = %v", err)
			}
		})
	})
	t.Run("a JSON value that is not an object is unexpected", func(t *testing.T) {
		for _, out := range []string{"[]", "null", "3", `"s"`, "true"} {
			withFakeDevtunnel(t, []fakeResponse{stdout(out)}, func(cli *tunnel.CLI) {
				err := cli.RequireLogin()
				want := "devtunnel user show returned an unexpected JSON value:\n" + out
				if err == nil || err.Error() != want {
					t.Fatalf("%s: error = %v", out, err)
				}
			})
		}
	})
	t.Run("a tunnel must name its id and carry both reserved labels", func(t *testing.T) {
		for _, out := range []string{
			`{"tunnels":[{"tunnelId":"","labels":[]}]}`,
			`{"tunnels":[{"tunnelId":"a.b"}]}`,
			`{"tunnels":[{"tunnelId":"a.b","labels":[1]}]}`,
			`{"tunnels":["x"]}`,
			`{"tunnels":[null]}`,
			`{"tunnels":[{"tunnelId":"a.b","labels":["_pi_ahp"]}]}`,
			`{"tunnels":[{"tunnelId":"a.b","labels":["vscode-server-launcher"]}]}`,
			`{"warning":3}`,
			`{"warning":"nothing about tunnels"}`,
		} {
			withFakeDevtunnel(t, []fakeResponse{stdout(out)}, func(cli *tunnel.CLI) {
				if _, err := cli.FindTunnel(); err == nil || !strings.Contains(err.Error(), "devtunnel list returned an unexpected response:\n"+out) {
					t.Fatalf("%s: error = %v", out, err)
				}
			})
		}
	})
	t.Run("only the first tunnel is used and later ones are not checked", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"tunnels":[
			{"tunnelId":"a.b","labels":["_pi_ahp","vscode-server-launcher"]},
			{"tunnelId":"c.d","labels":[]}]}`)}, func(cli *tunnel.CLI) {
			got, err := cli.FindTunnel()
			if err != nil || got == nil || got.TunnelID != "a.b" {
				t.Fatalf("tunnel = %+v, %v", got, err)
			}
		})
	})
	t.Run("warnings match the CLI's wording only", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"warning":"There are no tunnel found here"}`)}, func(cli *tunnel.CLI) {
			if got, err := cli.FindTunnel(); err != nil || got != nil {
				t.Fatalf("tunnel = %+v, %v", got, err)
			}
		})
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"warning":"no tunnels foundation"}`)}, func(cli *tunnel.CLI) {
			if _, err := cli.FindTunnel(); err == nil {
				t.Fatal("a word that merely starts with 'found' is not the empty-listing warning")
			}
		})
		withFakeDevtunnel(t, []fakeResponse{stdout(`{"warning":"No tunnel ports found"}`), {}}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("a created tunnel without a name carries only the reserved labels", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{stdout(`{"tunnel":{"tunnelId":"x.y"}}`)}, func(cli *tunnel.CLI) {
			if _, err := cli.CreateTunnel(""); err != nil {
				t.Fatal(err)
			}
		})
		want := [][]string{{"create", "--json", "-l", tunnel.LauncherLabel, "-l", tunnel.ProtocolLabel, "-l", tunnel.IdentityLabel}}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("a created tunnel needs a non-empty id", func(t *testing.T) {
		for _, out := range []string{`{"tunnel":null}`, `{"tunnel":[]}`, `{"tunnel":{"tunnelId":""}}`, `{"tunnel":{"tunnelId":3}}`, `{"tunnel":"x"}`} {
			withFakeDevtunnel(t, []fakeResponse{stdout(out)}, func(cli *tunnel.CLI) {
				if _, err := cli.CreateTunnel(""); err == nil || !strings.Contains(err.Error(), "devtunnel create returned an unexpected response") {
					t.Fatalf("%s: error = %v", out, err)
				}
			})
		}
	})
	t.Run("port numbers must be integers in range and a protocol must be named", func(t *testing.T) {
		for _, port := range []string{
			`{"portNumber":0,"protocol":"http"}`, `{"portNumber":65536,"protocol":"http"}`, `{"portNumber":1.5,"protocol":"http"}`,
			`{"portNumber":"1","protocol":"http"}`, `{"portNumber":80,"protocol":"  "}`, `{"portNumber":80,"protocol":3}`,
			`{"portNumber":80}`, `[]`, `null`, `"p"`,
		} {
			withFakeDevtunnel(t, []fakeResponse{stdout(`{"ports":[` + port + `]}`)}, func(cli *tunnel.CLI) {
				if err := cli.EnsurePort("sample.usw2"); err == nil || !strings.Contains(err.Error(), "devtunnel port list returned an unexpected response") {
					t.Fatalf("%s: error = %v", port, err)
				}
			})
		}
	})
	t.Run("another port is left alone and the fixed port is created", func(t *testing.T) {
		calls := withFakeDevtunnel(t, []fakeResponse{stdout(`{"ports":[{"portNumber":8080,"protocol":"https"}]}`), {}}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err != nil {
				t.Fatal(err)
			}
		})
		if !reflect.DeepEqual(calls, [][]string{listPortsCall, createPortCall}) {
			t.Fatalf("calls = %v", calls)
		}
	})
	t.Run("a shown port for another number is an unexpected response", func(t *testing.T) {
		withFakeDevtunnel(t, []fakeResponse{
			stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})),
			stdout(`{"port":{"portNumber":8080,"protocol":"https"}}`),
		}, func(cli *tunnel.CLI) {
			if err := cli.EnsurePort("sample.usw2"); err == nil || !strings.Contains(err.Error(), "devtunnel port show returned an unexpected response") {
				t.Fatalf("error = %v", err)
			}
		})
	})
	t.Run("port-specific access control of any non-null shape blocks the migration", func(t *testing.T) {
		for _, acl := range []string{`[{"a":1}]`, `{"entries":[]}`, `"anonymous"`, `0`, `false`} {
			withFakeDevtunnel(t, []fakeResponse{
				stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})),
				stdout(`{"port":{"portNumber":31546,"protocol":"https","accessControl":` + acl + `}}`),
			}, func(cli *tunnel.CLI) {
				want := "devtunnel port 31546 uses https and has port-specific access control; replace it manually"
				if err := cli.EnsurePort("sample.usw2"); err == nil || err.Error() != want {
					t.Fatalf("%s: error = %v", acl, err)
				}
			})
		}
		for _, acl := range []string{`[]`, `null`, ``} {
			body := `{"port":{"portNumber":31546,"protocol":"https"` + map[bool]string{true: `,"accessControl":` + acl, false: ""}[acl != ""] + `}}`
			calls := withFakeDevtunnel(t, []fakeResponse{stdout(jsonOf(t, map[string]any{"ports": []any{wrongPort}})), stdout(body), {}, {}}, func(cli *tunnel.CLI) {
				if err := cli.EnsurePort("sample.usw2"); err != nil {
					t.Fatalf("%q: %v", acl, err)
				}
			})
			if len(calls) != 4 {
				t.Fatalf("%q: calls = %v", acl, calls)
			}
		}
	})
	t.Run("a runner failure is the runner's own error", func(t *testing.T) {
		boom := errors.New("spawn failed")
		cli := tunnel.NewCLI(func([]string) (tunnel.CommandResult, error) { return tunnel.CommandResult{}, boom })
		if err := cli.RequireLogin(); !errors.Is(err, boom) {
			t.Fatalf("error = %v", err)
		}
	})
}
