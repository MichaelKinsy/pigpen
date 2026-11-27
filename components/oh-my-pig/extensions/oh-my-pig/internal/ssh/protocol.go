package ssh

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/internal-urls/ssh-protocol.ts

//go:embed prompts/ssh.md
var sshPromptDoc string

//go:embed prompts/ssh-reference.md
var sshReferenceDoc string

// Handler implements the `ssh://` internal URL protocol handler.
// upstream: packages/coding-agent/src/internal-urls/ssh-protocol.ts SshProtocolHandler
type Handler struct {
	Manager *Manager
}

// Register installs the `ssh://` URL protocol handler on the global router and
// wires session-end ControlMaster teardown via internal/lifecycle.
func Register(_ *sdk.Extension) {
	_ = urls.Register(&Handler{Manager: defaultManager})
	lifecycle.OnEnd("ssh", func(sessionID string) {
		defaultManager.CloseSession(sessionID)
	})
}

func (h *Handler) mgr() *Manager {
	if h != nil && h.Manager != nil {
		return h.Manager
	}
	return defaultManager
}

// Scheme returns "ssh".
func (*Handler) Scheme() string { return "ssh" }

// Spec declares `ssh://`'s URL router contract.
// upstream: ssh-protocol.ts:264-271
func (*Handler) Spec() urls.Spec {
	return urls.Spec{
		Backing:       urls.BackingRemote,
		Selectors:     urls.SelectorsLines,
		PortAuthority: true,
		Immutable:     false,
		ReadTier:      "exec",
		Write: &urls.WritePolicy{
			Via:     "handler",
			Payload: "text",
			Scope:   "workspace",
		},
	}
}

// PromptDoc advertises `ssh://` only when an `ssh` client is available on PATH
// (matching omp's `$which("ssh") ? sshDoc.trim() : undefined`).
// upstream: ssh-protocol.ts:274-276
func (h *Handler) PromptDoc(_ urls.SchemeHost) string {
	if !h.mgr().HasSSHBinary() {
		return ""
	}
	return strings.TrimSpace(sshPromptDoc)
}

// PromptReference returns the `help://read/urls` grammar reference when `ssh` is on PATH.
// upstream: ssh-protocol.ts:278-280
func (h *Handler) PromptReference() string {
	if !h.mgr().HasSSHBinary() {
		return ""
	}
	return strings.TrimSpace(sshReferenceDoc)
}

func contextForCall(c urls.Context) (context.Context, context.CancelFunc, string) {
	ctx, cancel := context.WithCancel(context.Background())
	sid := ""
	if c.SDK != (sdk.Context{}) {
		if doneCh := c.SDK.Done(); doneCh != nil {
			go func() {
				select {
				case <-doneCh:
					cancel()
				case <-ctx.Done():
				}
			}()
		}
		if id, err := c.SDK.GetSessionID(); err == nil {
			sid = strings.TrimSpace(id)
		}
	}
	return ctx, cancel, sid
}

// ── Authority & path parsing ─────────────────────────────────────────────────
// upstream: ssh-protocol.ts:57-260

var rawAuthorityRE = regexp.MustCompile(`(?i)^ssh:\/\/([^/?#]*)`)

// parsedAuthority mirrors the WHATWG URL authority decomposition for `ssh://`
// without failing open on malformed ports, empty userinfo, or bad escapes.
type parsedAuthority struct {
	rawEncoded string // undecoded text between `ssh://` and `/`, `?`, `#`
	username   string // undecoded username (before literal `@`)
	hasUser    bool
	password   string // undecoded password (after literal `:` in userinfo)
	hasPass    bool
	hostname   string // undecoded host (bracketed for IPv6)
	port       string // digits after literal `:`
	hasPort    bool
	isIPv6     bool
}

func parseSSHAuthority(href string) (parsedAuthority, error) {
	m := rawAuthorityRE.FindStringSubmatch(href)
	if m == nil {
		return parsedAuthority{}, fmt.Errorf("ssh:// requires a host: ssh://<host>/<absolute-path>")
	}
	raw := m[1]
	pa := parsedAuthority{rawEncoded: raw}
	if raw == "" {
		return pa, nil
	}

	// Reject ASCII whitespace or control chars anywhere in raw authority.
	for i := range len(raw) {
		if raw[i] <= 0x20 || raw[i] == 0x7f {
			return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
		}
	}

	hostPort := raw
	// Split userinfo at the LAST literal `@` (matching WHATWG URL).
	if at := strings.LastIndexByte(raw, '@'); at != -1 {
		userinfo := raw[:at]
		hostPort = raw[at+1:]
		if colon := strings.IndexByte(userinfo, ':'); colon != -1 {
			pa.username = userinfo[:colon]
			pa.password = userinfo[colon+1:]
			pa.hasUser = pa.username != ""
			pa.hasPass = true
		} else {
			pa.username = userinfo
			pa.hasUser = userinfo != ""
		}
		if !pa.hasPass && !pa.hasUser {
			// Literal empty username: `ssh://@host`
			return parsedAuthority{}, fmt.Errorf("ssh://: empty username in %q; drop the leading '@' or provide a username before it", href)
		}
	}

	if pa.hasPass {
		if pa.password != "" {
			return parsedAuthority{}, errors.New("ssh://: password authentication is not supported; ssh:// uses key/agent auth — drop the ':<password>' from the URL")
		}
		// Empty password like `ssh://user:@host` or `ssh://:@host`.
		return parsedAuthority{}, fmt.Errorf("ssh://: unsupported or malformed authority in %q; use ssh://[user@]host[:1-65535]/<absolute-path>", href)
	}

	if hostPort == "" {
		return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
	}

	// Parse bracketed IPv6 literal `[::1]` or `[::1]:2222`.
	if strings.HasPrefix(hostPort, "[") {
		closeBracket := strings.IndexByte(hostPort, ']')
		if closeBracket == -1 {
			return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
		}
		inner := hostPort[1:closeBracket]
		if _, err := netip.ParseAddr(inner); err != nil {
			return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
		}
		pa.hostname = hostPort[:closeBracket+1]
		pa.isIPv6 = true
		rest := hostPort[closeBracket+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
			}
			pa.hasPort = true
			pa.port = rest[1:]
		}
	} else {
		if strings.ContainsAny(hostPort, "[]") {
			return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
		}
		if colon := strings.LastIndexByte(hostPort, ':'); colon != -1 {
			// Multiple colons outside brackets is invalid WHATWG URL.
			if strings.IndexByte(hostPort, ':') != colon {
				return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
			}
			pa.hostname = hostPort[:colon]
			pa.hasPort = true
			pa.port = hostPort[colon+1:]
		} else {
			pa.hostname = hostPort
		}
	}

	if pa.hostname == "" {
		return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
	}

	// Validate percent-escapes strictly in username and hostname.
	for _, part := range []string{pa.username, pa.hostname} {
		if strings.Contains(part, "%") {
			if _, err := strictPercentDecode(part); err != nil {
				return parsedAuthority{}, fmt.Errorf("ssh://: invalid percent-escape in authority %q", href)
			}
		}
	}

	if pa.hasPort {
		if pa.port == "" {
			return parsedAuthority{}, fmt.Errorf("ssh://: empty port in %q; use ssh://host:<1-65535>/<path> or drop the colon", href)
		}
		for i := range len(pa.port) {
			if pa.port[i] < '0' || pa.port[i] > '9' {
				return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
			}
		}
		portNum, err := strconv.Atoi(pa.port)
		if err != nil || portNum > 65535 {
			return parsedAuthority{}, fmt.Errorf("ssh://: invalid host or port in %q; use ssh://host[:1-65535]/<absolute-path>", href)
		}
		if portNum == 0 {
			return parsedAuthority{}, errors.New("ssh://: port 0 is not a valid SSH port; use ssh://host:<1-65535>/<path> or omit the port")
		}
	}

	return pa, nil
}

// strictPercentDecode decodes %XX sequences strictly (unlike url.PathUnescape
// which accepts bare '%' in some contexts, url.PathUnescape already rejects
// malformed %XX, and does not touch '+').
func strictPercentDecode(s string) (string, error) {
	return url.PathUnescape(s)
}

func (h *Handler) loadConfiguredHosts(cwd string) []Host {
	opts := ConfigOptions{Cwd: cwd}
	if m := h.mgr(); m.ConfigOpts != nil {
		opts = *m.ConfigOpts
		if opts.Cwd == "" {
			opts.Cwd = cwd
		}
	}
	hosts, _ := DiscoverHosts(opts)
	return hosts
}

// resolveTarget maps the `ssh://` URL authority to a ConnectionTarget.
// upstream: ssh-protocol.ts:132-261
func (h *Handler) resolveTarget(u urls.URL, cwd string) (ConnectionTarget, error) {
	href := u.RawHref
	if href == "" {
		href = u.Href
	}
	pa, err := parseSSHAuthority(href)
	if err != nil {
		return ConnectionTarget{}, err
	}
	if pa.rawEncoded == "" {
		return ConnectionTarget{}, errors.New("ssh:// requires a host: ssh://<host>/<absolute-path>")
	}

	decodedRawAuthority, err := strictPercentDecode(pa.rawEncoded)
	if err != nil {
		return ConnectionTarget{}, fmt.Errorf("ssh://: invalid percent-escape in authority %q", href)
	}
	decodedHost, err := strictPercentDecode(pa.hostname)
	if err != nil {
		return ConnectionTarget{}, fmt.Errorf("ssh://: invalid percent-escape in authority %q", href)
	}
	decodedUser := ""
	if pa.hasUser {
		decodedUser, err = strictPercentDecode(pa.username)
		if err != nil {
			return ConnectionTarget{}, fmt.Errorf("ssh://: invalid percent-escape in authority %q", href)
		}
	}
	portNum := 0
	if pa.hasPort {
		portNum, _ = strconv.Atoi(pa.port)
	}

	sshHost := decodedHost
	if pa.isIPv6 {
		sshHost = pa.hostname[1 : len(pa.hostname)-1]
	}

	items := h.loadConfiguredHosts(cwd)

	// Explicit literal user/port override in the URL.
	if pa.hasUser || pa.hasPort {
		for _, entry := range items {
			if entry.Name != pa.hostname && entry.Name != decodedHost {
				continue
			}
			// A ~/.ssh/config alias (host === name, nothing pinned) accepts overrides.
			isPlainAlias := entry.Host == entry.Name &&
				entry.Username == "" &&
				entry.Port == 0 &&
				entry.KeyPath == "" &&
				entry.Compat == nil
			if !isPlainAlias {
				return ConnectionTarget{}, fmt.Errorf(
					"ssh://: user/port overrides are not allowed for the configured host %q; use ssh://%s/<path> or an unconfigured hostname",
					decodedHost, pa.hostname,
				)
			}
		}
		name := formatTargetName(decodedUser, sshHost, portNum)
		return ConnectionTarget{
			Name:     name,
			Host:     sshHost,
			Username: decodedUser,
			Port:     portNum,
		}, nil
	}

	// No explicit user/port: match full decoded authority against configured host names.
	for _, entry := range items {
		if entry.Name == decodedRawAuthority {
			return ConnectionTarget{
				Name:     entry.Name,
				Host:     entry.Host,
				Username: entry.Username,
				Port:     entry.Port,
				KeyPath:  entry.KeyPath,
				Compat:   entry.Compat,
			}, nil
		}
	}
	for _, entry := range items {
		if entry.Name == pa.hostname {
			return ConnectionTarget{
				Name:     entry.Name,
				Host:     entry.Host,
				Username: entry.Username,
				Port:     entry.Port,
				KeyPath:  entry.KeyPath,
				Compat:   entry.Compat,
			}, nil
		}
	}

	// Opaque OpenSSH destination.
	hostVal := decodedRawAuthority
	if pa.isIPv6 {
		hostVal = sshHost
	}
	return ConnectionTarget{
		Name: decodedRawAuthority,
		Host: hostVal,
	}, nil
}

func formatTargetName(user, host string, port int) string {
	var b strings.Builder
	if user != "" {
		b.WriteString(user)
		b.WriteByte('@')
	}
	b.WriteString(host)
	if port > 0 {
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(port))
	}
	return b.String()
}

func hasRawSearchOrHash(u urls.URL) (hasSearch, hasHash bool) {
	href := u.RawHref
	if href == "" {
		href = u.Href
	}
	if idx := strings.IndexByte(href, '#'); idx != -1 {
		hasHash = true
		href = href[:idx]
	}
	if strings.IndexByte(href, '?') != -1 || u.RawQuery != "" {
		hasSearch = true
	}
	return hasSearch, hasHash
}

// remotePathFromURL extracts and decodes the remote absolute path from u,
// resolving `/~` and `/~/...` against the connected remote account's $HOME.
// upstream: ssh-protocol.ts:57-97
func (h *Handler) remotePathFromURL(ctx context.Context, u urls.URL, target ConnectionTarget, sessionID string) (string, error) {
	href := u.RawHref
	if href == "" {
		href = u.Href
	}
	hasSearch, hasHash := hasRawSearchOrHash(u)
	if hasSearch {
		return "", fmt.Errorf("ssh:// does not support URL query strings; percent-encode a literal '?' as %%3F in the path: %s", href)
	}
	if hasHash {
		return "", fmt.Errorf("ssh:// does not support URL fragments; percent-encode a literal '#' as %%23 in the path: %s", href)
	}
	raw := u.Path
	homeRelative := raw == "/~" || strings.HasPrefix(raw, "/~/")
	decoded, err := strictPercentDecode(raw)
	if err != nil {
		return "", fmt.Errorf("Invalid URL encoding in ssh:// path: %s", href)
	}
	if decoded == "" {
		return "", errors.New("ssh:// requires an absolute path, e.g. ssh://host/etc/hosts or ssh://host/ for the root directory")
	}
	if homeRelative {
		home, err := h.mgr().RemoteHomeDirectory(ctx, target, RemoteOpOptions{SessionID: sessionID})
		if err != nil {
			return "", err
		}
		joined := strings.TrimRight(home, "/") + decoded[2:]
		if joined == "" {
			return "/", nil
		}
		return joined, nil
	}
	if strings.HasPrefix(raw, "/~") && !strings.HasPrefix(decoded, "/~/") {
		return "", errors.New("ssh://: named-user /~user paths are unsupported; use /~/ for the connected account's HOME")
	}
	return decoded, nil
}

// ── Rendering helpers ────────────────────────────────────────────────────────

func hostAddress(host Host) string {
	return formatTargetName(host.Username, host.Host, host.Port)
}

func formatHostIndex(hosts []Host) string {
	if len(hosts) == 0 {
		return "# SSH hosts\n\nNo SSH hosts are configured. Add hosts to an `ssh.json` capability file or as `Host` entries in `~/.ssh/config`, or read `ssh://<host>/<path>` with any destination OpenSSH can resolve.\n"
	}
	lines := make([]string, len(hosts))
	for i, host := range hosts {
		addr := hostAddress(host)
		suffix := ""
		if addr != host.Name {
			suffix = fmt.Sprintf(" — `%s`", addr)
		}
		desc := ""
		if host.Description != "" {
			desc = fmt.Sprintf(" (%s)", host.Description)
		}
		lines[i] = fmt.Sprintf("- [%s](ssh://%s/)%s%s", host.Name, url.PathEscape(host.Name), suffix, desc)
	}
	plural := "s"
	if len(hosts) == 1 {
		plural = ""
	}
	return fmt.Sprintf("# SSH hosts\n\n%d configured host%s:\n\n%s\n", len(hosts), plural, strings.Join(lines, "\n"))
}

func formatDirListing(entries []RemoteDirEntry) string {
	if len(entries) == 0 {
		return "(empty directory)"
	}
	cp := append([]RemoteDirEntry(nil), entries...)
	sort.Slice(cp, func(i, j int) bool {
		di := cp[i].FileType == RemoteTypeDir
		dj := cp[j].FileType == RemoteTypeDir
		if di != dj {
			return di
		}
		return compareLocale(cp[i].Name, cp[j].Name) < 0
	})
	lines := make([]string, len(cp))
	for i, e := range cp {
		if e.FileType == RemoteTypeDir {
			lines[i] = e.Name + "/"
		} else {
			lines[i] = e.Name
		}
	}
	return strings.Join(lines, "\n")
}
func contentTypeForRemotePath(remotePath string) string {
	// upstream: packages/coding-agent/src/internal-urls/filesystem-resource.ts:9-13,
	//           packages/tui/src/lang-from-path.ts:109-114,230-231
	base := remotePath
	if idx := strings.LastIndexByte(base, '/'); idx != -1 {
		base = base[idx+1:]
	}
	// A dotfile with no basename before the dot (e.g. `.json`, `.md`) has no extension
	// per Node's path.extname / isMarkdownPath.
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		return "text/plain"
	}
	switch strings.ToLower(base[dot:]) {
	case ".md", ".markdown", ".mdx", ".mdc", ".mkd", ".mdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}

// UTF-8 text files (≤ 1 MiB).
// upstream: ssh-protocol.ts:282-338
func (h *Handler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	href := u.RawHref
	if href == "" {
		href = u.Href
	}
	rawAuth := ""
	if m := rawAuthorityRE.FindStringSubmatch(href); m != nil {
		rawAuth = m[1]
	}
	// Bare `ssh://` (or `ssh:///`) with no host lists configured hosts; a host-less
	// URL with a path (`ssh:///etc/hosts`) is rejected.
	if rawAuth == "" && u.Host == "" {
		if u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("ssh:// requires a host before the path: ssh://<host>%s (host-less ssh://%s is not valid)", u.Path, u.Path)
		}
		content := formatHostIndex(h.loadConfiguredHosts(c.Cwd))
		imm := true
		return &urls.Resource{
			URL:         u.Href,
			Content:     content,
			ContentType: "text/markdown",
			Immutable:   &imm,
		}, nil
	}

	ctx, cancel, sid := contextForCall(c)
	defer cancel()
	target, err := h.resolveTarget(u, c.Cwd)
	if err != nil {
		return nil, err
	}
	// Validate target before any remote command so hostile hosts fail immediately.
	if _, err := BuildSSHTarget(target.Username, target.Host); err != nil {
		return nil, err
	}
	remotePath, err := h.remotePathFromURL(ctx, u, target, sid)
	if err != nil {
		return nil, err
	}

	// Classify before reading so FIFOs/devices fail fast without blocking `head`.
	kind, statErr := h.mgr().StatRemotePath(ctx, target, remotePath, RemoteOpOptions{SessionID: sid})
	if statErr == nil {
		if kind == RemotePathDirectory {
			return h.resolveDirectory(ctx, target, remotePath, u, sid, c.SkipDirectoryListing)
		}
		if kind == RemotePathOther {
			return nil, fmt.Errorf(
				"ssh://: %s is not a regular file (FIFO, socket, or device); ssh:// reads UTF-8 text files only — use `bash` with a remote SSH command for special files",
				remotePath,
			)
		}
	}

	fileRes, err := h.mgr().ReadRemoteFile(ctx, target, remotePath, RemoteFileReadOptions{
		MaxBytes:  MaxTextFileBytes,
		SessionID: sid,
	})
	if err != nil {
		return nil, err
	}
	if fileRes.Truncated {
		return nil, fmt.Errorf(
			"ssh://: %s exceeds the 1 MiB limit; ssh:// supports text files up to 1 MiB — use an sshfs mount for larger files",
			remotePath,
		)
	}
	content, valid := DecodeUTF8Text(fileRes.Bytes)
	if !valid {
		return nil, fmt.Errorf(
			"ssh://: %s is a binary or non-UTF-8 file; ssh:// supports UTF-8 text only — use `bash` with a remote SSH command or an `sshfs` mount",
			remotePath,
		)
	}
	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: contentTypeForRemotePath(remotePath),
	}, nil
}

func (h *Handler) resolveDirectory(ctx context.Context, target ConnectionTarget, remotePath string, u urls.URL, sessionID string, skipListing bool) (*urls.Resource, error) {
	content := ""
	if !skipListing {
		entries, err := h.mgr().ListRemoteDir(ctx, target, remotePath, RemoteOpOptions{SessionID: sessionID})
		if err != nil {
			return nil, err
		}
		content = formatDirListing(entries)
	}
	imm := true
	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/plain",
		Immutable:   &imm,
		IsDirectory: true,
	}, nil
}

// ReadDirectory implements urls.DirectoryReader so `grep`, `glob`, and `find`
// traverse remote directories directly under `ssh://` URLs without reading
// rendered directory listing text.
// upstream: ssh-protocol.ts:368-372
func (h *Handler) ReadDirectory(c urls.Context, u urls.URL) ([]urls.DirEntry, error) {
	ctx, cancel, sid := contextForCall(c)
	defer cancel()
	target, err := h.resolveTarget(u, c.Cwd)
	if err != nil {
		return nil, err
	}
	if _, err := BuildSSHTarget(target.Username, target.Host); err != nil {
		return nil, err
	}
	remotePath, err := h.remotePathFromURL(ctx, u, target, sid)
	if err != nil {
		return nil, err
	}
	entries, err := h.mgr().ListRemoteDir(ctx, target, remotePath, RemoteOpOptions{SessionID: sid})
	if err != nil {
		return nil, err
	}
	out := make([]urls.DirEntry, len(entries))
	for i, e := range entries {
		out[i] = urls.DirEntry{
			Name:  e.Name,
			IsDir: e.FileType == RemoteTypeDir,
		}
	}
	return out, nil
}

// Complete implements urls.Completer for host name autocompletion.
// upstream: ssh-protocol.ts:388-396
func (h *Handler) Complete(c urls.Context, _ string) ([]urls.Completion, error) {
	hosts := h.loadConfiguredHosts(c.Cwd)
	out := make([]urls.Completion, len(hosts))
	for i, host := range hosts {
		desc := host.Description
		if desc == "" {
			desc = hostAddress(host)
		}
		out[i] = urls.Completion{
			Value:       url.PathEscape(host.Name),
			Label:       host.Name,
			Description: desc,
		}
	}
	return out, nil
}

// Write implements urls.Writer to write UTF-8 text files byte-exact over SSH.
// upstream: ssh-protocol.ts:398-402
func (h *Handler) Write(c urls.Context, u urls.URL, content string) (*urls.WriteResult, error) {
	ctx, cancel, sid := contextForCall(c)
	defer cancel()
	target, err := h.resolveTarget(u, c.Cwd)
	if err != nil {
		return nil, err
	}
	if _, err := BuildSSHTarget(target.Username, target.Host); err != nil {
		return nil, err
	}
	remotePath, err := h.remotePathFromURL(ctx, u, target, sid)
	if err != nil {
		return nil, err
	}
	if err := h.mgr().WriteRemoteFile(ctx, target, remotePath, []byte(content), RemoteFileWriteOptions{SessionID: sid}); err != nil {
		return nil, err
	}
	return nil, nil
}

// Ensure interface compliance at compile time.
var (
	_ urls.Handler          = (*Handler)(nil)
	_ urls.Writer           = (*Handler)(nil)
	_ urls.DirectoryReader  = (*Handler)(nil)
	_ urls.Completer        = (*Handler)(nil)
	_ urls.PromptDocer      = (*Handler)(nil)
	_ urls.PromptReferencer = (*Handler)(nil)
)
