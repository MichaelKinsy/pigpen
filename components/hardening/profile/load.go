package profile

import (
	"bytes"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/hostname"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/ident"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/netrange"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/safefile"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/strictjson"
)

// SchemaVersion is the profile schema this library reads. A file may carry "schema": 1.
const SchemaVersion = 1

// Dir is the directory below the agent directory that holds the profile files.
const Dir = "pigpen-enterprise"

// EnvPrefix returns "PIGPEN_<PACKAGE>_ENTERPRISE" for a Package name (hyphens become underscores). The variable of
// that name, when set, names another profile file. Each flag has the variable EnvPrefix + "_" + EnvFlagName.
func EnvPrefix(pkg string) string {
	return "PIGPEN_" + strings.ToUpper(strings.ReplaceAll(pkg, "-", "_")) + "_ENTERPRISE"
}

// EnvFlagName returns the upper-case suffix of a flag's environment variable, for example CREDENTIAL_FILE.
func EnvFlagName(flag string) string {
	var b strings.Builder
	for i, r := range flag {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte('_')
		}
		b.WriteString(strings.ToUpper(string(r)))
	}
	return b.String()
}

// AgentDir returns the agent directory: PIG_CODING_AGENT_DIR, else $PIG_HOME/agent, else "" (none).
// It never falls back to a Pi directory.
func AgentDir(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	if d := getenv("PIG_CODING_AGENT_DIR"); d != "" {
		return d
	}
	if h := getenv("PIG_HOME"); h != "" {
		return filepath.Join(h, "agent")
	}
	return ""
}

// Load reads <agentDir>/pigpen-enterprise/<pkg>.json and the PIGPEN_<PKG>_ENTERPRISE* environment. It does no
// network work and starts nothing. It opens the profile path at most once.
//
//   - No file and no flag variable: every flag is off, with no error.
//   - A file that exists and cannot be read, or does not parse, has an unknown or repeated key, or is larger than
//     64 KiB: *Error with ProfileMisconfigured. The returned Flags then has every flag off and reports the error
//     in Status.
//   - A flag that is on without the configuration it needs (credentialFile without a file name, provider and
//     origin, egressPolicy without an allow-list, resourceServer without issuer, audience and JWKS URL), or with an
//     unreadable proxy or CA bundle: *Error with ProfileMisconfigured.
//   - An environment variable can turn a flag on ("1" or "true"), never off. "", "0", "false", "no" and "off"
//     leave the file's setting; any other value is ProfileMisconfigured.
//
// The profile is never read from the working directory or a project .pig directory: agentDir and the
// PIGPEN_<PKG>_ENTERPRISE path must be absolute.
func Load(pkg, agentDir string, getenv func(string) string) (Flags, error) {
	return load(pkg, agentDir, getenv, safefile.Read)
}

func load(pkg, agentDir string, getenv func(string) string, read func(string, int64) ([]byte, error)) (Flags, error) {
	f := Flags{pkg: pkg}
	fail := func(flag string) (Flags, error) {
		f = Flags{pkg: pkg, found: f.found, lastErr: ProfileMisconfigured}
		return f, NewError(ProfileMisconfigured, flag, pkg)
	}
	if !ident.Package(pkg) {
		f.pkg = ""
		f.lastErr = ProfileMisconfigured
		return f, NewError(ProfileMisconfigured, "", "invalid")
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	prefix := EnvPrefix(pkg)

	// The path.
	path, explicit := "", false
	if p := getenv(prefix); p != "" {
		if !filepath.IsAbs(p) {
			return fail("")
		}
		path, explicit = p, true
	} else if agentDir != "" {
		if !filepath.IsAbs(agentDir) {
			return fail("")
		}
		path = filepath.Join(agentDir, Dir, pkg+".json")
	}

	// The file.
	var raw rawFile
	if path != "" {
		data, err := read(path, MaxProfileBytes)
		switch {
		case err == nil:
			f.found = true
			// null decodes into a struct without error: the profile must be an object.
			if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
				return fail("")
			}
			if err := strictjson.Decode(data, &raw, true); err != nil {
				return fail("")
			}
		case errors.Is(err, safefile.ErrNotExist) && !explicit:
			// No profile: every flag stays off.
		default:
			f.found = !errors.Is(err, safefile.ErrNotExist)
			return fail("")
		}
	}
	if raw.Schema != nil && *raw.Schema != SchemaVersion {
		return fail("")
	}

	// The environment can only turn a flag on. A value that is neither an on-value nor an off-value is refused:
	// leaving the flag off would degrade a flag the operator may have meant to turn on.
	envOn := map[string]bool{}
	for _, flag := range FlagNames {
		switch strings.ToLower(getenv(prefix + "_" + EnvFlagName(flag))) {
		case "1", "true":
			envOn[flag] = true
		case "", "0", "false", "no", "off":
		default:
			return fail(flag)
		}
	}
	on := func(flag string, enabled *bool) bool { return (enabled != nil && *enabled) || envOn[flag] }

	var out Flags
	out.pkg, out.found = pkg, f.found
	var err error
	if r := raw.CredentialFile; on(FlagCredentialFile, r.enabledPtr()) {
		if out.CredentialFile, err = buildCredential(r); err != nil {
			return fail(FlagCredentialFile)
		}
	}
	if r := raw.EgressPolicy; on(FlagEgressPolicy, r.enabledPtr()) {
		if out.EgressPolicy, err = buildEgress(r, read); err != nil {
			return fail(FlagEgressPolicy)
		}
	}
	if r := raw.Audit; on(FlagAudit, r.enabledPtr()) {
		if out.Audit, err = buildAudit(r); err != nil {
			return fail(FlagAudit)
		}
	}
	if r := raw.ResourceServer; on(FlagResourceServer, r.enabledPtr()) {
		if out.ResourceServer, err = buildResource(r); err != nil {
			return fail(FlagResourceServer)
		}
	}
	if r := raw.PolicyFailClosed; on(FlagPolicyFailClosed, r.enabledPtr()) {
		out.PolicyFailClosed = true
		if r != nil && r.PolicyFile != nil {
			if !filepath.IsAbs(*r.PolicyFile) {
				return fail(FlagPolicyFailClosed)
			}
			out.PolicyFile = *r.PolicyFile
		}
	}
	if r := raw.Headless; on(FlagHeadless, r.enabledPtr()) {
		out.Headless = true
	}
	return out, nil
}

type rawFile struct {
	Schema           *int           `json:"schema"`
	CredentialFile   *rawCredential `json:"credentialFile"`
	EgressPolicy     *rawEgress     `json:"egressPolicy"`
	Audit            *rawAudit      `json:"audit"`
	ResourceServer   *rawResource   `json:"resourceServer"`
	PolicyFailClosed *rawPolicy     `json:"policyFailClosed"`
	Headless         *rawHeadless   `json:"headless"`
}

type rawCredential struct {
	Enabled             *bool   `json:"enabled"`
	Path                *string `json:"path"`
	Provider            *string `json:"provider"`
	Origin              *string `json:"origin"`
	Header              *string `json:"header"`
	Scheme              *string `json:"scheme"`
	ExpiryMarginSeconds *int    `json:"expiryMarginSeconds"`
}

type rawEgress struct {
	Enabled    *bool    `json:"enabled"`
	Allow      []string `json:"allow"`
	AllowCIDRs []string `json:"allowCIDRs"`
	Proxy      *string  `json:"proxy"`
	NoProxy    []string `json:"noProxy"`
	CABundle   *string  `json:"caBundle"`
}

type rawAudit struct {
	Enabled  *bool   `json:"enabled"`
	Sink     *string `json:"sink"`
	Required *bool   `json:"required"`
}

type rawResource struct {
	Enabled       *bool    `json:"enabled"`
	Issuer        *string  `json:"issuer"`
	Audience      *string  `json:"audience"`
	JWKSURL       *string  `json:"jwksURL"`
	Algorithms    []string `json:"algorithms"`
	LeewaySeconds *int     `json:"leewaySeconds"`
	SubjectClaim  *string  `json:"subjectClaim"`
	TenantClaim   *string  `json:"tenantClaim"`
}

type rawPolicy struct {
	Enabled    *bool   `json:"enabled"`
	PolicyFile *string `json:"policyFile"`
}

type rawHeadless struct {
	Enabled *bool `json:"enabled"`
}

func (r *rawCredential) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}
func (r *rawEgress) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}
func (r *rawAudit) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}
func (r *rawResource) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}
func (r *rawPolicy) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}
func (r *rawHeadless) enabledPtr() *bool {
	if r == nil {
		return nil
	}
	return r.Enabled
}

var errInvalid = errors.New("invalid")

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// secureURL reports whether u is an http(s) URL without user information, query or fragment, with an acceptable
// host, that is https or names a loopback host.
func secureURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return nil, errInvalid
	}
	o, err := hostname.OriginOf(u)
	if err != nil {
		return nil, errInvalid
	}
	if o.Scheme != "https" && !hostname.IsLoopback(o.Host) {
		return nil, errInvalid
	}
	return u, nil
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0) {
			return false
		}
	}
	return true
}

var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Content-Type": true, "Transfer-Encoding": true, "Connection": true, "Upgrade": true,
	"Te": true, "Trailer": true, "Proxy-Authorization": true, "Proxy-Connection": true, "Cookie": true, "Set-Cookie": true,
	"Keep-Alive": true, "Expect": true,
}

func buildCredential(r *rawCredential) (*CredentialFile, error) {
	if r == nil {
		return nil, errInvalid
	}
	c := &CredentialFile{Path: str(r.Path), Provider: str(r.Provider), Header: "Authorization", ExpiryMargin: DefaultExpiryMargin}
	if !filepath.IsAbs(c.Path) || !ident.Tool(c.Provider) {
		return nil, errInvalid
	}
	u, err := secureURL(str(r.Origin))
	if err != nil {
		return nil, err
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errInvalid
	}
	o, err := hostname.OriginOf(u)
	if err != nil {
		return nil, errInvalid
	}
	c.Origin = o.String()
	if r.Header != nil {
		if !validToken(*r.Header) {
			return nil, errInvalid
		}
		c.Header = http.CanonicalHeaderKey(*r.Header)
	}
	if forbiddenHeaders[c.Header] {
		return nil, errInvalid
	}
	if r.Scheme != nil {
		if *r.Scheme != "" && !validToken(*r.Scheme) {
			return nil, errInvalid
		}
		c.Scheme = *r.Scheme
	} else if c.Header == "Authorization" {
		c.Scheme = "Bearer"
	}
	if r.ExpiryMarginSeconds != nil {
		s := *r.ExpiryMarginSeconds
		if s < 0 || time.Duration(s)*time.Second > MaxExpiryMargin {
			return nil, errInvalid
		}
		c.ExpiryMargin = time.Duration(s) * time.Second
	}
	return c, nil
}

func buildEgress(r *rawEgress, read func(string, int64) ([]byte, error)) (*EgressPolicy, error) {
	if r == nil || len(r.Allow) == 0 {
		return nil, errInvalid
	}
	e := &EgressPolicy{}
	seen := map[string]bool{}
	for _, a := range r.Allow {
		n, err := hostname.ParseEntry(a)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			e.Allow = append(e.Allow, n)
		}
	}
	for _, c := range r.AllowCIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, errInvalid
		}
		if p, err = netrange.NormalizePrefix(p); err != nil {
			return nil, errInvalid
		}
		e.AllowCIDRs = append(e.AllowCIDRs, p)
	}
	for _, n := range r.NoProxy {
		entry, err := hostname.ParseEntry(n)
		if err != nil {
			return nil, err
		}
		e.NoProxy = append(e.NoProxy, entry)
	}
	if r.Proxy != nil && *r.Proxy != "" {
		p, host, err := canonicalProxy(*r.Proxy)
		if err != nil {
			return nil, err
		}
		// The proxy is a destination like any other: it must be on the allow-list (and pass the address rules
		// when it is dialled).
		if !listed(e.Allow, host) {
			return nil, errInvalid
		}
		e.Proxy = p
	}
	if r.CABundle != nil && *r.CABundle != "" {
		if !filepath.IsAbs(*r.CABundle) {
			return nil, errInvalid
		}
		data, err := read(*r.CABundle, MaxCABundleBytes)
		if err != nil || !x509.NewCertPool().AppendCertsFromPEM(data) {
			return nil, errInvalid
		}
		e.CABundle = *r.CABundle
	}
	return e, nil
}

func listed(allow []string, host string) bool {
	for _, a := range allow {
		if hostname.Match(a, host) {
			return true
		}
	}
	return false
}

// canonicalProxy validates a proxy URL and returns "scheme://host:port" and the normalised host.
func canonicalProxy(s string) (string, string, error) {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", "", errInvalid
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", "", errInvalid
	}
	host, err := hostname.Normalize(u.Hostname())
	if err != nil {
		return "", "", errInvalid
	}
	port := u.Port() // a proxy is named with its port
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", "", errInvalid
	}
	return scheme + "://" + net.JoinHostPort(host, port), host, nil
}

func buildAudit(r *rawAudit) (*Audit, error) {
	a := &Audit{Sink: SinkStderr}
	if r == nil {
		return a, nil
	}
	if r.Sink != nil {
		switch *r.Sink {
		case SinkStderr, SinkSessionFile:
			a.Sink = *r.Sink
		default:
			return nil, errInvalid
		}
	}
	a.Required = r.Required != nil && *r.Required
	return a, nil
}

var allowedAlgorithms = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// AllowedAlgorithms lists the signature algorithms resourceServer may be configured with. HS* and none are never in it.
func AllowedAlgorithms() []string { return append([]string(nil), allowedAlgorithms...) }

func validClaimName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func buildResource(r *rawResource) (*ResourceServer, error) {
	if r == nil || str(r.Issuer) == "" || str(r.Audience) == "" || str(r.JWKSURL) == "" {
		return nil, errInvalid
	}
	rs := &ResourceServer{Issuer: *r.Issuer, Audience: *r.Audience, JWKSURL: *r.JWKSURL, Leeway: DefaultLeeway, SubjectClaim: "sub", TenantClaim: "tenant"}
	if len(rs.Issuer) > 512 || len(rs.Audience) > 512 || !printable(rs.Issuer) || !printable(rs.Audience) {
		return nil, errInvalid
	}
	u, err := secureURL(rs.JWKSURL)
	if err != nil {
		return nil, err
	}
	rs.JWKSURL = u.String()
	rs.Algorithms = []string{"RS256"}
	if len(r.Algorithms) > 0 {
		rs.Algorithms = nil
		seen := map[string]bool{}
		for _, a := range r.Algorithms {
			ok := false
			for _, k := range allowedAlgorithms {
				ok = ok || k == a
			}
			if !ok {
				return nil, errInvalid
			}
			if !seen[a] {
				seen[a] = true
				rs.Algorithms = append(rs.Algorithms, a)
			}
		}
	}
	if r.LeewaySeconds != nil {
		s := *r.LeewaySeconds
		if s < 0 || time.Duration(s)*time.Second > MaxLeeway {
			return nil, errInvalid
		}
		rs.Leeway = time.Duration(s) * time.Second
	}
	if r.SubjectClaim != nil {
		rs.SubjectClaim = *r.SubjectClaim
	}
	if r.TenantClaim != nil {
		rs.TenantClaim = *r.TenantClaim
	}
	if !validClaimName(rs.SubjectClaim) || !validClaimName(rs.TenantClaim) {
		return nil, errInvalid
	}
	return rs, nil
}

func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
