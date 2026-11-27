package filetools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var sensitivePathPrefixes = []string{
	"/etc/",
	"/boot/",
	"/usr/lib/systemd/",
	"/private/etc/",
	"/private/var/db/",
	"/private/var/root/",
}

var sensitiveExactPaths = map[string]bool{
	"/var/run/docker.sock": true,
	"/run/docker.sock":     true,
}

var sensitiveBaseNames = map[string]bool{
	".env":            true,
	"id_rsa":          true,
	"id_ed25519":      true,
	"id_ecdsa":        true,
	"id_dsa":          true,
	".netrc":          true,
	".pgpass":         true,
	".npmrc":          true,
	".pypirc":         true,
	".git-credentials": true,
}

func (s *state) writeFileToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:        "write_file",
		Label:       "Write File",
		Description: "Write or overwrite a file atomically, creating parent directories as needed. Guards sensitive system and secret paths.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Full text content to write to the file.",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: s.executeWriteFile,
	}
}

func (s *state) executeWriteFile(ctx sdk.Context, params map[string]any) (any, error) {
	rawPath, ok := paramString(params, "path")
	if !ok || strings.TrimSpace(rawPath) == "" {
		return errResult("write_file: missing required field 'path'"), nil
	}
	content, ok := paramString(params, "content")
	if !ok {
		return errResult("write_file: missing required field 'content'"), nil
	}

	absPath, err := resolvePath(safeCwd(ctx), rawPath)
	if err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	if err := s.checkWriteGuard(ctx, absPath); err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	if err := s.checkStaleness(absPath); err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	if err := atomicWriteFile(absPath, []byte(content)); err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	if info, err := os.Stat(absPath); err == nil {
		s.recordRead(absPath, info, true)
	}

	lineCount := countLines(content)
	byteCount := len(content)
	return okResult(
		fmt.Sprintf("Wrote %d bytes (%d lines) to %s", byteCount, lineCount, absPath),
		map[string]any{
			"path":  absPath,
			"bytes": byteCount,
			"lines": lineCount,
		},
	), nil
}

// checkWriteGuard enforces hard-denied system paths and sensitive secret/key path protection.
func (s *state) checkWriteGuard(ctx sdk.Context, absPath string) error {
	norm := filepath.ToSlash(filepath.Clean(absPath))
	if real, err := filepath.EvalSymlinks(absPath); err == nil {
		norm = filepath.ToSlash(filepath.Clean(real))
	} else if parentReal, err := filepath.EvalSymlinks(filepath.Dir(absPath)); err == nil {
		norm = filepath.ToSlash(filepath.Join(parentReal, filepath.Base(absPath)))
	}

	if isActiveHermesConfigPath(norm) {
		return fmt.Errorf("refusing to write active Hermes config file %q (protected against self-modification)", absPath)
	}
	if sensitiveExactPaths[norm] {
		return fmt.Errorf("refusing to write sensitive system path %q", absPath)
	}
	for _, prefix := range sensitivePathPrefixes {
		if strings.HasPrefix(norm, prefix) {
			return fmt.Errorf("refusing to write sensitive system path %q", absPath)
		}
	}

	// Secret/key path guard (bypassable via Yolo or explicit config flag).
	if s.cfg != nil && (s.cfg.IsYolo() || strings.EqualFold(s.cfg.Raw["file_tools.allow_sensitive_writes"], "true")) {
		return nil
	}

	reason := sensitiveSecretReason(norm)
	if reason == "" {
		return nil
	}

	if safeHasUI(ctx) {
		confirmed, err := ctx.Confirm(
			"Confirm sensitive file write",
			fmt.Sprintf("Write to %s (%s)?", absPath, reason),
		)
		if err != nil {
			return fmt.Errorf("sensitive path guard (%s): confirmation failed: %v", reason, err)
		}
		if ok, _ := confirmed.(bool); ok {
			return nil
		}
		return fmt.Errorf("write to sensitive path %q (%s) denied by user", absPath, reason)
	}

	return fmt.Errorf("refusing to write sensitive path %q (%s); enable yolo mode to override", absPath, reason)
}

func sensitiveSecretReason(slashPath string) string {
	lower := strings.ToLower(slashPath)
	base := filepath.Base(lower)

	if sensitiveBaseNames[base] {
		return "secret/credential file " + base
	}
	if strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".example") && !strings.HasSuffix(base, ".sample") && !strings.HasSuffix(base, ".template") {
		return "environment secret file " + base
	}
	ext := filepath.Ext(base)
	if ext == ".pem" || ext == ".key" || ext == ".p12" || ext == ".pfx" {
		return "private key/certificate file " + base
	}
	if strings.Contains(lower, "/.ssh/") || strings.HasSuffix(lower, "/.ssh") {
		return "SSH configuration/key path"
	}
	if strings.HasSuffix(lower, "/.aws/credentials") || strings.HasSuffix(lower, "/.kube/config") {
		return "cloud/cluster credential file"
	}
	return ""
}

// atomicWriteFile creates parent directories and atomically writes data via temp file + rename.
// If absPath is a symlink, it resolves the symlink target and atomically writes to the real target
// so the symlink itself is preserved.
func atomicWriteFile(absPath string, data []byte) error {
	targetPath := absPath
	if linfo, err := os.Lstat(absPath); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return fmt.Errorf("resolve symlink target %q: %w", absPath, err)
		}
		targetPath = resolved
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	perm := os.FileMode(0o644)
	if info, err := os.Stat(targetPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%q is a directory", targetPath)
		}
		perm = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".pig-hermes-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	cleanup = false
	return nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	trimmed := strings.TrimSuffix(s, "\n")
	return strings.Count(trimmed, "\n") + 1
}
