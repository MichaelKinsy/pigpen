package config

import (
	"encoding/json"
	"google.golang.org/protobuf/types/known/durationpb"
	"regexp"
	"strings"

	configv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/config/v1"
)

// Defaults for the model limits kagent does not compile. PiG compacts by the
// model's contextWindow, so both are always written explicitly.
const (
	DefaultContextWindow = 128000
	DefaultMaxTokens     = 8192
)

// Pig API identifiers (models.json "api").
const (
	apiOpenAICompletions = "openai-completions"
	apiOpenAIResponses   = "openai-responses"
	apiAzureResponses    = "azure-openai-responses"
	apiAnthropic         = "anthropic-messages"
	apiGoogle            = "google-generative-ai"
	apiMistral           = "mistral-conversations"
	apiBedrock           = "bedrock-converse-stream"
)

var (
	modelIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
	thinkingLevels   = map[string]string{"off": "off", "none": "off", "minimal": "minimal", "low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max"}
	azureDeployChars = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type modelsFile struct {
	Providers map[string]modelProvider `json:"providers"`
}

type modelProvider struct {
	BaseURL string            `json:"baseUrl,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Models  []modelEntry      `json:"models"`
}

type modelEntry struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	API            string         `json:"api"`
	Reasoning      bool           `json:"reasoning"`
	ContextWindow  int            `json:"contextWindow"`
	MaxTokens      int            `json:"maxTokens"`
	SamplingParams map[string]any `json:"samplingParams,omitempty"`
}

// EnvVar is one environment variable handed to pig.
type EnvVar struct{ Name, Value string }

// resolvedModel is the result of mapping an ADKModelConfig onto PiG.
type resolvedModel struct {
	provider  string
	model     string
	thinking  string
	modelsRaw []byte
	env       []EnvVar
}

// Options are the adapter's own tunables (KAGENT_PIG_* variables).
type Options struct {
	ContextWindow int
	MaxTokens     int
	// Tools is the --tools allowlist (KAGENT_PIG_TOOLS), empty for the default.
	Tools string
	// LogLevel overrides the compiled log level (KAGENT_PIG_LOG_LEVEL).
	LogLevel string
	// PassEnv lists extra environment variable names pig may inherit (KAGENT_PIG_PASS_ENV).
	PassEnv []string
}

type modelMapper struct {
	c       *collector
	headers *headerAllocator
	getenv  func(string) string
	options Options
	env     []EnvVar
	needEnv map[string]bool
}

func (m *modelMapper) keyEnv(names ...string) string {
	for _, name := range names {
		if value := m.getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// requireKey adds the provider key variable under pigName, read from the
// first of sources present. The value is the controller's inert placeholder.
func (m *modelMapper) requireKey(pigName string, sources ...string) bool {
	value := m.keyEnv(sources...)
	if value == "" {
		m.c.reject("model.credentials", "environment variable %s is not set; the controller injects the credential placeholder there", strings.Join(sources, " or "))
		return false
	}
	m.env = append(m.env, EnvVar{Name: pigName, Value: value})
	return true
}

func (m *modelMapper) limits(maxTokens *int64) (window, tokens int) {
	window, tokens = DefaultContextWindow, DefaultMaxTokens
	if m.options.ContextWindow > 0 {
		window = m.options.ContextWindow
	}
	if m.options.MaxTokens > 0 {
		tokens = m.options.MaxTokens
	}
	if maxTokens != nil {
		tokens = int(*maxTokens)
	}
	return window, tokens
}

func (m *modelMapper) userHeaders(model *configv1.ADKModelConfig) map[string]string {
	if len(model.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(model.Headers))
	for name, value := range model.Headers {
		if !headerNamePattern.MatchString(name) || !validHeaderValue(value) {
			m.c.reject("model.headers", "header %q has an invalid name or value", name)
			continue
		}
		headers[name] = m.headers.reference(value)
	}
	return headers
}

func (m *modelMapper) checkCommon(model *configv1.ADKModelConfig) {
	if model.ApiKeyPassthrough {
		m.c.reject("model.api_key_passthrough", "no caller credential reaches a Harness turn")
	}
	if model.TlsInsecureSkipVerify != nil && *model.TlsInsecureSkipVerify {
		m.c.reject("model.tls_insecure_skip_verify", "PiG has no per-provider TLS option; trust comes from the egress trust bundle")
	}
	if model.TlsCaCertPath != nil && *model.TlsCaCertPath != "" {
		m.c.reject("model.tls_ca_cert_path", "PiG has no per-provider TLS option; trust comes from the egress trust bundle")
	}
	if model.TlsDisableSystemCas != nil && *model.TlsDisableSystemCas {
		m.c.reject("model.tls_disable_system_cas", "PiG has no per-provider TLS option; trust comes from the egress trust bundle")
	}
	if model.Model == "" {
		m.c.reject("model.model", "model name is required")
	} else if !modelIDPattern.MatchString(model.Model) {
		m.c.reject("model.model", "model name %q contains characters PiG could interpret", model.Model)
	}
}

func (m *modelMapper) checkURL(field, raw string) bool {
	if raw == "" {
		return true
	}
	if !validHTTPURL(raw) {
		m.c.reject(field, "must be an absolute http(s) URL without credentials, fragment or $")
		return false
	}
	return true
}

func (m *modelMapper) warnTimeout(field string, d *durationpb.Duration) {
	if d != nil && d.AsDuration() > 0 {
		m.c.warn("%s is accepted but not honoured: PiG has no per-request model timeout", field)
	}
}

// mapModel compiles the model section. It reports every unsupported field.
func mapModel(c *collector, headers *headerAllocator, getenv func(string) string, options Options, model *configv1.ADKModelConfig) *resolvedModel {
	m := &modelMapper{c: c, headers: headers, getenv: getenv, options: options}
	if model == nil {
		c.reject("model", "a model is required")
		return nil
	}
	m.checkCommon(model)
	var out *resolvedModel
	switch p := model.Provider.(type) {
	case *configv1.ADKModelConfig_Openai:
		out = m.openai(model, p.Openai)
	case *configv1.ADKModelConfig_AzureOpenai:
		out = m.azure(model, p.AzureOpenai)
	case *configv1.ADKModelConfig_Anthropic:
		out = m.anthropic(model, p.Anthropic)
	case *configv1.ADKModelConfig_Mistral:
		out = m.mistral(model, p.Mistral)
	case *configv1.ADKModelConfig_Gemini:
		out = m.gemini(model, p.Gemini)
	case *configv1.ADKModelConfig_Bedrock:
		out = m.bedrock(model, p.Bedrock)
	case *configv1.ADKModelConfig_Ollama:
		out = m.ollama(model, p.Ollama)
	case *configv1.ADKModelConfig_Foundry:
		out = m.foundry(model, p.Foundry)
	case *configv1.ADKModelConfig_GeminiVertexAi:
		c.reject("model.gemini_vertex_ai", "PiG's Vertex provider needs Google ADC or an API key that gateway header injection cannot supply")
	case *configv1.ADKModelConfig_GeminiAnthropic:
		c.reject("model.gemini_anthropic", "Anthropic on Vertex needs Google ADC that gateway header injection cannot supply")
	case *configv1.ADKModelConfig_SapAiCore:
		c.reject("model.sap_ai_core", "PiG has no SAP AI Core provider and gateway injection cannot do its OAuth client credentials")
	default:
		c.reject("model.provider", "unknown provider %T; failing closed", p)
	}
	if out != nil {
		out.env = append(out.env, m.env...)
	}
	return out
}

func (m *modelMapper) finish(providerID string, provider modelProvider, entry modelEntry, thinking string) *resolvedModel {
	provider.Models = []modelEntry{entry}
	raw, err := json.MarshalIndent(modelsFile{Providers: map[string]modelProvider{providerID: provider}}, "", "  ")
	if err != nil {
		m.c.reject("model", "encode models.json: %v", err)
		return nil
	}
	return &resolvedModel{provider: providerID, model: entry.ID, thinking: thinking, modelsRaw: raw}
}

func entryFor(id, api string, window, tokens int) modelEntry {
	return modelEntry{ID: id, Name: id, API: api, ContextWindow: window, MaxTokens: tokens}
}

// sampling builds PiG samplingParams, which only the OpenAI-style APIs honour.
func sampling(entry *modelEntry, values map[string]any) {
	if len(values) > 0 {
		entry.SamplingParams = values
	}
}

func (m *modelMapper) openai(model *configv1.ADKModelConfig, p *configv1.ADKOpenAI) *resolvedModel {
	if p == nil {
		p = &configv1.ADKOpenAI{}
	}
	if p.TokenExchange != nil {
		m.c.reject("model.openai.token_exchange", "GDCH token exchange needs a service-account file and is deferred in kagent")
	}
	if p.N != nil && *p.N > 1 {
		m.c.reject("model.openai.n", "PiG has no multi-candidate sampling (n=%d)", *p.N)
	}
	api := apiOpenAICompletions
	switch p.ApiFormat {
	case "", "chatCompletions":
	case "responses":
		api = apiOpenAIResponses
	default:
		m.c.reject("model.openai.api_format", "unknown API format %q", p.ApiFormat)
	}
	m.checkURL("model.openai.base_url", p.BaseUrl)
	maxTokens := p.MaxTokens
	if p.MaxCompletionTokens != nil {
		maxTokens = p.MaxCompletionTokens
	}
	window, tokens := m.limits(maxTokens)
	entry := entryFor(model.Model, api, window, tokens)
	params := map[string]any{}
	if p.Temperature != nil {
		params["temperature"] = *p.Temperature
	}
	if p.TopP != nil {
		params["top_p"] = *p.TopP
	}
	for name, value := range map[string]*float64{"frequency_penalty": p.FrequencyPenalty, "presence_penalty": p.PresencePenalty} {
		if value == nil {
			continue
		}
		if api == apiOpenAIResponses {
			m.c.warn("model.openai.%s is accepted but not honoured: the Responses API has no such parameter", name)
			continue
		}
		params[name] = *value
	}
	if p.Seed != nil {
		if api == apiOpenAIResponses {
			m.c.warn("model.openai.seed is accepted but not honoured: the Responses API has no seed parameter")
		} else {
			params["seed"] = *p.Seed
		}
	}
	sampling(&entry, params)
	thinking := ""
	if p.ReasoningEffort != nil && *p.ReasoningEffort != "" {
		level, ok := thinkingLevels[strings.ToLower(*p.ReasoningEffort)]
		if !ok {
			m.c.reject("model.openai.reasoning_effort", "%q is not a PiG thinking level (off, minimal, low, medium, high, xhigh, max)", *p.ReasoningEffort)
		}
		entry.Reasoning, thinking = true, level
	}
	m.warnTimeout("model.openai.timeout", p.Timeout)
	if p.StrictOutputSchema {
		m.c.warn("model.openai.strict_output_schema is accepted but not honoured: PiG does not constrain output to a schema")
	}
	provider := modelProvider{BaseURL: p.BaseUrl, Headers: m.userHeaders(model)}
	if !m.requireKey("OPENAI_API_KEY", "OPENAI_API_KEY") {
		return nil
	}
	provider.APIKey = "${OPENAI_API_KEY}"
	return m.finish("openai", provider, entry, thinking)
}

func (m *modelMapper) azure(model *configv1.ADKModelConfig, p *configv1.ADKAzureOpenAI) *resolvedModel {
	if p == nil {
		p = &configv1.ADKAzureOpenAI{}
	}
	if p.Endpoint == "" {
		m.c.reject("model.azure_openai.endpoint", "endpoint is required")
	}
	m.checkURL("model.azure_openai.endpoint", p.Endpoint)
	api := apiAzureResponses
	switch p.ApiFormat {
	case "", "chatCompletions":
		api = apiOpenAICompletions
	case "responses":
	default:
		m.c.reject("model.azure_openai.api_format", "unknown API format %q", p.ApiFormat)
	}
	window, tokens := m.limits(p.MaxTokens)
	entry := entryFor(model.Model, api, window, tokens)
	params := map[string]any{}
	if p.Temperature != nil {
		params["temperature"] = *p.Temperature
	}
	if p.TopP != nil {
		params["top_p"] = *p.TopP
	}
	sampling(&entry, params)
	if p.StrictOutputSchema {
		m.c.warn("model.azure_openai.strict_output_schema is accepted but not honoured: PiG does not constrain output to a schema")
	}
	headers := m.userHeaders(model)
	if !m.requireKey("AZURE_OPENAI_API_KEY", "AZURE_OPENAI_API_KEY") {
		return nil
	}
	if api == apiOpenAICompletions {
		// Azure chat completions through PiG sends only Authorization: Bearer; the
		// gateway rule for Azure replaces api-key, so the placeholder must travel there too.
		if headers == nil {
			headers = map[string]string{}
		}
		if _, set := headers["api-key"]; !set {
			headers["api-key"] = "${AZURE_OPENAI_API_KEY}"
		}
	}
	if p.ApiVersion != "" {
		if !azureDeployChars.MatchString(p.ApiVersion) {
			m.c.reject("model.azure_openai.api_version", "contains characters outside letters, digits, '.', '_' and '-'")
		} else {
			m.env = append(m.env, EnvVar{Name: "AZURE_OPENAI_API_VERSION", Value: p.ApiVersion})
		}
	}
	if p.Deployment != "" && p.Deployment != model.Model {
		if !azureDeployChars.MatchString(p.Deployment) {
			m.c.reject("model.azure_openai.deployment", "contains characters outside letters, digits, '.', '_' and '-'")
		} else {
			m.env = append(m.env, EnvVar{Name: "AZURE_OPENAI_DEPLOYMENT_NAME_MAP", Value: model.Model + "=" + p.Deployment})
		}
	}
	return m.finish("azure", modelProvider{BaseURL: p.Endpoint, APIKey: "${AZURE_OPENAI_API_KEY}", Headers: headers}, entry, "")
}

func (m *modelMapper) foundry(model *configv1.ADKModelConfig, p *configv1.ADKFoundry) *resolvedModel {
	if p == nil {
		p = &configv1.ADKFoundry{}
	}
	if p.ApiFormat == "anthropic" {
		m.c.reject("model.foundry.api_format", "the Foundry Anthropic path layout is unverified in PiG; only the openai format is mapped")
		return nil
	}
	if p.Endpoint == "" {
		m.c.reject("model.foundry.endpoint", "endpoint is required")
	}
	m.checkURL("model.foundry.endpoint", p.Endpoint)
	m.c.warn("model.foundry is mapped from PiG's documentation only (azure provider, chat completions, api-key header) and has not been verified against Foundry")
	if p.StrictOutputSchema {
		m.c.warn("model.foundry.strict_output_schema is accepted but not honoured: PiG does not constrain output to a schema")
	}
	window, tokens := m.limits(nil)
	entry := entryFor(model.Model, apiOpenAICompletions, window, tokens)
	headers := m.userHeaders(model)
	if !m.requireKey("AZURE_OPENAI_API_KEY", "FOUNDRY_API_KEY", "AZURE_OPENAI_API_KEY") {
		return nil
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if _, set := headers["api-key"]; !set {
		headers["api-key"] = "${AZURE_OPENAI_API_KEY}"
	}
	return m.finish("azure", modelProvider{BaseURL: p.Endpoint, APIKey: "${AZURE_OPENAI_API_KEY}", Headers: headers}, entry, "")
}

func (m *modelMapper) anthropic(model *configv1.ADKModelConfig, p *configv1.ADKAnthropic) *resolvedModel {
	if p == nil {
		p = &configv1.ADKAnthropic{}
	}
	m.checkURL("model.anthropic.base_url", p.BaseUrl)
	window, tokens := m.limits(p.MaxTokens)
	entry := entryFor(model.Model, apiAnthropic, window, tokens)
	m.ignoredSampling("model.anthropic", p.Temperature, p.TopP)
	if p.TopK != nil {
		m.c.warn("model.anthropic.top_k is accepted but not honoured: PiG applies sampling parameters only to OpenAI-style APIs")
	}
	m.warnTimeout("model.anthropic.timeout", p.Timeout)
	if p.PromptCaching || p.CacheTtl != "" {
		m.c.warn("model.anthropic.prompt_caching and cache_ttl are accepted but not honoured: PiG controls caching with PI_CACHE_RETENTION, which this harness does not set")
	}
	if !m.requireKey("ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY") {
		return nil
	}
	return m.finish("anthropic", modelProvider{BaseURL: p.BaseUrl, APIKey: "${ANTHROPIC_API_KEY}", Headers: m.userHeaders(model)}, entry, "")
}

func (m *modelMapper) mistral(model *configv1.ADKModelConfig, p *configv1.ADKMistral) *resolvedModel {
	if p == nil {
		p = &configv1.ADKMistral{}
	}
	m.checkURL("model.mistral.base_url", p.BaseUrl)
	window, tokens := m.limits(p.MaxTokens)
	entry := entryFor(model.Model, apiMistral, window, tokens)
	m.ignoredSampling("model.mistral", p.Temperature, p.TopP)
	m.warnTimeout("model.mistral.timeout", p.Timeout)
	if !m.requireKey("MISTRAL_API_KEY", "MISTRAL_API_KEY") {
		return nil
	}
	return m.finish("mistral", modelProvider{BaseURL: p.BaseUrl, APIKey: "${MISTRAL_API_KEY}", Headers: m.userHeaders(model)}, entry, "")
}

func (m *modelMapper) gemini(model *configv1.ADKModelConfig, p *configv1.ADKGemini) *resolvedModel {
	if p == nil {
		p = &configv1.ADKGemini{}
	}
	if p.BaseUrl != "" {
		m.c.reject("model.gemini.base_url", "kagent treats it as a complete API prefix while PiG appends /v1beta; only the default endpoint is mapped")
	}
	window, tokens := m.limits(p.MaxOutputTokens)
	entry := entryFor(model.Model, apiGoogle, window, tokens)
	// PiG reads GEMINI_API_KEY; the controller injects GOOGLE_API_KEY.
	if !m.requireKey("GEMINI_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY") {
		return nil
	}
	return m.finish("google", modelProvider{APIKey: "${GEMINI_API_KEY}", Headers: m.userHeaders(model)}, entry, "")
}

func (m *modelMapper) bedrock(model *configv1.ADKModelConfig, p *configv1.ADKBedrock) *resolvedModel {
	if p == nil {
		p = &configv1.ADKBedrock{}
	}
	if p.Region == "" {
		m.c.reject("model.bedrock.region", "region is required")
	} else if !azureDeployChars.MatchString(p.Region) {
		m.c.reject("model.bedrock.region", "contains characters outside letters, digits, '.', '_' and '-'")
	} else {
		m.env = append(m.env, EnvVar{Name: "AWS_REGION", Value: p.Region})
	}
	if len(p.AdditionalModelRequestFields) > 0 {
		m.c.warn("model.bedrock.additional_model_request_fields is accepted but not honoured: PiG cannot pass extra Converse request fields")
	}
	if p.PromptCaching || p.CacheTtl != "" {
		m.c.warn("model.bedrock.prompt_caching and cache_ttl are accepted but not honoured")
	}
	if p.Guardrail != nil {
		m.c.warn("model.bedrock.guardrail is accepted but not honoured: PiG sends no guardrail configuration")
	}
	m.warnTimeout("model.bedrock.read_timeout", p.ReadTimeout)
	m.warnTimeout("model.bedrock.connect_timeout", p.ConnectTimeout)
	window, tokens := m.limits(nil)
	entry := entryFor(model.Model, apiBedrock, window, tokens)
	if !m.requireKey("AWS_BEARER_TOKEN_BEDROCK", "AWS_BEARER_TOKEN_BEDROCK") {
		return nil
	}
	return m.finish("amazon-bedrock", modelProvider{Headers: m.userHeaders(model)}, entry, "")
}

func (m *modelMapper) ollama(model *configv1.ADKModelConfig, p *configv1.ADKOllama) *resolvedModel {
	if p != nil && len(p.Options) > 0 {
		m.c.warn("model.ollama.options is accepted but not honoured: PiG has no Ollama option map")
	}
	base := m.getenv("OLLAMA_API_BASE")
	if base == "" {
		base = "http://localhost:11434"
	}
	base = strings.TrimRight(base, "/")
	if !m.checkURL("OLLAMA_API_BASE", base) {
		return nil
	}
	window, tokens := m.limits(nil)
	entry := entryFor(model.Model, apiOpenAICompletions, window, tokens)
	// Local Ollama is unkeyed; PiG's custom provider still needs a non-empty key.
	key := "ollama"
	if value := m.getenv("OLLAMA_API_KEY"); value != "" {
		m.env = append(m.env, EnvVar{Name: "OLLAMA_API_KEY", Value: value})
		return m.finish("ollama", modelProvider{BaseURL: base + "/v1", APIKey: "${OLLAMA_API_KEY}", Headers: m.userHeaders(model)}, entry, "")
	}
	return m.finish("ollama", modelProvider{BaseURL: base + "/v1", APIKey: key, Headers: m.userHeaders(model)}, entry, "")
}

func (m *modelMapper) ignoredSampling(prefix string, temperature, topP *float64) {
	if temperature != nil {
		m.c.warn("%s.temperature is accepted but not honoured: PiG applies sampling parameters only to OpenAI-style APIs", prefix)
	}
	if topP != nil {
		m.c.warn("%s.top_p is accepted but not honoured: PiG applies sampling parameters only to OpenAI-style APIs", prefix)
	}
}
