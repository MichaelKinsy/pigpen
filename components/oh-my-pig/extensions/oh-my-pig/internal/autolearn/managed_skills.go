// Package autolearn ports omp's managed-skills primitives and `manage_skill` tool.
//
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts
// upstream: packages/coding-agent/src/autolearn/settings.ts
// upstream: packages/coding-agent/src/tools/manage-skill.ts
package autolearn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

const (
	// ManagedSkillsProviderID is stamped on discovered managed skills to distinguish them from authored ones.
	// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:17
	ManagedSkillsProviderID = "omp-managed"

	// MaxManagedSkillBytes is the hard UTF-8 byte cap on a managed SKILL.md file.
	// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:20
	MaxManagedSkillBytes = 64_000

	// SettingEnabled is the master gate for autolearn / manage_skill (default false).
	// upstream: packages/coding-agent/src/autolearn/settings.ts:10-21
	SettingEnabled = "autolearn.enabled"

	// SettingAutoContinue enables automatic post-stop capture turns (default false).
	// upstream: packages/coding-agent/src/autolearn/settings.ts:23-35
	SettingAutoContinue = "autolearn.autoContinue"

	// SettingMinToolCalls is the minimum tool calls per turn before auto-capture triggers (default 5).
	// upstream: packages/coding-agent/src/autolearn/settings.ts:38
	SettingMinToolCalls = "autolearn.minToolCalls"

	// DefaultMinToolCalls is the default value for autolearn.minToolCalls.
	// upstream: packages/coding-agent/src/autolearn/settings.ts:38
	DefaultMinToolCalls = 5
)

var (
	skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	multiTildeRE     = regexp.MustCompile(`~{2,}`)
	// Matches YAML 1.1/1.2 numeric scalars, inf/nan, hex/octal/binary, signs, and dash/underscore numeric forms.
	yamlNumericLikeRE = regexp.MustCompile(`^(?:[+-]?(?:0[xX][0-9a-fA-F_]+|0[oO][0-7_]+|0[bB][01_]+|[0-9][0-9_]*(?:\.[0-9_]*)?(?:[eE][+-]?[0-9_]+)?|\.[0-9_]+(?:[eE][+-]?[0-9_]+)?|\.(?:inf|Inf|INF|nan|NaN|NAN))|[0-9]+-[0-9]+)$`)
)

// AgentDirOverride overrides agent directory resolution in tests.
var AgentDirOverride func() string

// DefaultAgentDir resolves the agent directory when no sdk.Context is available:
// honors AgentDirOverride, PIG_CODING_AGENT_DIR, PIG_HOME/agent, then ~/.pig/agent.
func DefaultAgentDir() string {
	if fn := AgentDirOverride; fn != nil {
		if d := fn(); d != "" {
			return d
		}
	}
	if d := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); d != "" {
		return d
	}
	if h := strings.TrimSpace(os.Getenv("PIG_HOME")); h != "" {
		return filepath.Join(h, "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return filepath.Join(".pig", "agent")
}

// ManagedSkillsDir resolves the isolated managed-skills directory (`<agentDir>/managed-skills`).
// When agentDir is empty, DefaultAgentDir() is used.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:25-27
func ManagedSkillsDir(agentDir string) string {
	if strings.TrimSpace(agentDir) == "" {
		agentDir = DefaultAgentDir()
	}
	return filepath.Join(agentDir, "managed-skills")
}

// SanitizeSkillName validates and normalizes a managed-skill name.
// Rejects traversal (`..`), slashes, empty strings, and characters outside `[a-z0-9-]`.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:34-42
func SanitizeSkillName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if !skillNamePattern.MatchString(name) {
		return "", fmt.Errorf(
			"Invalid skill name %q. Use lowercase letters, digits, and hyphens (1-64 chars, starting with a letter or digit).",
			raw,
		)
	}
	return name, nil
}

// IsValidManagedSkillName reports whether name matches the exact post-sanitize shape.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:50-52
func IsValidManagedSkillName(name string) bool {
	return skillNamePattern.MatchString(name)
}

// SanitizeManagedDescription neutralizes a machine-generated managed-skill description
// so it cannot break out of the system prompt's `<skills>` listing.
// Strips Unicode Cc/Cf control/format chars, angle brackets, backticks, collapses
// repeated tildes (`~~+` → `~`), and collapses whitespace to a single line.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:62-69
func isECMASpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		'\u00A0', '\u1680',
		'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A',
		'\u2028', '\u2029', '\u202F', '\u205F', '\u3000', '\uFEFF':
		return true
	default:
		return false
	}
}

func SanitizeManagedDescription(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			b.WriteByte(' ')
			continue
		}
		if r == '<' || r == '>' || r == '`' {
			continue
		}
		b.WriteRune(r)
	}
	s := multiTildeRE.ReplaceAllString(b.String(), "~")
	var out strings.Builder
	out.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if isECMASpace(r) {
			if out.Len() > 0 {
				inSpace = true
			}
			continue
		}
		if inSpace {
			out.WriteByte(' ')
			inSpace = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

// ToSkillFrontmatter serializes the minimal `name`/`description` YAML frontmatter block.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:75-82
func ToSkillFrontmatter(name, description string) string {
	safeDesc := SanitizeManagedDescription(description)
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", formatYAMLScalar(name), formatYAMLScalar(safeDesc))
}

func formatYAMLScalar(s string) string {
	// Quote whenever YAML 1.1/1.2 (including Bun's YAML parser) could misinterpret
	// the scalar as non-string (booleans, null, numbers, flow punctuation, leading
	// indicators, etc.), using double-quoted YAML escaping so parseFrontmatter
	// round-trips verbatim as a string.
	needsQuote := s == "" ||
		strings.ContainsAny(s, ":\"'#{}[]&*!|>\\%@`,\n\r\t") ||
		strings.HasPrefix(s, "-") ||
		strings.HasPrefix(s, "+") ||
		strings.HasPrefix(s, "?") ||
		strings.HasPrefix(s, ".") ||
		strings.HasPrefix(s, " ") ||
		strings.HasSuffix(s, " ")
	if !needsQuote {
		switch strings.ToLower(s) {
		case "true", "false", "null", "yes", "no", "on", "off", "y", "n", "~":
			needsQuote = true
		}
	}
	if !needsQuote && yamlNumericLikeRE.MatchString(s) {
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// WriteManagedSkillInput configures WriteManagedSkill.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:84-89
type WriteManagedSkillInput struct {
	Action      string // "create" or "update"
	Name        string
	Description string
	Body        string
	AgentDir    string // optional; defaults to DefaultAgentDir()
}

// Per-skill mutex serializing concurrent create/update/delete on the same skill name.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:99-109
type skillLockEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	skillLocksMu sync.Mutex
	skillLocks   = map[string]*skillLockEntry{}
)

func withSkillLock(key string, fn func() error) error {
	skillLocksMu.Lock()
	entry := skillLocks[key]
	if entry == nil {
		entry = &skillLockEntry{}
		skillLocks[key] = entry
	}
	entry.refs++
	skillLocksMu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		skillLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && skillLocks[key] == entry {
			delete(skillLocks, key)
		}
		skillLocksMu.Unlock()
	}()
	return fn()
}

// assertManagedRootSafe validates the managed-skills root before composing any child path.
// A symlinked root is allowed when it resolves to an existing directory; a broken link
// or a link to a non-directory is refused.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:120-136
func assertManagedRootSafe(root string) error {
	rootStat, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if rootStat.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	targetStat, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("The managed-skills root %s is a broken symlink; refusing to write.", root)
		}
		return err
	}
	if !targetStat.IsDir() {
		return fmt.Errorf("The managed-skills root %s links to a non-directory; refusing to write.", root)
	}
	return nil
}

// WriteManagedSkill creates or updates a managed `SKILL.md` and returns its resolved file path.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:163-241
func WriteManagedSkill(input WriteManagedSkillInput) (string, error) {
	name, err := SanitizeSkillName(input.Name)
	if err != nil {
		return "", err
	}
	description := SanitizeManagedDescription(input.Description)
	body := strings.TrimSpace(input.Body)
	if description == "" {
		return "", fmt.Errorf("Managed skill %q needs a non-empty description.", name)
	}
	if body == "" {
		return "", fmt.Errorf("Managed skill %q needs a non-empty body.", name)
	}
	content := fmt.Sprintf("%s\n%s\n", ToSkillFrontmatter(name, description), body)
	if len(content) > MaxManagedSkillBytes {
		return "", fmt.Errorf(
			"Managed skill is %d bytes; the limit is %d. Trim the body or description.",
			len(content), MaxManagedSkillBytes,
		)
	}
	root := ManagedSkillsDir(input.AgentDir)
	lockKey := filepath.Join(root, name)
	var outPath string
	err = withSkillLock(lockKey, func() error {
		if err := assertManagedRootSafe(root); err != nil {
			return err
		}
		dir := filepath.Join(root, name)
		file := filepath.Join(dir, "SKILL.md")
		dirStat, err := os.Lstat(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if dirStat != nil && dirStat.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(
				"Managed skill %q resolves through a symlink; refusing to write outside the managed directory.",
				name,
			)
		}
		if input.Action == "create" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("Managed skill %q already exists. Use action \"update\" to change it.", name)
				}
				return err
			}
			_, writeErr := f.WriteString(content)
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			outPath = file
			return nil
		}
		if input.Action != "update" {
			return fmt.Errorf("Invalid action %q; expected \"create\" or \"update\".", input.Action)
		}
		fileStat, err := os.Lstat(file)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("Managed skill %q does not exist. Use action \"create\" to add it.", name)
			}
			return err
		}
		if fileStat.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Managed skill %q SKILL.md is a symlink; refusing to overwrite it.", name)
		}
		if err := assertManagedSkillFileSafeForUpdate(name, fileStat); err != nil {
			return err
		}
		f, err := openManagedSkillFileForUpdate(name, file)
		if err != nil {
			return err
		}
		defer f.Close()
		openStat, err := f.Stat()
		if err != nil {
			return err
		}
		if err := assertManagedSkillFileSafeForUpdate(name, openStat); err != nil {
			return err
		}
		if err := assertOpenedFileSafeForUpdate(name, f); err != nil {
			return err
		}
		if err := f.Truncate(0); err != nil {
			return err
		}
		if _, err := f.Seek(0, 0); err != nil {
			return err
		}
		if _, err := f.WriteString(content); err != nil {
			return err
		}
		outPath = file
		return nil
	})
	if err != nil {
		return "", err
	}
	return outPath, nil
}

// DeleteManagedSkill deletes a managed skill directory. Errors when it does not exist.
// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:244-266
func DeleteManagedSkill(name, agentDir string) error {
	safe, err := SanitizeSkillName(name)
	if err != nil {
		return err
	}
	root := ManagedSkillsDir(agentDir)
	lockKey := filepath.Join(root, safe)
	return withSkillLock(lockKey, func() error {
		if err := assertManagedRootSafe(root); err != nil {
			return err
		}
		dir := filepath.Join(root, safe)
		dirStat, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("Managed skill %q does not exist.", safe)
			}
			return err
		}
		if dirStat.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Managed skill %q is a symlink; refusing to delete outside the managed directory.", safe)
		}
		return os.RemoveAll(dir)
	})
}
