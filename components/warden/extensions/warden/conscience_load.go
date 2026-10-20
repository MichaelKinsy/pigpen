package warden

import (
	"io"
	"os"
	"sync"
)

// Conscience load mode (src/load.ts, spec section 5 rules 1-10): read a skill file from disk and supply its text to
// the model. Nothing here talks to the judge; the caller decides when to load.

// Why a load produced no body.
const (
	LoadDenied          = "load_denied"
	LoadTooLarge        = "load_too_large"
	LoadChanged         = "load_changed"
	LoadFailed          = "load_failed"
	LoadMetadataUnsafe  = "metadata_unsafe"
	LoadAlreadySupplied = "already_supplied"
	LoadNoPolicy        = "no_policy"
	LoadNotEligible     = "not_eligible"
	LoadHeadroomUnknown = "headroom_unknown"
)

// LoadResult is the outcome of loadSkillBody. Body is nil when nothing was loaded; a skill whose body is empty loads
// as an empty string, which the caller treats like a failure (the original tests the body for truthiness).
type LoadResult struct {
	Body           *string
	SkillName      string
	AdvertisedPath string
	ResolvedPath   string
	RelativeRef    string
	SkipReason     string
	BytesLoaded    int
}

// ConsciencePolicy is the measured policy that gates delivery.
type ConsciencePolicy struct {
	QuestionHash       string
	Model              string
	RecommendThreshold float64
	AdvanceThreshold   float64
	LoadThreshold      float64
}

// conscienceBetaPolicy was measured on 2026-09-22 (126 labelled rows, pooled tool precision 74/83). The
// questionHash pins the question wording: a wording change must re-measure before this may ship again.
var conscienceBetaPolicy = &ConsciencePolicy{
	QuestionHash:       "fb2d35042f667b3c",
	Model:              "jev-1.13.0",
	RecommendThreshold: 0.80,
	AdvanceThreshold:   0.70,
	LoadThreshold:      1.0,
}

// policyMatches is the activation-gate predicate: the question hash and the model that actually answered both
// equal the policy's. An empty model never matches a policy.
func policyMatches(policy *ConsciencePolicy, questionHash, model string) bool {
	return policy != nil && policy.QuestionHash == questionHash && policy.Model == model
}

// fileIdentity is a file's lstat identity at selection time (rule 4, cross-call detection).
type fileIdentity struct {
	dev, ino  uint64
	size      int64
	mtimeNs   int64
	isSymlink bool
}

var (
	identityMu    sync.Mutex
	identityCache = map[string]fileIdentity{}
)

func lstatIdentity(path string) (fileIdentity, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	dev, ino := statDevIno(st)
	return fileIdentity{dev: dev, ino: ino, size: st.Size(), mtimeNs: st.ModTime().UnixNano(), isSymlink: st.Mode()&os.ModeSymlink != 0}, nil
}

// recordFileIdentity records a skill file's identity when the conscience selects it, before the judge-to-load gap.
func recordFileIdentity(skillName, filePath string) {
	id, err := lstatIdentity(filePath)
	identityMu.Lock()
	defer identityMu.Unlock()
	if err != nil {
		delete(identityCache, skillName)
		return
	}
	identityCache[skillName] = id
}

// clearFileIdentityCache runs on every new prompt.
func clearFileIdentityCache() {
	identityMu.Lock()
	identityCache = map[string]fileIdentity{}
	identityMu.Unlock()
}

// checkPathRules (rule 3): the proposed read goes through the path rules on the advertised and the resolved path.
func checkPathRules(skill Skill, resolved string, rules []PathRule, exempt []string) bool {
	if len(rules) == 0 {
		return false
	}
	ex := map[string]bool{}
	for _, e := range exempt {
		ex[e] = true
	}
	hits := append(matchPathRules("read", map[string]any{"path": skill.FilePath}, "", rules, ex), matchPathRules("read", map[string]any{"path": resolved}, "", rules, ex)...)
	for _, h := range hits {
		if h.Severity == SeverityDeny || h.Severity == SeverityDestructive {
			return true
		}
	}
	return false
}

// LoadOpts are the caller's facts about the moment of the load.
type LoadOpts struct {
	PathRules      []PathRule
	ExemptRules    []string
	LoadedBytes    int
	RemainingMs    int
	ConsentGiven   bool
	ProjectTrusted bool
	CatalogName    string
	CatalogDesc    string
	UserInvoked    bool
	// ContextWindow is the model's window, nil when unknown.
	ContextWindow *int
	HasImages     bool
}

var rePathInBody = lazyRE(`(?:^|` + reJSSpace + `)/[\w.~\-/]+(?:` + reJSSpace + `|$)`)

// checkHeadroom (rule 9): load only when the body fits under 80% of the model window; 4 bytes a token, 20% kept.
func checkHeadroom(bodyBytes int, window *int, hasImages bool) string {
	if window == nil || *window <= 0 || hasImages {
		return LoadHeadroomUnknown
	}
	if bodyBytes > *window*4*8/10 {
		return LoadTooLarge
	}
	return ""
}

// loadSkillBody loads a skill body from disk under rules 1-10.
func loadSkillBody(skill Skill, cfg ConscienceConfig, opts LoadOpts) LoadResult {
	res := func(reason string) LoadResult {
		return LoadResult{SkillName: skill.Name, AdvertisedPath: skill.FilePath, ResolvedPath: skill.FilePath, RelativeRef: relativeRef, SkipReason: reason}
	}
	// Rule 2: preconditions.
	switch {
	case !cfg.Enabled, cfg.Skills.Mode != "load", !opts.ConsentGiven, !opts.ProjectTrusted:
		return res(LoadNotEligible)
	case opts.UserInvoked:
		return res(LoadAlreadySupplied)
	case skill.DisableModelInvocation:
		return res(LoadNotEligible)
	}
	// Rule 3: path rules.
	if checkPathRules(skill, skill.FilePath, opts.PathRules, opts.ExemptRules) {
		return res(LoadDenied)
	}
	// Rule 5: size bounds.
	remaining := cfg.MaxLoadedBytes - opts.LoadedBytes
	if remaining <= 0 {
		return res(LoadTooLarge)
	}
	effectiveMax := min(cfg.MaxSkillBytes, remaining)
	if opts.RemainingMs <= 0 {
		return res(LoadFailed)
	}
	// Rule 4: the file changed between selection and load. lstat metadata, so a symlink retarget is not seen here.
	identityMu.Lock()
	cached, haveCached := identityCache[skill.Name]
	identityMu.Unlock()
	if haveCached {
		cur, err := lstatIdentity(skill.FilePath)
		if err != nil {
			return res(LoadFailed)
		}
		bothSymlinks := cached.isSymlink && cur.isSymlink
		if !bothSymlinks && (cur.dev != cached.dev || cur.ino != cached.ino || cur.size != cached.size || cur.mtimeNs != cached.mtimeNs) {
			identityMu.Lock()
			delete(identityCache, skill.Name)
			identityMu.Unlock()
			return res(LoadChanged)
		}
	}
	// Rule 4: identity before the open.
	pre, ok := captureIdentity(skill.FilePath)
	if !ok {
		return res(LoadFailed)
	}
	f, err := os.Open(skill.FilePath)
	if err != nil {
		return res(LoadFailed)
	}
	defer f.Close()
	fdStat, err := f.Stat()
	if err != nil {
		return res(LoadFailed)
	}
	dev, ino := statDevIno(fdStat)
	if pre.dev != dev || pre.ino != ino || pre.size != fdStat.Size() || pre.mtimeNs != fdStat.ModTime().UnixNano() {
		return res(LoadChanged)
	}
	// Rule 5: bounded read.
	buf := make([]byte, effectiveMax)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return res(LoadFailed)
	}
	if n >= effectiveMax {
		return res(LoadTooLarge)
	}
	// Rule 4: identity after the read.
	post, err := f.Stat()
	if err != nil {
		return res(LoadFailed)
	}
	if post.Size() != fdStat.Size() || !post.ModTime().Equal(fdStat.ModTime()) {
		return res(LoadChanged)
	}
	// Rule 6: parse and strip the frontmatter.
	fm, body, err := parseFrontmatter(nodeUTF8(buf[:n]))
	if err != nil {
		return res(LoadFailed)
	}
	name, _ := fm["name"].(string)
	desc, _ := fm["description"].(string)
	userOnly := fm["disable-model-invocation"] == true
	if name != "" && name != opts.CatalogName {
		return res(LoadChanged)
	}
	if desc != "" && desc != opts.CatalogDesc {
		return res(LoadChanged)
	}
	if userOnly && !skill.DisableModelInvocation {
		return res(LoadChanged)
	}
	// Rule 7: credential and path check.
	if len(FindSecrets(body)) > 0 || rePathInBody.MatchString(body) {
		return res(LoadMetadataUnsafe)
	}
	// Rule 9: headroom.
	bytesLoaded := len(body)
	if reason := checkHeadroom(bytesLoaded, opts.ContextWindow, opts.HasImages); reason != "" {
		return res(reason)
	}
	if id, err := lstatIdentity(skill.FilePath); err == nil {
		identityMu.Lock()
		identityCache[skill.Name] = id
		identityMu.Unlock()
	} else {
		identityMu.Lock()
		delete(identityCache, skill.Name)
		identityMu.Unlock()
	}
	r := res("")
	r.Body, r.BytesLoaded = &body, bytesLoaded
	return r
}

const relativeRef = "Resolve this skill's relative references against the directory of its advertised location."

// captureIdentity is the identity of the file a path leads to, symlinks followed (the stat of an opened descriptor).
func captureIdentity(path string) (fileIdentity, bool) {
	l, err := lstatIdentity(path)
	if err != nil {
		return fileIdentity{}, false
	}
	if !l.isSymlink {
		return l, true
	}
	st, err := os.Stat(path)
	if err != nil {
		return fileIdentity{}, false
	}
	dev, ino := statDevIno(st)
	return fileIdentity{dev: dev, ino: ino, size: st.Size(), mtimeNs: st.ModTime().UnixNano(), isSymlink: true}, true
}

// buildLoadMessage (rule 8): the complete body and the relative-reference sentence, never the path.
func buildLoadMessage(r LoadResult) string {
	body := "null"
	if r.Body != nil {
		body = *r.Body
	}
	return "Skill: " + r.SkillName + "\n\n" + body + "\n\n" + r.RelativeRef
}
