package kagentpolicy_test

import (
	"strings"
	"testing"

	kp "github.com/MichaelKinsy/pigpen/kagent-policy"
)

func parse(policy string) (kp.Policy, error) {
	return kp.ParsePolicy([]byte(policy), testEnv(headerEnv))
}

func TestParsePolicyValid(t *testing.T) {
	p, err := parse(probePolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AskUser || len(p.Servers) != 1 {
		t.Fatalf("policy = %+v", p)
	}
	s := p.Servers[0]
	if s.Name != "kagent-tools" || s.URL != "https://tools.kagent.svc.cluster.local/mcp" || s.TimeoutSeconds != 60 || !s.RequireApproval {
		t.Fatalf("server = %+v", s)
	}
	if len(s.Headers) != 1 || s.Headers[0] != (kp.Header{Name: "Authorization", Var: "KAGENT_PIG_HDR_1"}) {
		t.Fatalf("headers = %+v", s.Headers)
	}
	if len(s.Tools) != 1 || s.Tools[0] != "kagent_probe" {
		t.Fatalf("tools = %v", s.Tools)
	}
}

func TestParsePolicyLongestServerName(t *testing.T) {
	if _, err := parse(`{"mcp_servers":[{"name":"` + strings.Repeat("a", 40) + `","url":"https://a.example/mcp"}]}`); err != nil {
		t.Fatal(err)
	}
}

func TestParsePolicyDefaults(t *testing.T) {
	p, err := parse(`{"mcp_servers":[{"name":"a","url":"http://a.example:8080/mcp"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Servers[0]
	if p.AskUser || s.RequireApproval || len(s.Tools) != 0 || len(s.Headers) != 0 || s.TimeoutSeconds != 0 {
		t.Fatalf("defaults are not all off: %+v %+v", p, s)
	}
	if p, err := parse(`{}`); err != nil || len(p.Servers) != 0 || p.AskUser {
		t.Fatalf("empty policy: %+v %v", p, err)
	}
}

func TestParsePolicyRejects(t *testing.T) {
	server := func(fields string) string {
		return `{"mcp_servers":[{"name":"a","url":"https://a.example/mcp"` + fields + `}]}`
	}
	cases := []struct{ name, policy, want string }{
		{"empty file", ``, "EOF"},
		{"not json", `{`, "policy:"},
		{"array", `[]`, "policy:"},
		{"trailing document", `{} {}`, "after the JSON document"},
		{"unknown top-level key", `{"mcp_servers":[],"ask_users":true}`, "unknown field"},
		{"unknown server key", server(`,"require_aproval":true`), "unknown field"},
		{"duplicate top-level key", `{"ask_user":false,"ask_user":true}`, `"ask_user" twice`},
		{"duplicate server key", server(`,"require_approval":false,"require_approval":true`), `"require_approval" twice`},
		{"case-variant top-level key", `{"ask_user":false,"ASK_USER":true}`, `"ASK_USER", which differs from "ask_user" only in case`},
		{"case-variant list key", `{"Mcp_Servers":[]}`, `"Mcp_Servers"`},
		{"case-variant server key", server(`,"require_approval":true,"REQUIRE_APPROVAL":false`), `"REQUIRE_APPROVAL", which differs from "require_approval" only in case`},
		{"case-variant server name key", `{"mcp_servers":[{"Name":"a","url":"https://a.example/mcp"}]}`, `"Name"`},
		{"double underscore name", `{"mcp_servers":[{"name":"a__b","url":"https://a.example/mcp"}]}`, "name is required"},
		{"trailing underscore name", `{"mcp_servers":[{"name":"a_","url":"https://a.example/mcp"}]}`, "name is required"},
		{"leading hyphen name", `{"mcp_servers":[{"name":"-a","url":"https://a.example/mcp"}]}`, "name is required"},
		{"mixed double separator name", `{"mcp_servers":[{"name":"a_-b","url":"https://a.example/mcp"}]}`, "name is required"},
		{"null approval", server(`,"require_approval":null`), "must not be null"},
		{"null ask_user", `{"ask_user":null}`, "must not be null"},
		{"null servers", `{"mcp_servers":null}`, "must not be null"},
		{"null tools", server(`,"tools":null`), "must not be null"},
		{"string approval", server(`,"require_approval":"true"`), "policy:"},
		{"string ask_user", `{"ask_user":"yes"}`, "policy:"},
		{"missing name", `{"mcp_servers":[{"url":"https://a.example/mcp"}]}`, "name is required"},
		{"name over 40 characters", `{"mcp_servers":[{"name":"` + strings.Repeat("a", 41) + `","url":"https://a.example/mcp"}]}`, "name is required"},
		{"bad name", `{"mcp_servers":[{"name":"a b","url":"https://a.example/mcp"}]}`, "name is required"},
		{"missing url", `{"mcp_servers":[{"name":"a"}]}`, "url is required"},
		{"file url", `{"mcp_servers":[{"name":"a","url":"file:///etc/passwd"}]}`, "http or https"},
		{"no host", `{"mcp_servers":[{"name":"a","url":"https:///mcp"}]}`, "must name a host"},
		{"credentials in url", `{"mcp_servers":[{"name":"a","url":"https://user:pw@a.example/mcp"}]}`, "must not carry credentials"},
		{"env in url", `{"mcp_servers":[{"name":"a","url":"https://a.example/${KAGENT_PIG_HDR_1}"}]}`, "must not contain $"},
		{"shell url", `{"mcp_servers":[{"name":"a","url":"!echo https://a.example"}]}`, "policy:"},
		{"space in url", `{"mcp_servers":[{"name":"a","url":"https://a.example/m cp"}]}`, "whitespace"},
		{"colliding names", `{"mcp_servers":[{"name":"a-b","url":"https://a.example"},{"name":"a_b","url":"https://b.example"}]}`, "collides"},
		{"same name twice", `{"mcp_servers":[{"name":"a","url":"https://a.example"},{"name":"a","url":"https://b.example"}]}`, "collides"},
		{"shell header", server(`,"headers":{"X":"!echo pwned"}`), "single ${VAR}"},
		{"expanding header", server(`,"headers":{"X":"plain $HOME"}`), "single ${VAR}"},
		{"bare dollar header", server(`,"headers":{"X":"$KAGENT_PIG_HDR_1"}`), "single ${VAR}"},
		{"prefixed header", server(`,"headers":{"X":"Bearer ${KAGENT_PIG_HDR_1}"}`), "single ${VAR}"},
		{"bare name header", server(`,"headers":{"X":"KAGENT_PIG_HDR_1"}`), "single ${VAR}"},
		{"unset header variable", server(`,"headers":{"X":"${KAGENT_PIG_HDR_9}"}`), "KAGENT_PIG_HDR_9, which is not set"},
		{"bad header name", server(`,"headers":{"X Y":"${KAGENT_PIG_HDR_1}"}`), "invalid header name"},
		{"header names differing in case", server(`,"headers":{"X-A":"${KAGENT_PIG_HDR_1}","x-a":"${KAGENT_PIG_HDR_1}"}`), "same header"},
		{"zero timeout", server(`,"timeout_seconds":0`), "timeout_seconds"},
		{"negative timeout", server(`,"timeout_seconds":-1`), "timeout_seconds"},
		{"huge timeout", server(`,"timeout_seconds":100000`), "timeout_seconds"},
		{"wildcard tool", server(`,"tools":["*"]`), "must not contain *"},
		{"pattern tool", server(`,"tools":["read_*"]`), "must not contain *"},
		{"empty tool", server(`,"tools":[""]`), "1 to"},
		{"duplicate tool", server(`,"tools":["a","a"]`), "twice"},
		{"non-string tool", server(`,"tools":[1]`), "policy:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(tc.policy)
			if err == nil {
				t.Fatalf("policy %s was accepted", tc.policy)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestParsePolicyLimits(t *testing.T) {
	var tools []string
	for i := range 257 {
		tools = append(tools, `"t`+strings.Repeat("x", i%5)+string(rune('a'+i%26))+strings.Repeat("y", i/26)+`"`)
	}
	if _, err := parse(`{"mcp_servers":[{"name":"a","url":"https://a.example","tools":[` + strings.Join(tools, ",") + `]}]}`); err == nil || !strings.Contains(err.Error(), "limit is 256") {
		t.Fatalf("257 tools: %v", err)
	}
	var servers []string
	for i := range 65 {
		servers = append(servers, `{"name":"s`+strings.Repeat("a", i)+`","url":"https://a.example"}`)
	}
	if _, err := parse(`{"mcp_servers":[` + strings.Join(servers, ",") + `]}`); err == nil || !strings.Contains(err.Error(), "limit is 64") {
		t.Fatalf("65 servers: %v", err)
	}
}

// The error text never repeats a header value: it may be a secret.
func TestParsePolicyErrorsDoNotEchoHeaderValues(t *testing.T) {
	_, err := parse(`{"mcp_servers":[{"name":"a","url":"https://a.example","headers":{"Authorization":"Bearer hunter2-secret"}}]}`)
	if err == nil {
		t.Fatal("accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error repeats the header value: %v", err)
	}
}

func TestParsePolicyHeaderNamesAreData(t *testing.T) {
	p, err := parse(`{"mcp_servers":[{"name":"a","url":"https://a.example/mcp","headers":{"Require_Approval":"${KAGENT_PIG_HDR_1}","NAME":"${KAGENT_PIG_HDR_1}"}}]}`)
	if err != nil || len(p.Servers[0].Headers) != 2 {
		t.Fatalf("header names that look like schema keys: %+v %v", p, err)
	}
}

func TestParsePolicyAcceptsSingleSeparatorNames(t *testing.T) {
	for _, name := range []string{"a", "a-b", "a_b", "a1", "kagent-tools"} {
		if _, err := parse(`{"mcp_servers":[{"name":"` + name + `","url":"https://a.example/mcp"}]}`); err != nil {
			t.Fatalf("name %q rejected: %v", name, err)
		}
	}
}
