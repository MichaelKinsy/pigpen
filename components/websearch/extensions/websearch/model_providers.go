package websearch

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Helpers shared by the model-backed providers (openai-search.ts, xai-search.ts, kimi-search.ts,
// mistral-search.ts): what they read from the model registry, the header container that behaves
// like the WHATWG Headers class, and the decoded-JSON accessors.

// ---- decoded JSON -------------------------------------------------------------------------

func mpObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func mpArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func mpString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// mpFinitePositive is `typeof n === "number" && Number.isFinite(n) && n > 0`.
func mpFinitePositive(n *float64) (float64, bool) {
	if n == nil || math.IsNaN(*n) || math.IsInf(*n, 0) || *n <= 0 {
		return 0, false
	}
	return *n, true
}

// mpCap is `Math.min(Math.floor(n), 20)` for a finite positive count.
func mpCap(n float64) int {
	if n >= 20 {
		return 20
	}
	return int(n)
}

// recencyLabels are the prompt labels of the recency filter.
var recencyLabels = map[string]string{"day": "past 24 hours", "week": "past week", "month": "past month", "year": "past year"}

// ---- the model registry -------------------------------------------------------------------

// providerHeaders is `Record<string, string | null>`: a nil value is a JSON null.
type providerHeaders map[string]*string

// registryAuth is the result of ModelRegistry.getApiKeyAndHeaders.
type registryAuth struct {
	OK      bool
	APIKey  string
	Headers providerHeaders
	// BaseURL is resolved.baseUrl; BaseURLSet is false when it is absent or null (the `??` in the
	// original then falls back to the model's baseUrl).
	BaseURL    any
	BaseURLSet bool
	Error      string
}

func parseRegistryAuth(m map[string]any) registryAuth {
	var a registryAuth
	a.OK, _ = m["ok"].(bool)
	a.APIKey, _ = m["apiKey"].(string)
	a.Error = jsString(m["error"])
	if v, present := m["baseUrl"]; present && v != nil {
		a.BaseURL, a.BaseURLSet = v, true
	}
	a.Headers = providerHeaders{}
	if h, ok := mpObject(m["headers"]); ok {
		for name, v := range h {
			switch x := v.(type) {
			case nil:
				a.Headers[name] = nil
			case string:
				s := x
				a.Headers[name] = &s
			default:
				s := jsString(x)
				a.Headers[name] = &s
			}
		}
	}
	return a
}

// registryAuthFor is `await modelRegistry.getApiKeyAndHeaders(model)`; a rejection is the error.
func registryAuthFor(reg ModelRegistry, model map[string]any) (registryAuth, error) {
	m, err := reg.GetApiKeyAndHeaders(model)
	if err != nil {
		return registryAuth{}, err
	}
	return parseRegistryAuth(m), nil
}

// sortedHeaderNames orders a registry's headers deterministically (the host's JSON object does
// not keep the registry's insertion order).
func (h providerHeaders) sortedNames() []string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ---- Headers ------------------------------------------------------------------------------

type headerPair struct{ name, value string }

// headerObject is a plain JS object used as `headers`: exact-key assignment, insertion order.
type headerObject struct{ pairs []headerPair }

func (o *headerObject) set(name, value string) {
	for i := range o.pairs {
		if o.pairs[i].name == name {
			o.pairs[i].value = value
			return
		}
	}
	o.pairs = append(o.pairs, headerPair{name, value})
}

// requestHeaderObject is `toRequestHeaders(headers)`: every non-null registry header.
func requestHeaderObject(h providerHeaders) *headerObject {
	o := &headerObject{}
	for _, name := range h.sortedNames() {
		if v := h[name]; v != nil {
			o.set(name, *v)
		}
	}
	return o
}

// jsHeaders is the WHATWG Headers class: case-insensitive names, `append` joins values with ", ".
type jsHeaders struct {
	order  []string
	values map[string]string
}

func newJSHeaders() *jsHeaders { return &jsHeaders{values: map[string]string{}} }

// headersFromObject is `new Headers(object)`: each key is appended, so two keys that differ only
// in case end up as one header with both values.
func headersFromObject(o *headerObject) *jsHeaders {
	h := newJSHeaders()
	for _, p := range o.pairs {
		h.append(p.name, p.value)
	}
	return h
}

func (h *jsHeaders) append(name, value string) {
	key := strings.ToLower(name)
	if cur, ok := h.values[key]; ok {
		h.values[key] = cur + ", " + value
		return
	}
	h.order = append(h.order, key)
	h.values[key] = value
}

func (h *jsHeaders) set(name, value string) {
	key := strings.ToLower(name)
	if _, ok := h.values[key]; !ok {
		h.order = append(h.order, key)
	}
	h.values[key] = value
}

func (h *jsHeaders) del(name string) {
	key := strings.ToLower(name)
	if _, ok := h.values[key]; !ok {
		return
	}
	delete(h.values, key)
	for i, k := range h.order {
		if k == key {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
}

func (h *jsHeaders) http() http.Header {
	out := http.Header{}
	for _, key := range h.order {
		out[http.CanonicalHeaderKey(key)] = []string{h.values[key]}
	}
	return out
}

// ---- Bearer tokens and ids ----------------------------------------------------------------

var bearerTokenRE = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)

// uuidV4 is crypto.randomUUID().
func uuidV4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ---- String.prototype.localeCompare(b, undefined, { numeric: true }) ----------------------

// icuPunctuation lists the ASCII punctuation in the order of the root collation (CLDR), which is
// the order the default ICU `localeCompare` ranks it: before digits, which rank before letters.
const icuPunctuation = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

type collationClass int

const (
	collPunct collationClass = iota
	collDigit
	collLetter
)

func collationOf(r rune) (collationClass, int) {
	if i := strings.IndexRune(icuPunctuation, r); i >= 0 {
		return collPunct, i
	}
	if r >= '0' && r <= '9' {
		return collDigit, int(r - '0')
	}
	return collLetter, int(toLowerRune(r))
}

func collationLetter(r rune) (rune, bool) {
	if cls, _ := collationOf(r); cls == collLetter {
		return r, true
	}
	return 0, false
}

func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return []rune(strings.ToLower(string(r)))[0]
}

// compareNumericLocale approximates the numeric collation of ICU for the identifiers model ids
// are made of: runs of digits compare by value, then punctuation < digits < letters, letters
// compare case-insensitively first and lower case sorts before upper case among equals. It
// returns <0, 0 or >0. A character outside ASCII ranks as a letter by its lower-cased code point.
func compareNumericLocale(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		ca, cb := ra[i], rb[j]
		if ca >= '0' && ca <= '9' && cb >= '0' && cb <= '9' {
			si, sj := i, j
			for i < len(ra) && ra[i] >= '0' && ra[i] <= '9' {
				i++
			}
			for j < len(rb) && rb[j] >= '0' && rb[j] <= '9' {
				j++
			}
			na, nb := strings.TrimLeft(string(ra[si:i]), "0"), strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			continue
		}
		clsA, ka := collationOf(ca)
		clsB, kb := collationOf(cb)
		if clsA != clsB {
			return int(clsA) - int(clsB)
		}
		if ka != kb {
			return ka - kb
		}
		i++
		j++
	}
	if i < len(ra) {
		return 1
	}
	if j < len(rb) {
		return -1
	}
	// Equal at the primary level: lower case before upper case at the first letter that differs.
	var la, lb []rune
	for _, r := range ra {
		if _, isLetter := collationLetter(r); isLetter {
			la = append(la, r)
		}
	}
	for _, r := range rb {
		if _, isLetter := collationLetter(r); isLetter {
			lb = append(lb, r)
		}
	}
	for k := 0; k < len(la) && k < len(lb); k++ {
		if la[k] != lb[k] {
			if la[k] > lb[k] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// base64URLDecodeLax is Buffer.from(padded, "base64") on a base64url segment: the URL alphabet
// is mapped to the standard one and padding is optional.
func base64URLDecodeLax(s string) ([]byte, error) {
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	s = strings.TrimRight(s, "=")
	return base64.RawStdEncoding.DecodeString(s)
}

// plainFetchCredentialHeaders are the headers Node's fetch drops when a redirect leaves the
// origin (the providers that call plain `fetch` rely on it, not on fetchWithCredentialRedirects).
var plainFetchCredentialHeaders = []string{"Authorization", "Cookie", "Proxy-Authorization"}

// decodeResponseJSON is `response.json()`: any JSON value, or the decoder's error text.
func decodeResponseJSON(text string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, errors.New(jsonErrorText(err))
	}
	return v, nil
}

// encodeJSONBody is JSON.stringify: no HTML escaping.
func encodeJSONBody(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}

// isTimeout reports a request that ran out of time (the original's TimeoutError).
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
