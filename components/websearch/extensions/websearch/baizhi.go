package websearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Port of baizhi.ts: web search through Baizhi's MCP server. The original drives the MCP SDK's
// streamable-HTTP client; this is the part of that protocol the exchange uses, written against the
// same wire sequence (initialize, notifications/initialized, the optional GET event stream,
// tools/call, DELETE) with the same headers.

const (
	baizhiMCPURL    = "https://agent-toolkit.app.baizhi.cloud/mcp"
	baizhiProtocol  = "2025-11-25"
	baizhiCleanupMs = 1500
)

var mcpSupportedProtocols = []string{baizhiProtocol, "2025-06-18", "2025-03-26", "2024-11-05", "2024-10-07"}

// baizhiConfig is credentialOptions() (baizhi.ts:14): any problem with the file hides its content,
// because a parse error can quote a fragment holding a literal key.
func baizhiConfig() (map[string]any, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		return nil, fmt.Errorf("Invalid configuration in %s: expected a JSON object", ConfigPath())
	}
	return root, nil
}

// IsBaizhiAvailable reports whether a Baizhi credential source is configured (baizhi.ts:27).
func IsBaizhiAvailable() bool {
	root, err := baizhiConfig()
	if err != nil {
		return false
	}
	return HasCredentialSource(CredentialOptions{Provider: "Baizhi", ConfiguredValue: root["baizhiApiKey"], EnvironmentValue: envOrNil("BAIZHI_API_KEY")})
}

type baizhiFilter struct {
	Domains        []string `json:"domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

type baizhiArguments struct {
	Query       string        `json:"query"`
	Count       int           `json:"count"`
	NeedSummary bool          `json:"need_summary"`
	TimeRange   string        `json:"time_range"`
	Filter      *baizhiFilter `json:"filter,omitempty"`
}

func baizhiSearchArguments(query string, o SearchOptions) (*baizhiArguments, error) {
	if jsTrim(query) == "" {
		return nil, errors.New("Baizhi search query must not be empty")
	}
	var domains, exclude []string
	for _, raw := range o.DomainFilter {
		bare := jsTrim(strings.TrimPrefix(jsTrim(raw), "-"))
		domain := ""
		if _, err := netip.ParseAddr(bare); err == nil {
			domain = bare
		} else {
			domain = NormalizeDomain(raw)
		}
		if domain == "" {
			return nil, errors.New("Baizhi domain filter must contain a valid domain")
		}
		target := &domains
		if strings.HasPrefix(jsTrim(raw), "-") {
			target = &exclude
		}
		if !sliceHas(*target, domain) {
			*target = append(*target, domain)
		}
	}
	args := &baizhiArguments{Query: jsTrim(query), Count: NormalizeSearchResultCount(o.NumResults), TimeRange: o.RecencyFilter}
	if args.TimeRange == "" {
		// The published discovery contract defaults to month, not unrestricted time.
		args.TimeRange = "month"
	}
	if len(domains) > 0 || len(exclude) > 0 {
		args.Filter = &baizhiFilter{Domains: domains, ExcludeDomains: exclude}
	}
	return args, nil
}

// mcpError is a failure of the MCP exchange classified like the SDK's errors are by the catch
// block of searchWithBaizhi: an HTTP status or JSON-RPC code, a network TypeError, a SyntaxError.
type mcpError struct {
	code    int  // HTTP status or JSON-RPC error code, 0 when none
	network bool // TypeError: the request could not be completed
	syntax  bool // SyntaxError: a body that is not JSON
}

func (e *mcpError) Error() string { return "MCP request failed" }

func baizhiHeaderValueOK(key string) bool {
	for _, r := range key {
		if r == 0 || r == '\n' || r == '\r' || r > 0xFF {
			return false
		}
	}
	return true
}

// SearchWithBaizhi runs one websearch_search call (baizhi.ts:77).
func SearchWithBaizhi(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	args, err := baizhiSearchArguments(query, o)
	if err != nil {
		return nil, err
	}
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	if tctx.Err() != nil {
		return nil, errors.New("Aborted")
	}
	root, err := baizhiConfig()
	if err != nil {
		return nil, err
	}
	apiKey, err := ResolveCredential(tctx, CredentialOptions{Provider: "Baizhi", ConfiguredValue: root["baizhiApiKey"], EnvironmentValue: envOrNil("BAIZHI_API_KEY")})
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, errors.New("Baizhi API key not found. Set BAIZHI_API_KEY or baizhiApiKey in web-search.json. Get your own key at https://agent-toolkit.app.baizhi.cloud/")
	}
	if !baizhiHeaderValueOK(apiKey) {
		return nil, errors.New("Baizhi credential resolution failed: invalid-header-value")
	}
	s := &mcpSession{auth: "Bearer " + apiKey}
	answer, err := s.run(tctx, args, apiKey)
	// Best effort, separately bounded DELETE; always before the result is reported.
	s.terminate(ctx)
	if err != nil {
		return nil, errors.New(baizhiMessage(ctx, tctx, err))
	}
	return &SearchResponse{Answer: answer, Results: []SearchResult{}}, nil
}

var baizhiPassThroughRE = regexp.MustCompile(`^Baizhi (search tool returned an error|returned invalid response:)`)

// baizhiMessage is the catch block of searchWithBaizhi: never remote bodies or nested errors,
// which can echo credentials in plain, encoded or transformed form.
func baizhiMessage(ctx, tctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "Aborted"
	}
	if tctx.Err() != nil {
		return "Baizhi request timed out after 60s"
	}
	var me *mcpError
	if errors.As(err, &me) {
		switch {
		case me.code >= 400 && me.code <= 599:
			return fmt.Sprintf("Baizhi HTTP %d request failed; check your API key, account and service status", me.code)
		case me.network:
			return "Baizhi network request failed"
		case me.syntax || me.code == -32700 || me.code == -32602:
			return "Baizhi returned invalid response"
		}
	} else if baizhiPassThroughRE.MatchString(err.Error()) {
		return err.Error()
	}
	return "Baizhi MCP request failed; check your connection, API key and account status"
}

type mcpSession struct {
	auth      string
	sessionID string
	protocol  string
}

type mcpRequest struct {
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id,omitempty"`
}

type mcpInitParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    struct{}       `json:"capabilities"`
	ClientInfo      map[string]any `json:"clientInfo"`
}

type mcpCallParams struct {
	Name      string           `json:"name"`
	Arguments *baizhiArguments `json:"arguments"`
}

func (s *mcpSession) headers() http.Header {
	h := http.Header{"Authorization": {s.auth}}
	if s.sessionID != "" {
		h.Set("Mcp-Session-Id", s.sessionID)
	}
	if s.protocol != "" {
		h.Set("Mcp-Protocol-Version", s.protocol)
	}
	return h
}

// do sends one request without following redirects (a fixed endpoint: neither the credential nor
// the session id may reach another origin, including through discovery redirects).
func (s *mcpSession) do(ctx context.Context, method string, h http.Header, body []byte) (*http.Response, error) {
	u, _ := url.Parse(baizhiMCPURL)
	init := RequestInit{Method: method, Header: h}
	if body != nil {
		init.Body = body
	}
	resp, err := doAPI(ctx, u, init)
	if err != nil {
		return nil, &mcpError{network: true}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, &mcpError{network: true}
	}
	return resp, nil
}

func (s *mcpSession) post(ctx context.Context, req mcpRequest) (*http.Response, error) {
	b, _ := json.Marshal(req)
	h := s.headers()
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/event-stream")
	resp, err := s.do(ctx, "POST", h, b)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, &mcpError{code: resp.StatusCode}
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		s.sessionID = sid
	}
	return resp, nil
}

type mcpMessage struct {
	ID     *json.RawMessage `json:"id"`
	Result json.RawMessage  `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// request sends a JSON-RPC request and returns the result of the response carrying its id, read
// from a JSON body or from a server-sent event stream.
func (s *mcpSession) request(ctx context.Context, method string, params any, id int) (json.RawMessage, error) {
	resp, err := s.post(ctx, mcpRequest{Method: method, Params: params, JSONRPC: "2.0", ID: &id})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ctype := resp.Header.Get("Content-Type")
	want := strconv.Itoa(id)
	pick := func(m mcpMessage) (json.RawMessage, bool, error) {
		if m.ID == nil || strings.TrimSpace(string(*m.ID)) != want {
			return nil, false, nil
		}
		if m.Error != nil {
			return nil, true, &mcpError{code: m.Error.Code}
		}
		return m.Result, true, nil
	}
	switch {
	case strings.Contains(ctype, "text/event-stream"):
		return readMCPStream(resp.Body, pick)
	case strings.Contains(ctype, "application/json"):
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
		if err != nil {
			return nil, err
		}
		var batch []mcpMessage
		if json.Unmarshal(body, &batch) != nil {
			var one mcpMessage
			if err := json.Unmarshal(body, &one); err != nil {
				return nil, &mcpError{syntax: true}
			}
			batch = []mcpMessage{one}
		}
		for _, m := range batch {
			if r, ok, err := pick(m); ok {
				return r, err
			}
		}
	}
	return nil, errors.New("no response")
}

// readMCPStream reads server-sent events until the message for the request arrives.
func readMCPStream(r io.Reader, pick func(mcpMessage) (json.RawMessage, bool, error)) (json.RawMessage, error) {
	br := bufio.NewReader(r)
	var data []string
	flush := func() (json.RawMessage, bool, error) {
		if len(data) == 0 {
			return nil, false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		var m mcpMessage
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			return nil, true, &mcpError{syntax: true}
		}
		return pick(m)
	}
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if res, ok, ferr := flush(); ok {
				return res, ferr
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if err != nil {
			if res, ok, ferr := flush(); ok {
				return res, ferr
			}
			return nil, errors.New("stream ended")
		}
	}
}

func (s *mcpSession) run(ctx context.Context, args *baizhiArguments, apiKey string) (string, error) {
	initResult, err := s.request(ctx, "initialize", mcpInitParams{ProtocolVersion: baizhiProtocol,
		ClientInfo: map[string]any{"name": "pi-web-access-baizhi", "version": "1.0.0"}}, 0)
	if err != nil {
		return "", err
	}
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    *string `json:"name"`
			Version *string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(initResult, &init) != nil || init.Capabilities == nil || init.ServerInfo.Name == nil || init.ServerInfo.Version == nil || !sliceHas(mcpSupportedProtocols, init.ProtocolVersion) {
		return "", errors.New("invalid initialize result")
	}
	s.protocol = init.ProtocolVersion
	notify, err := s.post(ctx, mcpRequest{Method: "notifications/initialized", JSONRPC: "2.0"})
	if err != nil {
		return "", err
	}
	_ = notify.Body.Close()
	var stream sync.WaitGroup
	streamCtx, stopStream := context.WithCancel(ctx)
	defer func() { stopStream(); stream.Wait() }()
	if notify.StatusCode == http.StatusAccepted {
		// The SDK opens the optional server-to-client stream after the initialized notification; a
		// server without one answers 405.
		h := s.headers()
		h.Set("Accept", "text/event-stream")
		stream.Add(1)
		go func() {
			defer stream.Done()
			if resp, err := s.do(streamCtx, "GET", h, nil); err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	raw, err := s.request(ctx, "tools/call", mcpCallParams{Name: "websearch_search", Arguments: args}, 1)
	if err != nil {
		return "", err
	}
	var result struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return "", errors.New("invalid tool result")
	}
	if result.IsError {
		return "", errors.New("Baizhi search tool returned an error")
	}
	// The public discovery snapshot documents inputs but no outputSchema: preserve the MCP payload
	// instead of guessing result item field names.
	var parts []string
	if len(result.StructuredContent) > 0 {
		text, err := jsStringifyIndent(result.StructuredContent)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	for _, c := range result.Content {
		var item struct {
			Type string `json:"type"`
			Text any    `json:"text"`
		}
		if json.Unmarshal(c, &item) != nil {
			continue
		}
		if text, ok := item.Text.(string); ok && item.Type == "text" && jsTrim(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("Baizhi returned invalid response: no search text or structured content")
	}
	escaped, _ := json.Marshal(apiKey)
	return RedactCredential(RedactCredential(strings.Join(parts, "\n\n"), apiKey), string(escaped[1:len(escaped)-1])), nil
}

// terminate is transport.terminateSession(): a DELETE bounded to 1.5s, even after a cancellation.
func (s *mcpSession) terminate(ctx context.Context) {
	if s.sessionID == "" {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), baizhiCleanupMs*time.Millisecond)
	defer cancel()
	if resp, err := s.do(dctx, "DELETE", s.headers(), nil); err == nil {
		_ = resp.Body.Close()
	}
}

// jsStringifyIndent is JSON.stringify(JSON.parse(raw), null, 2): key order kept, strings and
// numbers written the way JavaScript writes them.
func jsStringifyIndent(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := jsWriteValue(&b, dec, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

func jsWriteValue(b *strings.Builder, dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	indent := strings.Repeat("  ", depth+1)
	closing := strings.Repeat("  ", depth)
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			if !dec.More() {
				_, _ = dec.Token()
				b.WriteString("{}")
				return nil
			}
			b.WriteString("{\n")
			first := true
			for dec.More() {
				if !first {
					b.WriteString(",\n")
				}
				first = false
				k, err := dec.Token()
				if err != nil {
					return err
				}
				b.WriteString(indent + jsQuote(k.(string)) + ": ")
				if err := jsWriteValue(b, dec, depth+1); err != nil {
					return err
				}
			}
			_, _ = dec.Token()
			b.WriteString("\n" + closing + "}")
			return nil
		}
		if !dec.More() {
			_, _ = dec.Token()
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		first := true
		for dec.More() {
			if !first {
				b.WriteString(",\n")
			}
			first = false
			b.WriteString(indent)
			if err := jsWriteValue(b, dec, depth+1); err != nil {
				return err
			}
		}
		_, _ = dec.Token()
		b.WriteString("\n" + closing + "]")
	case string:
		b.WriteString(jsQuote(t))
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return err
		}
		b.WriteString(jsNumber(f))
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case nil:
		b.WriteString("null")
	}
	return nil
}

// jsQuote is JSON.stringify for a string (well-formed: no HTML escaping, control characters as
// \b \f \n \r \t or \u00xx).
func jsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else if r >= 0xD800 && r <= 0xDFFF {
				u := utf16.Encode([]rune{r})
				fmt.Fprintf(&b, `\u%04x`, u[0])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
