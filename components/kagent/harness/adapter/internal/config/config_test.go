package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func envOf(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { v, ok := values[name]; return v, ok }
}

var allKeys = map[string]string{
	"OPENAI_API_KEY": CredentialPlaceholder, "ANTHROPIC_API_KEY": CredentialPlaceholder, "AZURE_OPENAI_API_KEY": CredentialPlaceholder,
	"GOOGLE_API_KEY": CredentialPlaceholder, "MISTRAL_API_KEY": CredentialPlaceholder, "AWS_BEARER_TOKEN_BEDROCK": CredentialPlaceholder,
}

func document(model, agentExtra, runtime string) []byte {
	if agentExtra != "" {
		agentExtra = "," + agentExtra
	}
	if runtime != "" {
		runtime = `,"runtime":` + runtime
	}
	return []byte(fmt.Sprintf(`{"agent":{"name":"a","instruction":"be brief","model":%s%s}%s}`, model, agentExtra, runtime))
}

func mustParse(t *testing.T, raw []byte, env map[string]string) *Compiled {
	t.Helper()
	compiled, err := Parse(raw, envOf(env), Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return compiled
}

type modelsDoc struct {
	Providers map[string]struct {
		BaseURL string            `json:"baseUrl"`
		APIKey  string            `json:"apiKey"`
		Headers map[string]string `json:"headers"`
		Models  []struct {
			ID             string         `json:"id"`
			API            string         `json:"api"`
			Reasoning      bool           `json:"reasoning"`
			ContextWindow  int            `json:"contextWindow"`
			MaxTokens      int            `json:"maxTokens"`
			SamplingParams map[string]any `json:"samplingParams"`
		} `json:"models"`
	} `json:"providers"`
}

func decodeModels(t *testing.T, c *Compiled) modelsDoc {
	t.Helper()
	var doc modelsDoc
	if err := json.Unmarshal(c.ModelsJSON, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func envValue(c *Compiled, name string) (string, bool) {
	for _, v := range c.Env {
		if v.Name == name {
			return v.Value, true
		}
	}
	return "", false
}

func TestProviderMapping(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		provider  string
		api       string
		baseURL   string
		apiKey    string
		maxTokens int
		sampling  map[string]any
		wantEnv   map[string]string
		headers   map[string]string // expected header values as references (checked by key presence)
		thinking  string
		warns     []string
	}{
		{name: "openai chat", model: `{"model":"gpt-x","openai":{"base_url":"https://gw.example/v1","temperature":0.5,"top_p":0.9,"frequency_penalty":0.1,"presence_penalty":0.2,"seed":7,"max_completion_tokens":2048}}`,
			provider: "openai", api: "openai-completions", baseURL: "https://gw.example/v1", apiKey: "${OPENAI_API_KEY}", maxTokens: 2048,
			sampling: map[string]any{"temperature": 0.5, "top_p": 0.9, "frequency_penalty": 0.1, "presence_penalty": 0.2, "seed": float64(7)},
			wantEnv:  map[string]string{"OPENAI_API_KEY": CredentialPlaceholder}},
		{name: "openai responses drops unsupported sampling with warnings", model: `{"model":"gpt-x","openai":{"api_format":"responses","temperature":0.3,"frequency_penalty":0.1,"seed":3}}`,
			provider: "openai", api: "openai-responses", apiKey: "${OPENAI_API_KEY}", maxTokens: DefaultMaxTokens,
			sampling: map[string]any{"temperature": 0.3}, warns: []string{"frequency_penalty", "seed"}},
		{name: "openai reasoning", model: `{"model":"o9","openai":{"reasoning_effort":"high"}}`,
			provider: "openai", api: "openai-completions", apiKey: "${OPENAI_API_KEY}", maxTokens: DefaultMaxTokens, thinking: "high"},
		{name: "anthropic", model: `{"model":"claude-x","anthropic":{"max_tokens":4000,"temperature":0.2,"top_k":5,"prompt_caching":true}}`,
			provider: "anthropic", api: "anthropic-messages", apiKey: "${ANTHROPIC_API_KEY}", maxTokens: 4000, warns: []string{"anthropic.temperature", "anthropic.top_k", "prompt_caching"}},
		{name: "azure responses", model: `{"model":"gpt-x","azure_openai":{"endpoint":"https://r.openai.azure.com","deployment":"dep","api_version":"2025-01-01","api_format":"responses"}}`,
			provider: "azure", api: "azure-openai-responses", baseURL: "https://r.openai.azure.com", apiKey: "${AZURE_OPENAI_API_KEY}", maxTokens: DefaultMaxTokens,
			wantEnv: map[string]string{"AZURE_OPENAI_API_VERSION": "2025-01-01", "AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-x=dep"}},
		{name: "azure chat needs api-key header", model: `{"model":"gpt-x","azure_openai":{"endpoint":"https://r.openai.azure.com","deployment":"gpt-x"}}`,
			provider: "azure", api: "openai-completions", baseURL: "https://r.openai.azure.com", apiKey: "${AZURE_OPENAI_API_KEY}", maxTokens: DefaultMaxTokens,
			headers: map[string]string{"api-key": "${AZURE_OPENAI_API_KEY}"}},
		{name: "mistral", model: `{"model":"m","mistral":{"base_url":"https://m.example","max_tokens":999}}`,
			provider: "mistral", api: "mistral-conversations", baseURL: "https://m.example", apiKey: "${MISTRAL_API_KEY}", maxTokens: 999},
		{name: "gemini translates GOOGLE_API_KEY", model: `{"model":"gem","gemini":{"max_output_tokens":1234}}`,
			provider: "google", api: "google-generative-ai", apiKey: "${GEMINI_API_KEY}", maxTokens: 1234,
			wantEnv: map[string]string{"GEMINI_API_KEY": CredentialPlaceholder}},
		{name: "bedrock", model: `{"model":"anthropic.claude","bedrock":{"region":"eu-west-1","prompt_caching":true}}`,
			provider: "amazon-bedrock", api: "bedrock-converse-stream", maxTokens: DefaultMaxTokens,
			wantEnv: map[string]string{"AWS_REGION": "eu-west-1", "AWS_BEARER_TOKEN_BEDROCK": CredentialPlaceholder}, warns: []string{"prompt_caching"}},
		{name: "ollama local", model: `{"model":"llama3:8b","ollama":{"options":{"num_ctx":"4096"}}}`,
			provider: "ollama", api: "openai-completions", baseURL: "http://localhost:11434/v1", apiKey: "ollama", maxTokens: DefaultMaxTokens, warns: []string{"ollama.options"}},
		{name: "foundry openai", model: `{"model":"gpt-f","foundry":{"endpoint":"https://x.ai.azure.com","api_format":"openai"}}`,
			provider: "azure", api: "openai-completions", baseURL: "https://x.ai.azure.com", apiKey: "${AZURE_OPENAI_API_KEY}", maxTokens: DefaultMaxTokens,
			headers: map[string]string{"api-key": "${AZURE_OPENAI_API_KEY}"}, warns: []string{"model.foundry is mapped from PiG's documentation only"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"OLLAMA_API_BASE": ""}
			for k, v := range allKeys {
				env[k] = v
			}
			c := mustParse(t, document(tc.model, "", ""), env)
			doc := decodeModels(t, c)
			p, ok := doc.Providers[tc.provider]
			if !ok || len(doc.Providers) != 1 || len(p.Models) != 1 {
				t.Fatalf("providers = %s", c.ModelsJSON)
			}
			m := p.Models[0]
			if c.Provider != tc.provider || c.Model != m.ID || m.API != tc.api || p.BaseURL != tc.baseURL || p.APIKey != tc.apiKey {
				t.Errorf("mapping = provider %q model %q api %q baseUrl %q key %q", c.Provider, c.Model, m.API, p.BaseURL, p.APIKey)
			}
			if m.ContextWindow != DefaultContextWindow || m.MaxTokens != tc.maxTokens {
				t.Errorf("limits = %d/%d, want %d/%d", m.ContextWindow, m.MaxTokens, DefaultContextWindow, tc.maxTokens)
			}
			if fmt.Sprint(m.SamplingParams) != fmt.Sprint(tc.sampling) && !(len(m.SamplingParams) == 0 && len(tc.sampling) == 0) {
				t.Errorf("samplingParams = %v, want %v", m.SamplingParams, tc.sampling)
			}
			for name, want := range tc.wantEnv {
				if got, ok := envValue(c, name); !ok || got != want {
					t.Errorf("env %s = %q,%v want %q", name, got, ok, want)
				}
			}
			for name, want := range tc.headers {
				if p.Headers[name] != want {
					t.Errorf("header %s = %q, want %q", name, p.Headers[name], want)
				}
			}
			if c.Thinking != tc.thinking || m.Reasoning != (tc.thinking != "") {
				t.Errorf("thinking = %q reasoning = %v", c.Thinking, m.Reasoning)
			}
			for _, want := range tc.warns {
				if !slices.ContainsFunc(c.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
					t.Errorf("warnings %q lack %q", c.Warnings, want)
				}
			}
			if len(tc.warns) == 0 && len(c.Warnings) != 0 {
				t.Errorf("unexpected warnings %q", c.Warnings)
			}
		})
	}
}

func TestGeminiAcceptsGeminiAPIKeyToo(t *testing.T) {
	c := mustParse(t, document(`{"model":"g","gemini":{}}`, "", ""), map[string]string{"GEMINI_API_KEY": "k"})
	if v, _ := envValue(c, "GEMINI_API_KEY"); v != "k" {
		t.Fatalf("GEMINI_API_KEY = %q", v)
	}
}

func TestOllamaUsesBaseAndKeyFromEnvironment(t *testing.T) {
	c := mustParse(t, document(`{"model":"llama3","ollama":{}}`, "", ""), map[string]string{"OLLAMA_API_BASE": "http://ollama.internal:11434/", "OLLAMA_API_KEY": CredentialPlaceholder})
	p := decodeModels(t, c).Providers["ollama"]
	if p.BaseURL != "http://ollama.internal:11434/v1" || p.APIKey != "${OLLAMA_API_KEY}" {
		t.Fatalf("ollama = %+v", p)
	}
}

func TestLimitsComeFromOptionsWhenKagentGivesNone(t *testing.T) {
	c, err := Parse(document(`{"model":"gpt-x","openai":{}}`, "", ""), envOf(allKeys), Options{ContextWindow: 1000000, MaxTokens: 32000})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeModels(t, c).Providers["openai"].Models[0]
	if m.ContextWindow != 1000000 || m.MaxTokens != 32000 {
		t.Fatalf("limits = %d/%d", m.ContextWindow, m.MaxTokens)
	}
}

func TestRejections(t *testing.T) {
	openai := `{"model":"gpt-x","openai":{}}`
	tests := []struct {
		name   string
		raw    []byte
		fields []string
	}{
		{"sse_tools", document(openai, `"sse_tools":[{"params":{"url":"https://a.example/sse"}}]`, ""), []string{"agent.sse_tools"}},
		{"stdio_tools", document(openai, `"stdio_tools":[{"command":"ls"}]`, ""), []string{"agent.stdio_tools"}},
		{"remote_agents", document(openai, `"remote_agents":[{"name":"r","url":"https://r.example"}]`, ""), []string{"agent.remote_agents"}},
		{"sub_agents", document(openai, `"sub_agents":[{"name":"s"}]`, ""), []string{"agent.sub_agents"}},
		{"memory", document(openai, `"memory":{"ttl_days":3}`, ""), []string{"agent.memory"}},
		{"agent_plugins", document(openai, `"agent_plugins":{"skills":[{"name":"s","source":{"directory":"/skills","path":"x"}}]}`, ""), []string{"agent.agent_plugins"}},
		{"skills_directory", document(openai, `"skills_directory":"/skills"`, ""), []string{"agent.skills_directory"}},
		{"output", document(openai, `"output":{"json_schema":"e30=","sha256":"abc"}`, ""), []string{"agent.output"}},
		{"vertex", document(`{"model":"g","gemini_vertex_ai":{}}`, "", ""), []string{"model.gemini_vertex_ai"}},
		{"gemini_anthropic", document(`{"model":"g","gemini_anthropic":{}}`, "", ""), []string{"model.gemini_anthropic"}},
		{"sap", document(`{"model":"g","sap_ai_core":{"base_url":"https://s.example"}}`, "", ""), []string{"model.sap_ai_core"}},
		{"foundry anthropic", document(`{"model":"g","foundry":{"endpoint":"https://f.example","api_format":"anthropic"}}`, "", ""), []string{"model.foundry.api_format"}},
		{"token_exchange", document(`{"model":"g","openai":{"token_exchange":{"gdch_service_account":{"service_account_path":"/p","audience":"a"}}}}`, "", ""), []string{"model.openai.token_exchange"}},
		{"n", document(`{"model":"g","openai":{"n":2}}`, "", ""), []string{"model.openai.n"}},
		{"api_key_passthrough", document(`{"model":"g","api_key_passthrough":true,"openai":{}}`, "", ""), []string{"model.api_key_passthrough"}},
		{"tls skip", document(`{"model":"g","tls_insecure_skip_verify":true,"openai":{}}`, "", ""), []string{"model.tls_insecure_skip_verify"}},
		{"tls ca", document(`{"model":"g","tls_ca_cert_path":"/ca","openai":{}}`, "", ""), []string{"model.tls_ca_cert_path"}},
		{"tls system cas", document(`{"model":"g","tls_disable_system_cas":true,"openai":{}}`, "", ""), []string{"model.tls_disable_system_cas"}},
		{"token_passthrough", document(openai, `"http_tools":[{"params":{"url":"https://m.example/mcp"},"token_passthrough":true}]`, ""), []string{"agent.http_tools[0].token_passthrough"}},
		{"allowed_headers", document(openai, `"http_tools":[{"params":{"url":"https://m.example/mcp"},"allowed_headers":["x"]}]`, ""), []string{"agent.http_tools[0].allowed_headers"}},
		{"mcp tls", document(openai, `"http_tools":[{"params":{"url":"https://m.example/mcp","tls_insecure_skip_verify":true}}]`, ""), []string{"agent.http_tools[0].params.tls"}},
		{"mcp url dollar", document(openai, `"http_tools":[{"params":{"url":"https://m.example/$X"}}]`, ""), []string{"agent.http_tools[0].params.url"}},
		{"mcp tool star", document(openai, `"http_tools":[{"params":{"url":"https://m.example/mcp"},"tools":["a*"]}]`, ""), []string{"agent.http_tools[0].tools"}},
		{"gemini base_url", document(`{"model":"g","gemini":{"base_url":"https://g.example"}}`, "", ""), []string{"model.gemini.base_url"}},
		{"reasoning effort", document(`{"model":"g","openai":{"reasoning_effort":"turbo"}}`, "", ""), []string{"model.openai.reasoning_effort"}},
		{"bedrock region", document(`{"model":"g","bedrock":{}}`, "", ""), []string{"model.bedrock.region"}},
		{"model id", document(`{"model":"$(x)","openai":{}}`, "", ""), []string{"model.model"}},
		{"base url", document(`{"model":"g","openai":{"base_url":"ftp://x"}}`, "", ""), []string{"model.openai.base_url"}},
		{"token propagation", document(openai, "", `{"token_propagation":{"enabled":true}}`), []string{"runtime.token_propagation"}},
		{"mcp servers over the limit", document(openai, `"http_tools":[`+strings.TrimSuffix(strings.Repeat(`{"params":{"url":"https://h.example/mcp"}},`, 65), ",")+`]`, ""), []string{"agent.http_tools"}},
		{"mcp timeout over the limit", document(openai, `"http_tools":[{"params":{"url":"https://h.example/mcp","timeout":"7200s"}}]`, ""), []string{"agent.http_tools[0].params.timeout"}},
		{"mcp too many tools", document(openai, `"http_tools":[{"params":{"url":"https://h.example/mcp"},"tools":`+distinctTools(257)+`}]`, ""), []string{"agent.http_tools[0].tools"}},
		{"mcp tool name too long", document(openai, `"http_tools":[{"params":{"url":"https://h.example/mcp"},"tools":["`+strings.Repeat("t", 129)+`"]}]`, ""), []string{"agent.http_tools[0].tools"}},
		{"mcp url too long", document(openai, `"http_tools":[{"params":{"url":"https://h.example/`+strings.Repeat("p", 2100)+`"}}]`, ""), []string{"agent.http_tools[0].params.url"}},
		{"mcp header name outside the policy rule", document(openai, `"http_tools":[{"params":{"url":"https://h.example/mcp","headers":{"X.Y":"v"}}}]`, ""), []string{"agent.http_tools[0].params.headers"}},
		{"mcp header twice with different case", document(openai, `"http_tools":[{"params":{"url":"https://h.example/mcp","headers":{"authorization":"a","Authorization":"b"}}}]`, ""), []string{"agent.http_tools[0].params.headers"}},
		{"collects every problem", document(`{"model":"g","api_key_passthrough":true,"openai":{"n":3}}`, `"memory":{"ttl_days":1},"stdio_tools":[{"command":"x"}]`, ""),
			[]string{"agent.memory", "agent.stdio_tools", "model.api_key_passthrough", "model.openai.n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw, envOf(allKeys), Options{})
			var unsupported *UnsupportedError
			if !asUnsupported(err, &unsupported) {
				t.Fatalf("err = %v, want UnsupportedError", err)
			}
			got := unsupported.Fields()
			slices.Sort(got)
			want := slices.Clone(tc.fields)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("fields = %v, want %v (%v)", got, want, err)
			}
			for _, field := range want {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("error %q does not name %s", err, field)
				}
			}
		})
	}
}

func asUnsupported(err error, target **UnsupportedError) bool {
	u, ok := err.(*UnsupportedError)
	if ok {
		*target = u
	}
	return ok
}

func TestMissingProviderKeyNamesTheVariable(t *testing.T) {
	_, err := Parse(document(`{"model":"g","anthropic":{}}`, "", ""), envOf(nil), Options{})
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") || !strings.Contains(err.Error(), "model.credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownAPIFormatIsRejectedAtStartup(t *testing.T) {
	for name, model := range map[string]string{
		"openai": `{"model":"m","openai":{"api_format":"completions-v2"}}`,
		"azure":  `{"model":"m","azure_openai":{"endpoint":"https://r.openai.azure.com","api_format":"weird"}}`,
	} {
		_, err := Parse(document(model, "", ""), envOf(allKeys), Options{})
		if err == nil || !strings.Contains(err.Error(), "api_format") {
			t.Errorf("%s: err = %v, want a rejection naming api_format", name, err)
		}
	}
}

func TestUnparsableConfigIsRejected(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown field":   `{"agent":{"name":"a","bogus":1,"model":{"model":"m","openai":{}}}}`,
		"no provider":     `{"agent":{"name":"a","model":{"model":"m"}}}`,
		"no agent":        `{}`,
		"not json":        `nope`,
		"missing env var": `{"agent":{"name":"a","instruction":"__KAGENT_ENV[NOPE]__","model":{"model":"m","openai":{}}}}`,
	} {
		if _, err := Parse([]byte(raw), envOf(allKeys), Options{}); err == nil {
			t.Errorf("%s: Parse succeeded", name)
		}
	}
}

func TestEnvPlaceholdersAreExpanded(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "k", "KAGENT_CREDENTIAL_AB": "secret-token"}
	raw := document(`{"model":"m","openai":{}}`, `"instruction":"__KAGENT_ENV[KAGENT_CREDENTIAL_AB]__","http_tools":[{"params":{"url":"https://m.example/mcp","headers":{"X-Api":"__KAGENT_ENV[KAGENT_CREDENTIAL_AB]__"}}}]`, "")
	// duplicate "instruction" is invalid JSON for protojson, so build it properly
	raw = []byte(strings.Replace(string(raw), `"instruction":"be brief",`, "", 1))
	c := mustParse(t, raw, env)
	if c.SystemPrompt != "secret-token" {
		t.Fatalf("instruction = %q", c.SystemPrompt)
	}
	if v, _ := envValue(c, HeaderEnvPrefix+"1"); v != "secret-token" && v != "Bearer "+CredentialPlaceholder {
		t.Fatalf("header env = %q", v)
	}
	if strings.Contains(string(c.PolicyJSON), "secret-token") {
		t.Fatal("secret reached the policy file")
	}
}

var allowedDollar = regexp.MustCompile(`\$\{(KAGENT_PIG_HDR_[0-9]+|[A-Z_]+_API_KEY|AWS_BEARER_TOKEN_BEDROCK)\}`)

func TestHeaderIndirectionNeverEmitsShellOrExpansionSyntax(t *testing.T) {
	hostile := map[string]string{"X-Cmd": "!echo pwned", "X-Home": "plain $HOME", "X-Brace": "${HOME}", "Authorization": "!cat /etc/passwd"}
	hostileJSON, _ := json.Marshal(hostile)
	model := `{"model":"m","headers":` + string(hostileJSON) + `,"openai":{}}`
	raw := document(model, `"http_tools":[{"params":{"url":"https://tools.example/mcp","headers":`+string(hostileJSON)+`},"require_approval":true}]`, "")
	c := mustParse(t, raw, allKeys)
	for name, contents := range map[string]string{"models.json": string(c.ModelsJSON), "policy": string(c.PolicyJSON)} {
		stripped := allowedDollar.ReplaceAllString(contents, "")
		if strings.ContainsAny(stripped, "$") || strings.Contains(stripped, `"!`) || strings.Contains(stripped, "pwned") || strings.Contains(stripped, "passwd") {
			t.Errorf("%s leaks hostile header text:\n%s", name, contents)
		}
	}
	server := c.Policy.MCPServers[0]
	for header, ref := range server.Headers {
		if !regexp.MustCompile(`^\$\{KAGENT_PIG_HDR_[0-9]+\}$`).MatchString(ref) {
			t.Errorf("policy header %s = %q, not a single ${KAGENT_PIG_HDR_n} reference", header, ref)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
		if v, ok := envValue(c, name); !ok || v != hostile[header] {
			t.Errorf("env %s = %q, want %q", name, v, hostile[header])
		}
	}
	for header, ref := range decodeModels(t, c).Providers["openai"].Headers {
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
		if v, ok := envValue(c, name); !ok || v != hostile[header] {
			t.Errorf("model header %s env %s = %q", header, name, v)
		}
	}
	// header variables are unique across the model and MCP servers
	seen := map[string]bool{}
	for _, v := range c.Env {
		if strings.HasPrefix(v.Name, HeaderEnvPrefix) {
			if seen[v.Name] {
				t.Errorf("duplicate %s", v.Name)
			}
			seen[v.Name] = true
		}
	}
}

func TestInvalidHeaderValuesAreRejected(t *testing.T) {
	for _, value := range []string{"a\nb", "a\x00b"} {
		hdr, _ := json.Marshal(map[string]string{"X": value})
		raw := document(`{"model":"m","openai":{}}`, `"http_tools":[{"params":{"url":"https://t.example/mcp","headers":`+string(hdr)+`}}]`, "")
		if _, err := Parse(raw, envOf(allKeys), Options{}); err == nil || !strings.Contains(err.Error(), "params.headers") {
			t.Errorf("value %q: err = %v", value, err)
		}
	}
}

func TestPolicyFile(t *testing.T) {
	raw := document(`{"model":"m","openai":{}}`, `"http_tools":[{"params":{"url":"https://kagent-tools.kagent.svc.cluster.local:8084/mcp","headers":{"Authorization":"Bearer `+CredentialPlaceholder+`"},"timeout":"90s"},"tools":["kagent_probe","k8s-get"],"require_approval":true},{"params":{"url":"https://kagent-tools.other.svc/mcp"}}]`, "")
	c := mustParse(t, raw, allKeys)
	var got Policy
	if err := json.Unmarshal(c.PolicyJSON, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.MCPServers) != 2 || !got.AskUser {
		t.Fatalf("policy = %s", c.PolicyJSON)
	}
	a, b := got.MCPServers[0], got.MCPServers[1]
	if a.Name != "kagent-tools" || b.Name != "kagent-tools-2" {
		t.Errorf("names = %q, %q", a.Name, b.Name)
	}
	if a.URL != "https://kagent-tools.kagent.svc.cluster.local:8084/mcp" || a.TimeoutSeconds != 90 || !a.RequireApproval || !slices.Equal(a.Tools, []string{"kagent_probe", "k8s-get"}) {
		t.Errorf("server a = %+v", a)
	}
	if b.RequireApproval || len(b.Tools) != 0 || b.TimeoutSeconds != 0 {
		t.Errorf("server b = %+v", b)
	}
	// every server gets an Authorization header so PiG never starts OAuth discovery
	for _, s := range got.MCPServers {
		ref, ok := s.Headers["Authorization"]
		if !ok {
			t.Fatalf("server %s has no Authorization header", s.Name)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
		if v, _ := envValue(c, name); v != "Bearer "+CredentialPlaceholder {
			t.Errorf("%s Authorization = %q", s.Name, v)
		}
	}
}

func TestServerNameDerivation(t *testing.T) {
	taken := map[string]bool{}
	for _, tc := range []struct{ url, want string }{
		{"https://Kagent_Tools.ns:80/mcp", "kagent-tools"},
		{"http://10.0.0.5:8080/mcp", "10-0-0-5"},
		{"http://localhost/mcp", "localhost"},
		{"http://localhost:9/mcp", "localhost-2"},
		{"http://localhost:10/mcp", "localhost-3"},
		{"https://" + strings.Repeat("a", 63) + ".example/mcp", strings.Repeat("a", MaxServerName-3)},
	} {
		if got := serverName(tc.url, taken); got != tc.want {
			t.Errorf("serverName(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestToolsAndBuiltinSwitches(t *testing.T) {
	model := `{"model":"m","openai":{}}`
	tests := []struct {
		name    string
		runtime string
		options Options
		tools   string
		askUser bool
	}{
		{"defaults", "", Options{}, DefaultTools + ",ask_user", true},
		{"override", "", Options{Tools: "read,bash"}, "read,bash,ask_user", true},
		{"no bash", `{"builtin_tools":{"disable_bash":true}}`, Options{}, "read,edit,write,grep,find,ls,ask_user", true},
		{"no file search", `{"builtin_tools":{"disable_file_search":true}}`, Options{}, "read,bash,edit,write,ask_user", true},
		{"no ask_user", `{"builtin_tools":{"disable_ask_user":true}}`, Options{}, DefaultTools, false},
		{"explicit ask_user not doubled", "", Options{Tools: "read,ask_user"}, "read,ask_user", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse(document(model, "", tc.runtime), envOf(allKeys), tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if c.ToolsArg() != tc.tools || c.Policy.AskUser != tc.askUser {
				t.Fatalf("tools = %q askUser = %v", c.ToolsArg(), c.Policy.AskUser)
			}
		})
	}
}

func TestToolsArgOffersEveryBoundMCPServer(t *testing.T) {
	raw := document(`{"model":"m","openai":{}}`, `"http_tools":[{"params":{"url":"https://kagent-tools.kagent:8084/mcp"}},{"params":{"url":"https://other.example/mcp"},"require_approval":true}]`, "")
	c := mustParse(t, raw, allKeys)
	want := DefaultTools + ",ask_user,mcp__kagent_tools__*,mcp__other__*"
	if c.ToolsArg() != want {
		t.Fatalf("tools = %q, want %q", c.ToolsArg(), want)
	}
	// without ask_user and with a narrowed list the patterns stay
	c2, _ := Parse(document(`{"model":"m","openai":{}}`, `"http_tools":[{"params":{"url":"https://x.example/mcp"}}]`, `{"builtin_tools":{"disable_ask_user":true}}`), envOf(allKeys), Options{Tools: "read"})
	if c2.ToolsArg() != "read,mcp__x__*" {
		t.Fatalf("tools = %q", c2.ToolsArg())
	}
}

func TestWarningsForAcceptedButUnhonouredFields(t *testing.T) {
	raw := document(`{"model":"m","openai":{"timeout":"30s","strict_output_schema":true}}`,
		`"stream":false,"session_db_url":"sqlite:////data/sessions.db","context_config":{"compaction":{"compaction_interval":5}},"http_tools":[{"params":{"url":"https://t.example/mcp","sse_read_timeout":"5s"}}]`,
		`{"host":"0.0.0.0","builtin_tools":{"bash_venv_path":"/venv"}}`)
	c := mustParse(t, raw, allKeys)
	for _, want := range []string{"openai.timeout", "strict_output_schema", "agent.stream=false", "session_db_url", "context_config.compaction", "sse_read_timeout", "runtime.host", "bash_venv_path"} {
		if !slices.ContainsFunc(c.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
			t.Errorf("no warning mentions %q: %q", want, c.Warnings)
		}
	}
}

func TestOptionsFromEnv(t *testing.T) {
	getenv := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	good, err := OptionsFromEnv(getenv(map[string]string{EnvTools: "read,bash", EnvLogLevel: "debug", EnvContextWindow: "200000", EnvMaxTokens: "4096", EnvPassEnv: "GH_TOKEN,FOO"}))
	if err != nil || good.Tools != "read,bash" || good.LogLevel != "debug" || good.ContextWindow != 200000 || good.MaxTokens != 4096 || !slices.Equal(good.PassEnv, []string{"GH_TOKEN", "FOO"}) {
		t.Fatalf("good = %+v, %v", good, err)
	}
	for name, env := range map[string]map[string]string{
		"tools with space":   {EnvTools: "read, bash"},
		"tools trailing":     {EnvTools: "read,"},
		"bad level":          {EnvLogLevel: "trace"},
		"bad window":         {EnvContextWindow: "0"},
		"bad max":            {EnvMaxTokens: "x"},
		"pass kagent secret": {EnvPassEnv: "KAGENT_BOOTSTRAP_JSON"},
		"pass bad name":      {EnvPassEnv: "A-B"},
	} {
		if _, err := OptionsFromEnv(getenv(env)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTelemetryProjection(t *testing.T) {
	raw := document(`{"model":"m","openai":{}}`, "", `{"telemetry":{"traces":{"endpoint":"http://otel:4317"},"capture_content":true,"max_capture_bytes":2048,"resource_attributes":{"k":"v"}},"log_level":"warn","control_plane_url":"http://ctl:8083","max_content_length":100}`)
	c := mustParse(t, raw, allKeys)
	tel := c.Telemetry
	if tel.Exporters == nil || tel.Exporters.Traces.Endpoint != "http://otel:4317" || tel.Exporters.Traces.Protocol != "grpc" || !tel.CaptureContent || tel.MaxCaptureBytes != 2048 || tel.Provider != "openai" || tel.Model != "m" {
		t.Fatalf("telemetry = %+v", tel)
	}
	if tel.Runtime != "" || tel.AgentName != "" {
		t.Fatalf("identity must stay empty, got %+v", tel)
	}
	if err := tel.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "warn" || c.ControlPlaneURL != "http://ctl:8083" || c.MaxContentLength == nil || *c.MaxContentLength != 100 {
		t.Fatalf("runtime = %+v", c)
	}
	o, _ := Parse(raw, envOf(allKeys), Options{LogLevel: "debug"})
	if o.LogLevel != "debug" {
		t.Fatalf("env log level must win, got %q", o.LogLevel)
	}
}

func TestServerNamesStayShortEnoughForTheGatePrefixToSurviveHashing(t *testing.T) {
	// PiG shortens a long tool name to 55 characters plus a hash suffix; the approval gate matches the prefix
	// mcp__<server>__, which must therefore fit in 55 characters for every name serverName can return.
	taken := map[string]bool{}
	for i := 0; i < 5; i++ {
		name := serverName("https://"+strings.Repeat("b", 63)+".example/mcp", taken)
		if prefix := "mcp__" + strings.ReplaceAll(name, "-", "_") + "__"; len(prefix) > 55 {
			t.Fatalf("server name %q gives a %d character prefix", name, len(prefix))
		}
	}
}

func distinctTools(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("tool%d", i)
	}
	raw, _ := json.Marshal(names)
	return string(raw)
}

// kagent's Go ADK connects to MCP servers and lists their tools when an invocation is prepared, not at startup,
// so a binding names no tools the runtime has seen. The harness checks the shape of each binding at startup and
// never asks a server anything: a binding with no allowlist, a slow server, an approval requirement and the
// connection options kagent compiles all stay accepted, and the tool list is left to the first use.
func TestMCPBindingsWithoutAnAllowlistAreAcceptedAndDiscoveredOnFirstUse(t *testing.T) {
	raw := document(`{"model":"m","openai":{}}`, `"http_tools":[
		{"params":{"url":"http://kagent-tools.kagent:8084/mcp"}},
		{"params":{"url":"http://slow.kagent:8080/mcp","timeout":"600s","sse_read_timeout":"300s","terminate_on_close":false,"headers":{"X-Team":"a"}},"require_approval":true},
		{"params":{"url":"http://narrow.kagent:8080/mcp"},"tools":["only_this"]}]`, "")
	c := mustParse(t, raw, allKeys)
	if len(c.Policy.MCPServers) != 3 {
		t.Fatalf("servers = %+v", c.Policy.MCPServers)
	}
	open, approval, narrow := c.Policy.MCPServers[0], c.Policy.MCPServers[1], c.Policy.MCPServers[2]
	if len(open.Tools) != 0 || len(approval.Tools) != 0 || !slices.Equal(narrow.Tools, []string{"only_this"}) {
		t.Fatalf("allowlists = %v %v %v", open.Tools, approval.Tools, narrow.Tools)
	}
	if !approval.RequireApproval || open.RequireApproval || approval.TimeoutSeconds != 600 {
		t.Fatalf("approval server = %+v", approval)
	}
	for _, want := range []string{"mcp__kagent_tools__*", "mcp__slow__*", "mcp__narrow__*"} {
		if !strings.Contains(c.ToolsArg(), want) {
			t.Errorf("tools %q lack %s", c.ToolsArg(), want)
		}
	}
	// the policy file carries the empty allowlist as no restriction, never as "no tools"
	var policy struct {
		Servers []struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
		} `json:"mcp_servers"`
	}
	if err := json.Unmarshal(c.PolicyJSON, &policy); err != nil || len(policy.Servers) != 3 || len(policy.Servers[0].Tools) != 0 {
		t.Fatalf("policy = %s (%v)", c.PolicyJSON, err)
	}
	// only sse_read_timeout and terminate_on_close are reported, once
	var noted int
	for _, w := range c.Warnings {
		if strings.Contains(w, "sse_read_timeout") {
			noted++
		}
	}
	if noted != 1 {
		t.Fatalf("warnings = %v", c.Warnings)
	}
}
