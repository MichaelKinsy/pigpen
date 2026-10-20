package pi_permission_system

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Port of src/access-intent/bash/bash-path-resolver.ts: resolves the filesystem paths a parsed bash program references against
// the working directory and platform. Platform and cwd questions are answered by the PathNormalizer; the resolver consults no
// ruleset, so candidacy is a syntax and filesystem question and the policy decision belongs to the gates downstream.

// BashPathRuleCandidate is a path-like token with the path's lexical and canonical forms for policy matching and the attributed
// effect that routes the token to a directional surface. Token is the raw path-like token shown in prompts, logs and session
// approvals.
type BashPathRuleCandidate struct {
	Token  string
	Path   *AccessPath
	Effect TokenEffect
}

// BashExternalPath is a path resolving outside the working directory, with its attributed effect.
type BashExternalPath struct {
	Path   *AccessPath
	Effect TokenEffect
}

// ResolvedBashPaths are the two typed slices a BashProgram exposes. ExternalAccesses are deduplicated accesses resolving outside
// the working directory (#418); RuleCandidates pairs every path-rule token with its cd-aware policy values (#393).
type ResolvedBashPaths struct {
	ExternalAccesses []BashExternalPath
	RuleCandidates   []BashPathRuleCandidate
}

// effectiveBase is the working directory in force where a path candidate appears. A known base carries an offset to be joined
// with the cwd at resolution time: a relative-or-absolute path string built by folding the literal targets of current-shell `cd`
// commands ("" is the cwd); an absolute offset (from `cd /abs`) ignores the cwd at resolution time. An unknown base marks a
// non-literal `cd` target (`cd "$DIR"`, `cd $(...)`, `cd -`, bare `cd`, `cd ~...`) that made the effective directory
// unresolvable.
type effectiveBase struct {
	known  bool
	offset string
}

var (
	// cwdBase is the working directory in force at the start of a program.
	cwdBase = effectiveBase{known: true}
	// unknownBase is the effective directory after a non-literal or unresolvable `cd`.
	unknownBase = effectiveBase{}
)

// pathCandidate is a path-candidate token paired with the effective working directory projected onto the point in the command
// stream where it appears, the effect its position proved, and the role its collector gave it.
type pathCandidate struct {
	token  string
	base   effectiveBase
	effect TokenEffect
	role   bashanalysis.TokenRole
}

// probedToken is a promoted bare token and its resolved path, before an effect is attached.
type probedToken struct {
	token string
	path  *AccessPath
}

// BashPathResolver resolves the filesystem paths a parsed bash program references. One instance per parse.
type BashPathResolver struct {
	normalizer *PathNormalizer
	words      *bashparse.WordReader
	// workdir is an aliased shell tool's working directory (#574), or nil.
	workdir *string
}

// NewBashPathResolver builds a resolver over the session normalizer and the program's word reader. workdir, when non-nil, seeds
// the initial effective base as if the program were prefixed with `cd <workdir>`.
func NewBashPathResolver(normalizer *PathNormalizer, words *bashparse.WordReader, workdir *string) *BashPathResolver {
	return &BashPathResolver{normalizer: normalizer, words: words, workdir: workdir}
}

// Resolve resolves a parsed bash program's path references into its external-path and rule-candidate slices, walking the AST
// exactly once.
//
// When a workdir is set it seeds the initial effective base, so relative tokens resolve against it, and the workdir itself is
// added to the external paths when it resolves outside the cwd. Containment is always measured against the session cwd baked into
// the normalizer, so a workdir outside the cwd does not widen the sandbox.
//
// salvagedRoots are regions the primary parse could not resolve, re-parsed cleanly on their own (#875). Their candidates are
// collected into the same slice before projection runs, so a path both a salvaged region and the primary parse name folds to one
// entry. Each is walked under the unknown base: a fragment carries no record of the `cd` in force where it sat, and resolving
// `cat rel.txt` against the session cwd after `cd /outside` would name a different file than the one that runs, which a rule for
// that other path could then allow.
//
// A token spelled from a HOME the program rebinds leaves both slices before projection: path normalization would otherwise
// expand its `~`/`$HOME` prefix to the startup home, naming a directory the shell never touches.
func (r *BashPathResolver) Resolve(rootNode *bashparse.Node, salvagedRoots []*bashparse.Node) ResolvedBashPaths {
	initialBase := cwdBase
	if r.workdir != nil {
		initialBase = r.deriveBaseFromCdTarget(cwdBase, *r.workdir)
	}
	var collected []pathCandidate
	r.walkForCandidates(rootNode, initialBase, &collected)
	for _, salvaged := range salvagedRoots {
		r.walkForCandidates(salvaged, unknownBase, &collected)
	}
	candidates := make([]pathCandidate, 0, len(collected))
	for _, c := range collected {
		if !r.words.SpellsReboundHome(c.token) {
			candidates = append(candidates, c)
		}
	}
	return ResolvedBashPaths{
		ExternalAccesses: r.withWorkdirExternal(r.projectExternalPaths(candidates)),
		RuleCandidates:   r.projectRuleCandidates(candidates),
	}
}

// withWorkdirExternal prepends the workdir's own AccessPath to the external paths when it resolves outside the cwd. A real
// `cd /etc` flags `/etc` via its argument token; the seeded base carries no such token, so it is added explicitly and
// deduplicated against the command's own external tokens (#574).
//
// It carries UnprovenEffect: no command word was observed for it and no redirect proved it. When the command's own tokens already
// named it, the token's attribution stands: an implicit base is not evidence against a proof.
func (r *BashPathResolver) withWorkdirExternal(tokenExternals []BashExternalPath) []BashExternalPath {
	if r.workdir == nil {
		return append([]BashExternalPath{}, tokenExternals...)
	}
	wdPath := r.normalizer.ForBashToken(*r.workdir, nil)
	canonical := wdPath.BoundaryValue()
	isExternal := true
	if canonical != "" {
		isExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)
	}
	if !isExternal {
		return append([]BashExternalPath{}, tokenExternals...)
	}
	key := canonical
	if key == "" {
		key = wdPath.Value()
	}
	for _, e := range tokenExternals {
		identity := e.Path.BoundaryValue()
		if identity == "" {
			identity = e.Path.Value()
		}
		if identity == key {
			return append([]BashExternalPath{}, tokenExternals...)
		}
	}
	return append([]BashExternalPath{{Path: wdPath, Effect: UnprovenEffect}}, tokenExternals...)
}

// ── AST walk: collect path candidates ────────────────────────────────────────

// walkForCandidates collects a single node's candidates tagged with base, returning the effective base in force after the node
// (the input base unless the node is a current-shell `cd <literal>` that folds the running directory).
//
// The effective directory is stateful: it starts at the cwd and each current-shell `cd <literal>` (joined by `&&`, `||`, `;` or a
// newline) folds into it for subsequent commands. A `cd` inside a pipeline or a backgrounded command runs in a subshell and does
// not update the running directory; subshell and brace-group interiors inherit the enclosing base without folding their own `cd`s
// (a conservative first tier).
func (r *BashPathResolver) walkForCandidates(node *bashparse.Node, base effectiveBase, out *[]pathCandidate) effectiveBase {
	switch node.Type() {
	case "program", "list", "redirected_statement":
		return r.walkCurrentShellSequence(node, base, out)
	case "command":
		tagTokens(bashanalysis.CollectCommandTokens(node, r.words), base, out)
		return r.foldCd(node, base)
	case "pipeline":
		// tree-sitter-bash mis-groups a redirect-bearing `&&`/`;` list as the first stage of a pipeline (`cd a && pnpm x 2>&1 |
		// tail` parses as `(cd a && pnpm x 2>&1) | tail`), burying a current-shell `cd` inside a node the default case treats as
		// non-folding. Recover bash operator precedence (`|` binds tighter than `&&`/`||`/`;`): fold the first stage's leading
		// current-shell commands while keeping its terminal command and every downstream stage as non-folding subshells (#454).
		return r.walkPipeline(node, base, out)
	case "subshell":
		// A subshell runs in a child shell: its interior `cd`s fold within the subshell but reset on exit, so the folded base is
		// discarded.
		r.walkCurrentShellSequence(node, base, out)
		return base
	case "compound_statement":
		// A `{ ... }` brace group runs in the current shell, so its `cd`s persist to following commands: thread and return the
		// folded base.
		return r.walkCurrentShellSequence(node, base, out)
	default:
		// Control-flow bodies, redirect targets, and command/process substitution interiors: collect every candidate in the
		// subtree tagged with the enclosing base and do not fold their internal `cd`s. (Folding inside substitutions is deferred:
		// conservative, never under-flags.)
		tagTokens(bashanalysis.CollectPathCandidateTokens(node, r.words), base, out)
		return base
	}
}

// walkCurrentShellSequence folds a current-shell sequence (`program` / `list` / `redirected_statement`): it threads the effective
// base left-to-right through the children so a `cd` updates the base for following siblings. A statement immediately followed by
// the background operator (`&`) runs in a subshell, so its folded base is discarded.
func (r *BashPathResolver) walkCurrentShellSequence(seqNode *bashparse.Node, base effectiveBase, out *[]pathCandidate) effectiveBase {
	current := base
	for i := 0; i < seqNode.ChildCount(); i++ {
		child := seqNode.Child(i)
		if !child.IsNamed() || bashparse.SkipSubtreeTypes[child.Type()] {
			continue
		}
		after := r.walkForCandidates(child, current, out)
		if !isBackgrounded(seqNode, i) {
			current = after
		}
	}
	return current
}

// walkPipeline walks a `pipeline` node, returning the effective base in force after it.
//
// Each stage of a true pipeline (`A | B | C`) runs in a subshell, so a `cd` inside any stage must not leak: the base normally
// passes through unchanged. The exception is the first stage: tree-sitter-bash wraps a redirect-bearing current-shell `&&`/`;`
// list (`cd a && pnpm x 2>&1 | tail`) as that stage, and bash precedence makes the list's leading commands current-shell, so they
// fold and the folded base persists past the pipeline to following siblings.
//
// The terminal command of the first stage is the real pipe stage (a subshell) and must not fold; every stage after a `|` is a
// downstream subshell stage and collects tokens against the folded base without folding (#454).
func (r *BashPathResolver) walkPipeline(node *bashparse.Node, base effectiveBase, out *[]pathCandidate) effectiveBase {
	current := base
	first := true
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if !child.IsNamed() || bashparse.SkipSubtreeTypes[child.Type()] {
			continue
		}
		if first {
			current = r.foldPipelineFirstStage(child, current, out)
			first = false
			continue
		}
		// Downstream stage (after a `|`): subshell. Collect against the folded base, do not fold.
		tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)
	}
	return current
}

// foldPipelineFirstStage collects the first pipe stage's candidates, folding its leading current-shell `cd` commands when
// tree-sitter wrapped a `list` or `redirected_statement` around them. The terminal command of that container is the real pipe
// stage (a subshell) and is collected without folding. A bare `command` first stage (a true pipeline first stage such as `cd
// nested | cat ../b`) is a subshell: it collects against the input base and does not fold.
func (r *BashPathResolver) foldPipelineFirstStage(node *bashparse.Node, base effectiveBase, out *[]pathCandidate) effectiveBase {
	if node.Type() == "list" {
		return r.foldListExceptTerminal(node, base, out)
	}
	if node.Type() == "redirected_statement" {
		current := base
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if !child.IsNamed() {
				continue
			}
			if child.Type() == "file_redirect" {
				// Redirect destinations are part of the piped stage; collect them against the folded base without folding.
				tagTokens(bashanalysis.CollectRedirectTokens(child, r.words), current, out)
				continue
			}
			// The inner statement is the `list`/`command` being redirected; fold its leading current-shell commands via the
			// terminal-excluding walk.
			current = r.foldPipelineFirstStage(child, current, out)
		}
		return current
	}
	// Bare `command` or any other shape: a true subshell first stage.
	tagTokens(bashanalysis.CollectPathCandidateTokens(node, r.words), base, out)
	return base
}

// foldListExceptTerminal folds every named, non-skip child of a `list` except the last, threading the effective base left-to-right
// through the leading current-shell commands; the terminal child is the real pipe stage and is collected without folding.
func (r *BashPathResolver) foldListExceptTerminal(node *bashparse.Node, base effectiveBase, out *[]pathCandidate) effectiveBase {
	var namedChildren []*bashparse.Node
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.IsNamed() && !bashparse.SkipSubtreeTypes[child.Type()] {
			namedChildren = append(namedChildren, child)
		}
	}
	current := base
	for i, child := range namedChildren {
		if i < len(namedChildren)-1 {
			current = r.walkForCandidates(child, current, out)
		} else {
			// Terminal child: the real pipe stage. Collect without folding.
			tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)
		}
	}
	return current
}

// foldCd computes the effective base after a command runs. It returns base unchanged unless the command is `cd`:
//
//   - `cd /abs` (absolute literal) gives a fresh known base, recovering from an earlier unknown base. On win32 a drive-mount
//     target (`cd /c/x`) folds to its translated Windows base, while a non-mount POSIX absolute (`cd /tmp`) is not
//     deterministically resolvable and yields unknown (#533).
//   - `cd rel` (relative literal) folds into a known base, or stays unknown if the base was already unknown.
//   - `cd "$DIR"` / `cd $(...)` / `cd -` / bare `cd` / `cd ~...` (non-literal) gives unknown.
//
// The target's platform/MSYS interpretation is delegated to the PathNormalizer; this method owns only the base-folding state.
func (r *BashPathResolver) foldCd(commandNode *bashparse.Node, base effectiveBase) effectiveBase {
	if name, ok := bashanalysis.ExtractCommandName(commandNode, r.words); !ok || name != "cd" {
		return base
	}
	target, ok := cdLiteralTarget(commandNode)
	if !ok {
		return unknownBase
	}
	return r.deriveBaseFromCdTarget(base, target)
}

// deriveBaseFromCdTarget folds a literal `cd`/working-directory target string into the effective base, delegating the
// platform/MSYS interpretation to the PathNormalizer: absolute is a fresh known base (recovers from an earlier unknown base),
// unknown makes the base conservatively unknown, relative joins into a known base or stays unknown if already unknown. Shared by
// foldCd (inline `cd` commands) and the initial-base seed (an aliased shell tool's workdir, an implicit leading `cd <workdir>`).
func (r *BashPathResolver) deriveBaseFromCdTarget(base effectiveBase, target string) effectiveBase {
	interpreted := r.normalizer.InterpretBashCdTarget(target)
	switch interpreted.Kind {
	case "absolute":
		return effectiveBase{known: true, offset: interpreted.Value}
	case "relative":
		if !base.known {
			return unknownBase
		}
		return effectiveBase{known: true, offset: r.normalizer.JoinBase(base.offset, target)}
	}
	return unknownBase
}

// ── Projection ───────────────────────────────────────────────────────────────

// projectExternalPaths projects the collected candidates into deduplicated external paths.
//
// It filters candidates through the strict path classifier unless their role already admits them, resolves each against its
// effective working directory base, and returns only paths that resolve outside the baked cwd in their lexical (as-typed,
// normalized but not symlink-resolved) form. The outside-cwd decision and the dedup identity use the canonical (symlink-resolved)
// form so `external_directory` config patterns match the path as the user typed it (#418).
func (r *BashPathResolver) projectExternalPaths(candidates []pathCandidate) []BashExternalPath {
	seen := map[string]int{}
	externalPaths := []BashExternalPath{}

	for _, c := range candidates {
		candidate := c.token
		if !admittedByRole(c.role) {
			candidate, _ = bashanalysis.ClassifyTokenAsPathCandidate(c.token)
		}
		// JavaScript's `!candidate`: null and the empty string alike.
		if candidate == "" {
			// A bare token the strict shape gate rejects can still escape the tree through a symlink, so probe it and apply the
			// ordinary boundary decision to whatever it resolves to (#645).
			if probed := r.probeBareToken(c.token, c.base); probed != nil {
				r.collectIfExternal(probed.path, c.effect, seen, &externalPaths)
			}
			continue
		}

		// Unknown effective directory: a relative candidate could resolve anywhere, so flag it conservatively (resolved against
		// the baked cwd only for a display path). Absolute / `~` candidates are base-independent below.
		if !c.base.known && r.isRelativeCandidate(candidate) {
			accessPath := r.normalizer.ForPath(candidate, nil)
			canonical := accessPath.BoundaryValue()
			if canonical != "" && !IsSafeSystemPath(canonical) {
				recordExternal(accessPath, c.effect, canonical, seen, &externalPaths)
			}
			continue
		}

		var resolveBase *string
		if c.base.known {
			rb := r.normalizer.ResolveBase(c.base.offset)
			resolveBase = &rb
		}
		r.collectIfExternal(r.normalizer.ForBashToken(candidate, resolveBase), c.effect, seen, &externalPaths)
	}
	return externalPaths
}

// collectIfExternal records accessPath when it resolves outside the working directory and has not already been collected.
//
// The boundary decision and dedup identity use the canonical (symlink-resolved) form the AccessPath already derived, while the
// stored value keeps the lexical form so config patterns match the path as the user typed it (#418). A win32 device path
// preserves `/dev/null` as its boundary value, so IsBoundaryOutsideWorkingDirectory reaches the safe-path exclusion (#533).
//
// A literal-only bash token (a win32 non-mount POSIX absolute like `/tmp`) has no canonical form; it is foreign to the win32 cwd,
// so it is always external. Its lexical value is the dedup identity so two distinct literal-only paths do not collapse (#533).
func (r *BashPathResolver) collectIfExternal(accessPath *AccessPath, effect TokenEffect, seen map[string]int, out *[]BashExternalPath) {
	lexical := accessPath.Value()
	if lexical == "" {
		return
	}
	canonical := accessPath.BoundaryValue()
	isExternal := true
	if canonical != "" {
		isExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)
	}
	if !isExternal {
		return
	}
	key := canonical
	if key == "" {
		key = lexical
	}
	recordExternal(accessPath, effect, key, seen, out)
}

// projectRuleCandidates projects the collected candidates into rule candidates with their cd-aware policy lookup values.
//
// It filters candidates through the broad path classifier, falling back to probeBareToken for a bare token the broad classifier
// rejects for shape: admitted only when it names an existing filesystem entry (#645). A token its role admits skips both (#609).
// On win32 the broad classifier is told to treat a backslash as a path separator, so a backslash-relative token (`dir\file`) is
// recognized as a rule candidate the same as its forward-slash equivalent (#520); on POSIX `\` is a legal filename character, so
// the token stays bare there. Each qualifying token is paired with its set of policy values (absolute + project-relative + raw).
// A token after a non-literal `cd` keeps only its literal value so no spurious absolute rule can match (#393).
func (r *BashPathResolver) projectRuleCandidates(candidates []pathCandidate) []BashPathRuleCandidate {
	seen := map[string]int{}
	result := []BashPathRuleCandidate{}

	for _, c := range candidates {
		var probed *probedToken
		shaped, ok := c.token, true
		if !admittedByRole(c.role) {
			shaped, ok = bashanalysis.ClassifyTokenAsRuleCandidate(c.token, r.normalizer.Flavor)
		}
		if !ok {
			probed = r.probeBareToken(c.token, c.base)
		} else {
			probed = &probedToken{token: shaped, path: r.buildRuleCandidatePath(shaped, c.base)}
		}
		if probed == nil {
			continue
		}

		matchValues := probed.path.MatchValues()
		if len(matchValues) == 0 {
			continue
		}

		key := strings.Join(matchValues, "\x00")
		if index, found := seen[key]; found {
			// The same resolved path, reached again with its own attribution: fold rather than split, so `cat ~/a > ~/a` stays one
			// entry in the prompt's evidence and two disagreeing proofs land on the bare family.
			result[index].Effect = MergeTokenEffects(result[index].Effect, c.effect)
			continue
		}
		seen[key] = len(result)
		result = append(result, BashPathRuleCandidate{Token: probed.token, Path: probed.path, Effect: c.effect})
	}
	return result
}

// probeBareToken promotes a bare token the shape gates rejected, when it names an existing filesystem entry: the existence probe
// (ADR 0009, #645).
//
// Most bash argument tokens are not paths (`status`, `build`, `main`), so a bare token is admitted only when the filesystem
// confirms it names something real. Candidacy therefore comes from the filesystem and never from the ruleset, which keeps the
// classifiers pure and lets a symlink be matched by rules naming its target.
//
// It returns nil when the token's shape rules out a path, when the effective base is unknown (no concrete directory to resolve
// against, so the token stays unpromoted per #393 conservatism), or when nothing exists at the resolved location. Shared by both
// projections so a promoted token is identical whether it is matched against `path` rules or tested against the cwd boundary.
func (r *BashPathResolver) probeBareToken(token string, base effectiveBase) *probedToken {
	bare, ok := bashanalysis.ClassifyBareTokenCandidate(token)
	if !ok || !base.known {
		return nil
	}
	rb := r.normalizer.ResolveBase(base.offset)
	path := r.normalizer.ForBashToken(bare, &rb)
	lexical := path.Value()
	if lexical == "" || !r.normalizer.EntryExists(lexical) {
		return nil
	}
	return &probedToken{token: bare, path: path}
}

func (r *BashPathResolver) buildRuleCandidatePath(candidate string, base effectiveBase) *AccessPath {
	// An unknown base + relative candidate stays literal-only: a resolved absolute or canonical alias would resolve against the
	// wrong directory and could spuriously match a rule (#393).
	if !base.known && r.isRelativeCandidate(candidate) {
		return r.normalizer.ForLiteral(NormalizePathPolicyLiteral(candidate))
	}
	var resolveBase *string
	if base.known {
		rb := r.normalizer.ResolveBase(base.offset)
		resolveBase = &rb
	}
	return r.normalizer.ForBashToken(candidate, resolveBase)
}

// isRelativeCandidate is true when a path candidate is relative (resolved against the effective directory) rather than absolute or
// home-relative (`~...`), which are base-independent. The absoluteness decision is the platform-aware PathNormalizer's, so Windows
// drive-letter paths (`C:/...`, `C:\...`) are absolute on win32 and relative on POSIX (where they denote an in-cwd path).
func (r *BashPathResolver) isRelativeCandidate(candidate string) bool {
	return !r.normalizer.IsAbsolute(candidate) && !strings.HasPrefix(candidate, "~")
}

// ── Pure AST/string helpers ──────────────────────────────────────────────────

// isBackgrounded is true when the statement at index is immediately followed by the background operator (`&`), distinct from the
// `&&` / `||` / `;` current-shell separators.
func isBackgrounded(seqNode *bashparse.Node, index int) bool {
	next := seqNode.Child(index + 1)
	if next == nil || next.IsNamed() {
		return false
	}
	return next.Type() == "&"
}

func tagTokens(tokens []bashanalysis.PathToken, base effectiveBase, out *[]pathCandidate) {
	for _, t := range tokens {
		*out = append(*out, pathCandidate{token: t.Token, base: base, effect: t.Effect, role: t.Role})
	}
}

// admittedByRole is whether a candidate's role settles its candidacy, so neither the shape gate nor the existence probe is
// consulted: a redirect's own target is proven by syntax to name a file, including one the command is about to create.
func admittedByRole(role bashanalysis.TokenRole) bool {
	return role == bashanalysis.RoleRedirectDestination
}

// recordExternal records an external access under dedupKey, merging into the entry already there rather than adding a second one.
//
// Keeping the effect out of the dedup key is deliberate: `cat ~/a > ~/a` is one path, and splitting it would show `~/a` twice in
// the ask prompt. Two disagreeing proofs fold to unproven, which routes to the bare family (consult both), so the fold loses
// nothing the gates would have used.
func recordExternal(accessPath *AccessPath, effect TokenEffect, dedupKey string, seen map[string]int, out *[]BashExternalPath) {
	index, found := seen[dedupKey]
	if !found {
		seen[dedupKey] = len(*out)
		*out = append(*out, BashExternalPath{Path: accessPath, Effect: effect})
		return
	}
	existing := (*out)[index]
	(*out)[index] = BashExternalPath{Path: existing.Path, Effect: MergeTokenEffects(existing.Effect, effect)}
}

// cdLiteralTarget is the literal target of a `cd` command, or ok=false when the first argument is not a static literal (it holds
// an expansion or command substitution) or cannot be resolved against the working directory (`cd -`, `cd ~...`, bare `cd`).
func cdLiteralTarget(commandNode *bashparse.Node) (string, bool) {
	for i := 0; i < commandNode.ChildCount(); i++ {
		child := commandNode.Child(i)
		if bashanalysis.CommandPrefixTypes[child.Type()] {
			continue
		}
		// A redirect is not cd's operand, wherever it sits (`2>/dev/null cd a`).
		if bashparse.RedirectNodeTypes[child.Type()] {
			continue
		}
		if !child.IsNamed() {
			continue
		}
		// Skip the `--` end-of-flags marker; the next argument is the target.
		if child.Type() == "word" && child.Text() == "--" {
			continue
		}
		if !bashparse.ArgNodeTypes[child.Type()] {
			return "", false
		}
		return literalTextOf(child)
	}
	return "", false
}

// literalTextOf is the literal string value of an argument node, or ok=false when it holds a variable expansion / command
// substitution or is a non-resolvable `cd` destination (`-`, `~...`).
func literalTextOf(node *bashparse.Node) (string, bool) {
	switch node.Type() {
	case "word":
		text := node.Text()
		if text == "-" || strings.HasPrefix(text, "~") {
			return "", false
		}
		return text, true
	case "raw_string":
		text := node.Text()
		if len(text) >= 2 && strings.HasPrefix(text, "'") && strings.HasSuffix(text, "'") {
			return text[1 : len(text)-1], true
		}
		return text, true
	case "concatenation":
		var result strings.Builder
		for _, child := range node.Children() {
			part, ok := literalTextOf(child)
			if !ok {
				return "", false
			}
			result.WriteString(part)
		}
		return result.String(), true
	case "string":
		var result strings.Builder
		for _, child := range node.Children() {
			if child.Type() == `"` {
				continue
			}
			if child.Type() != "string_content" {
				return "", false
			}
			result.WriteString(child.Text())
		}
		return result.String(), true
	}
	return "", false
}
