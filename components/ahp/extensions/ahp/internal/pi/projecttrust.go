package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Project trust (port of src/pi/project-trust.ts and the parts of Pi's trust-manager it uses).
//
// A project directory can carry resources that run code when Pi loads them (extensions, skills,
// prompts, themes, settings). Whether to load them is a security decision, and a host takes the
// working directory from a client over the network, so the policy matters:
//
//	trust    - load them (what raw Pi SDK construction does; the default)
//	inherit  - follow the decision the user recorded with Pi's own CLI; unknown = untrusted
//	never    - never load them, whatever was recorded

// ProjectTrustPolicy selects how project resources are trusted.
type ProjectTrustPolicy string

const (
	TrustPolicyTrust   ProjectTrustPolicy = "trust"
	TrustPolicyInherit ProjectTrustPolicy = "inherit"
	TrustPolicyNever   ProjectTrustPolicy = "never"
)

// TrustDecision is the outcome of a trust resolution.
type TrustDecision struct {
	Trusted bool
	// Reason is "no-project-resources", "user-trusted", "user-untrusted", "policy" or
	// "unknown-project".
	Reason string
}

// trustRequiringConfigResources are the entries of a project's config directory that Pi gates.
var trustRequiringConfigResources = []string{"settings.json", "extensions", "skills", "prompts", "themes", "SYSTEM.md", "APPEND_SYSTEM.md"}

// projectConfigDirs are the project config directory names checked: Pi's ".pi" and PiG's ".pig".
var projectConfigDirs = []string{".pi", ".pig"}

func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// HasTrustRequiringProjectResources reports whether cwd has project-local resources that must be
// gated: trust-requiring entries under its config directory, or .agents/skills in cwd or one of
// its ancestors. The user's own ~/.agents/skills is a trusted user resource and is ignored, even
// when cwd is the home directory.
func HasTrustRequiringProjectResources(cwd string) bool {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	userSkills := filepath.Join(canonical(home), ".agents", "skills")
	dir := canonical(cwd)
	for _, name := range projectConfigDirs {
		for _, entry := range trustRequiringConfigResources {
			if exists(filepath.Join(dir, name, entry)) {
				return true
			}
		}
	}
	for {
		skills := filepath.Join(dir, ".agents", "skills")
		if skills != userSkills && exists(skills) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// ProjectTrustStore is Pi's trust store (<agentDir>/trust.json): the very same file Pi's CLI
// writes, so a decision made in either tool holds in both.
type ProjectTrustStore struct{ path string }

// NewProjectTrustStore opens the store of an agent directory.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	return &ProjectTrustStore{path: filepath.Join(agentDir, "trust.json")}
}

func (s *ProjectTrustStore) read() (map[string]*bool, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return map[string]*bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var data map[string]*bool
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return nil, fmt.Errorf("Invalid trust store %s: expected an object of true, false or null values", s.path)
	}
	return data, nil
}

// withLock serialises store access with Pi's lock convention: a "<path>.lock" directory.
func (s *ProjectTrustStore) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	lock := s.path + ".lock"
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Mkdir(lock, 0o755); err == nil {
			defer os.Remove(lock)
			return fn()
		}
		if !os.IsExist(err) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("Failed to acquire trust store lock: %w", err)
}

// Get is the decision recorded for cwd or its nearest ancestor: true, false, or nil when none.
func (s *ProjectTrustStore) Get(cwd string) (*bool, error) {
	var out *bool
	err := s.withLock(func() error {
		data, err := s.read()
		if err != nil {
			return err
		}
		for dir := canonical(cwd); ; {
			if v := data[dir]; v != nil {
				out = v
				return nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return nil
			}
			dir = parent
		}
	})
	return out, err
}

// Set records (or with nil clears) the decision for cwd.
func (s *ProjectTrustStore) Set(cwd string, decision *bool) error {
	return s.withLock(func() error {
		data, err := s.read()
		if err != nil {
			return err
		}
		key := canonical(cwd)
		if decision == nil {
			delete(data, key)
		} else {
			data[key] = decision
		}
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(",")
			}
			kj, _ := json.Marshal(k)
			vj, _ := json.Marshal(data[k])
			fmt.Fprintf(&buf, "\n  %s: %s", kj, vj)
		}
		if len(keys) > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("}\n")
		return os.WriteFile(s.path, buf.Bytes(), 0o644)
	})
}

// ResolveProjectTrust decides whether a working directory's project resources may load. An empty
// policy means "trust". A store that cannot be read is an error, not a decision.
func ResolveProjectTrust(cwd string, policy ProjectTrustPolicy, agentDir string) (TrustDecision, error) {
	if policy == "" {
		policy = TrustPolicyTrust
	}
	if policy == TrustPolicyNever {
		return TrustDecision{false, "policy"}, nil
	}
	// Nothing in the directory needs gating: trusting it grants nothing.
	if !HasTrustRequiringProjectResources(cwd) {
		return TrustDecision{true, "no-project-resources"}, nil
	}
	if policy == TrustPolicyTrust {
		return TrustDecision{true, "policy"}, nil
	}
	stored, err := NewProjectTrustStore(agentDir).Get(cwd)
	if err != nil {
		return TrustDecision{}, err
	}
	switch {
	case stored != nil && *stored:
		return TrustDecision{true, "user-trusted"}, nil
	case stored != nil:
		return TrustDecision{false, "user-untrusted"}, nil
	}
	// No recorded decision, and a host has no user to ask.
	return TrustDecision{false, "unknown-project"}, nil
}
