package pi_subagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Port of src/runs/shared/external-cli-preflight.ts: the launch preflight of an external CLI (its version and help text, probed
// once per binary and cached), and the resolution of the binary on PATH.

const (
	maxProbeOutputBytes      = 256 * 1024
	maxProbeTimeoutMs        = 5000
	maxRemoteProbeTimeoutMs  = 15000
	maxPreflightCacheEntries = 64
)

// ExternalCliPreflightSpec is ExternalCliPreflightSpec: what to probe and how to validate the answers.
type ExternalCliPreflightSpec struct {
	ID            string
	VersionArgs   []string
	HelpArgs      []string
	EvidenceArgs  []string // nil: no evidence probe
	EvidenceLabel string
	ProbeTimeoutMs int // 0: the code-owned ceiling
	Remote        bool
	Validate      func(result *ExternalCliPreflightResult) error
}

// ExternalCliPreflightResult is ExternalCliPreflightResult.
type ExternalCliPreflightResult struct {
	BinaryPath    string
	BinaryMtimeMs float64
	Version       string
	Help          string
	Evidence      *string
	CacheHit      bool
}

type cachedPreflight struct{ binaryPath, version, help string; mtimeMs float64 }

var preflightCache = struct {
	sync.Mutex
	entries map[string]cachedPreflight
	order   []string
	lookup  map[string]string
}{entries: map[string]cachedPreflight{}, lookup: map[string]string{}}

// resolveExternalBinary is resolveBinary: an absolute or separator-bearing command must be executable; otherwise PATH is searched
// (with PATHEXT on Windows) in the given environment.
func resolveExternalBinary(command string, env map[string]string) (string, error) {
	if filepath.IsAbs(command) || strings.ContainsRune(command, filepath.Separator) {
		resolved, err := filepath.Abs(command)
		if err != nil {
			return "", err
		}
		if err := accessExecutable(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		pathext := env["PATHEXT"]
		if pathext == "" {
			pathext = ".EXE;.CMD;.BAT;.COM"
		}
		exts = strings.Split(pathext, ";")
	}
	for _, dir := range filepath.SplitList(env["PATH"]) {
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			candidate := filepath.Join(dir, command+ext)
			if accessExecutable(candidate) == nil {
				if real, err := filepath.EvalSymlinks(candidate); err == nil {
					return real, nil
				}
			}
		}
	}
	return "", fmt.Errorf("External CLI binary '%s' was not found on PATH.", command)
}

func accessExecutable(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("EACCES: permission denied, access '%s'", p)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("EACCES: permission denied, access '%s'", p)
	}
	return nil
}

// boundedBuffer keeps at most max bytes and records that more arrived.
type boundedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.overflow = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.overflow = true
	}
	return len(p), nil
}

func probeWithTimeout(binaryPath string, args []string, env map[string]string, label string, timeoutMs int, cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = cwd
	cmd.Env = envList(env)
	stdout := &boundedBuffer{max: maxProbeOutputBytes}
	stderr := &boundedBuffer{max: maxProbeOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	hideWindow(cmd)
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("External CLI %s preflight failed: spawnSync %s ETIMEDOUT (timed out after %dms)", label, binaryPath, timeoutMs)
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", fmt.Errorf("External CLI %s preflight failed: %s", label, err.Error())
	}
	if stdout.overflow || stderr.overflow {
		return "", fmt.Errorf("External CLI %s preflight failed: spawnSync %s ENOBUFS", label, binaryPath)
	}
	if exit != nil {
		detail := strings.TrimSpace(stderr.buf.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.buf.String())
		}
		return "", fmt.Errorf("External CLI %s preflight exited with code %d: %s", label, exit.ExitCode(), detail)
	}
	return strings.TrimSpace(stdout.buf.String()), nil
}

func narrowPositiveInteger(value, ceiling int, label string) (int, error) {
	if value == 0 {
		return ceiling, nil
	}
	if value < 0 || value > ceiling {
		return 0, fmt.Errorf("%s may only narrow the code-owned ceiling of %d.", label, ceiling)
	}
	return value, nil
}

func preflightSpecKey(spec ExternalCliPreflightSpec) string {
	b, _ := json.Marshal([]any{spec.ID, spec.VersionArgs, spec.HelpArgs, spec.EvidenceArgs, spec.EvidenceLabel, spec.ProbeTimeoutMs, spec.Remote})
	return string(b)
}

// preflightExternalCli is preflightExternalCli: it resolves command in env, probes version and help once per binary (the cache is
// keyed by path, modification time, version and the spec), validates, and returns the evidence.
func preflightExternalCli(command string, spec ExternalCliPreflightSpec, env map[string]string, cwd string) (*ExternalCliPreflightResult, error) {
	binaryPath, err := resolveExternalBinary(command, env)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return nil, err
	}
	mtime := float64(info.ModTime().UnixNano()) / 1e6
	lookupKey := fmt.Sprintf("%s\x00%v\x00%s", binaryPath, mtime, preflightSpecKey(spec))
	preflightCache.Lock()
	cachedKey := preflightCache.lookup[lookupKey]
	cached, hit := preflightCache.entries[cachedKey]
	preflightCache.Unlock()
	if cachedKey == "" {
		hit = false
	}
	ceiling := maxProbeTimeoutMs
	if spec.Remote {
		ceiling = maxRemoteProbeTimeoutMs
	}
	timeoutMs, err := narrowPositiveInteger(spec.ProbeTimeoutMs, ceiling, "probeTimeoutMs")
	if err != nil {
		return nil, err
	}
	base := cached
	if !hit {
		version, err := probeWithTimeout(binaryPath, spec.VersionArgs, env, "version", timeoutMs, cwd)
		if err != nil {
			return nil, err
		}
		help, err := probeWithTimeout(binaryPath, spec.HelpArgs, env, "help", timeoutMs, cwd)
		if err != nil {
			return nil, err
		}
		base = cachedPreflight{binaryPath: binaryPath, mtimeMs: mtime, version: version, help: help}
	}
	result := &ExternalCliPreflightResult{BinaryPath: base.binaryPath, BinaryMtimeMs: base.mtimeMs, Version: base.version, Help: base.help, CacheHit: hit}
	if spec.EvidenceArgs != nil {
		label := spec.EvidenceLabel
		if label == "" {
			label = "evidence"
		}
		evidence, err := probeWithTimeout(binaryPath, spec.EvidenceArgs, env, label, timeoutMs, cwd)
		if err != nil {
			return nil, err
		}
		result.Evidence = &evidence
	}
	if spec.Validate != nil {
		if err := spec.Validate(result); err != nil {
			return nil, err
		}
	}
	if !hit {
		cacheKey := fmt.Sprintf("%s\x00%s\x00%v\x00%s", binaryPath, base.version, mtime, preflightSpecKey(spec))
		preflightCache.Lock()
		if _, exists := preflightCache.entries[cacheKey]; !exists {
			preflightCache.order = append(preflightCache.order, cacheKey)
		}
		preflightCache.entries[cacheKey] = base
		preflightCache.lookup[lookupKey] = cacheKey
		for len(preflightCache.entries) > maxPreflightCacheEntries {
			oldest := preflightCache.order[0]
			preflightCache.order = preflightCache.order[1:]
			delete(preflightCache.entries, oldest)
			for k, v := range preflightCache.lookup {
				if v == oldest {
					delete(preflightCache.lookup, k)
				}
			}
		}
		preflightCache.Unlock()
	}
	return result, nil
}

// invalidateExternalCliPreflight is invalidateExternalCliPreflight: the entries of this binary or spec are dropped.
func invalidateExternalCliPreflight(command string, spec ExternalCliPreflightSpec) {
	key := preflightSpecKey(spec)
	preflightCache.Lock()
	defer preflightCache.Unlock()
	sep := string(filepath.Separator)
	kept := preflightCache.order[:0]
	for _, cacheKey := range preflightCache.order {
		entry := preflightCache.entries[cacheKey]
		if entry.binaryPath == command || strings.HasSuffix(entry.binaryPath, sep+command) || strings.Contains(cacheKey, key) {
			delete(preflightCache.entries, cacheKey)
			continue
		}
		kept = append(kept, cacheKey)
	}
	preflightCache.order = kept
	for lookupKey, cacheKey := range preflightCache.lookup {
		if _, ok := preflightCache.entries[cacheKey]; !ok {
			delete(preflightCache.lookup, lookupKey)
		}
	}
}

// clearExternalCliPreflightCache is clearExternalCliPreflightCacheForTests.
func clearExternalCliPreflightCache() {
	preflightCache.Lock()
	defer preflightCache.Unlock()
	preflightCache.entries = map[string]cachedPreflight{}
	preflightCache.lookup = map[string]string{}
	preflightCache.order = nil
}

// envMap turns an os.Environ-style list into a map (a later duplicate wins).
func envMap(list []string) map[string]string {
	m := make(map[string]string, len(list))
	for _, kv := range list {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m[k] = v
		}
	}
	return m
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
