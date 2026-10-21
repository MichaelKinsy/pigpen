package pi_goal_x

// ported from storage/goal-root.ts: where the goal pool lives (.pi/goals under cwd, or the configured goalsRoot).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// goalSessionManager is the part of the host's session manager the root cache keys on.
type goalSessionManager interface {
	getSessionId() (string, bool)
}

// goalStorageContext: goalStorageRoot, when set, is a plain snapshot for deferred readers (never capture a host context).
type goalStorageContext struct {
	cwd             string
	goalStorageRoot *string
	sessionManager  goalSessionManager
}

type pinnedGoalRoot struct {
	id    string
	hasID bool
	cwd   string
	root  string
}

// Host contexts change between hooks; the session manager (or, without one, the context) owns the pinned root's lifetime.
var (
	goalRootsMu sync.Mutex
	goalRoots   = map[any]pinnedGoalRoot{}
)

func defaultGoalRoot(cwd string) string { return absClean(filepath.Join(cwd, ".pi/goals")) }

func goalStorageRoot(ctx *goalStorageContext) (string, error) {
	if ctx.goalStorageRoot != nil {
		return *ctx.goalStorageRoot, nil
	}
	// Low-level cwd-only callers retain the original zero-settings-I/O contract.
	if ctx.sessionManager != nil {
		return configuredGoalRoot(ctx)
	}
	return defaultGoalRoot(ctx.cwd), nil
}

func configuredGoalRoot(ctx *goalStorageContext) (string, error) {
	if ctx.goalStorageRoot != nil {
		return *ctx.goalStorageRoot, nil
	}
	var id string
	var hasID bool
	var key any = ctx
	if ctx.sessionManager != nil {
		id, hasID = ctx.sessionManager.getSessionId()
		key = ctx.sessionManager
	}
	goalRootsMu.Lock()
	pinned, ok := goalRoots[key]
	goalRootsMu.Unlock()
	if ok && pinned.id == id && pinned.hasID == hasID && pinned.cwd == ctx.cwd {
		return pinned.root, nil
	}
	settings := loadSettingsSnapshot(ctx.cwd)
	source := ""
	if r := settings.provenance["goalsRoot"]; r != nil {
		source = r.source
	}
	for _, d := range settings.diagnostics {
		if d.settingPath == "goalsRoot" && source != "environment" && (d.scope == settingsScopeProject || source != "project") {
			return "", fmt.Errorf("Invalid goalsRoot: %s", d.message)
		}
	}
	root := defaultGoalRoot(ctx.cwd)
	if configured := settings.value.goalsRoot; configured != nil {
		expanded := *configured
		switch {
		case expanded == "~":
			expanded = homeDir()
		case strings.HasPrefix(expanded, "~/"):
			expanded = filepath.Join(homeDir(), expanded[2:])
		}
		if !filepath.IsAbs(expanded) || strings.Contains(expanded, "\x00") {
			return "", errors.New("goalsRoot / PI_GOAL_ROOT must be an absolute path or ~/path; reload after correcting it.")
		}
		root = settingsPathNormalize(expanded)
		if err := os.MkdirAll(root, 0o777); err != nil {
			return "", err
		}
		if fi, err := os.Lstat(root); err != nil {
			return "", err
		} else if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Goal root is a symlink: %s", root)
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		root = real
		// Metadata lives inside an override without perturbing the pool's mtime.
		metadata := filepath.Join(root, ".metadata")
		if err := os.MkdirAll(metadata, 0o777); err != nil {
			return "", err
		}
		if fi, err := os.Lstat(metadata); err != nil {
			return "", err
		} else if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Goal metadata directory is a symlink: %s", metadata)
		}
	}
	goalRootsMu.Lock()
	goalRoots[key] = pinnedGoalRoot{id: id, hasID: hasID, cwd: ctx.cwd, root: root}
	goalRootsMu.Unlock()
	return root, nil
}

func goalStorageContextOf(ctx *goalStorageContext) (*goalStorageContext, error) {
	root, err := configuredGoalRoot(ctx)
	if err != nil {
		return nil, err
	}
	return &goalStorageContext{cwd: ctx.cwd, goalStorageRoot: &root}, nil
}

// goalStoragePath maps a logical ".pi/goals/..." path (compatible in saved goal files, independent of pool location) to the
// real one.
func goalStoragePath(ctx *goalStorageContext, logical string) (string, error) {
	relative := strings.ReplaceAll(logical, "\\", "/")
	if relative != ".pi/goals" && !strings.HasPrefix(relative, ".pi/goals/") {
		return "", fmt.Errorf("Invalid goal storage path: %s", logical)
	}
	root, err := goalStorageRoot(ctx)
	if err != nil {
		return "", err
	}
	sub := ""
	if len(relative) > len(".pi/goals") {
		sub = relative[len(".pi/goals")+1:]
	}
	result := absClean(filepath.Join(root, filepath.FromSlash(sub)))
	inside, rerr := filepath.Rel(root, result)
	if rerr != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("Goal path escapes root: %s", logical)
	}
	return result, nil
}

func isExternalGoalStorage(ctx *goalStorageContext) (bool, error) {
	root, err := goalStorageRoot(ctx)
	if err != nil {
		return false, err
	}
	return root != defaultGoalRoot(ctx.cwd), nil
}

func goalPoolSnapshotPath(ctx *goalStorageContext) (string, error) {
	root, err := goalStorageRoot(ctx)
	if err != nil {
		return "", err
	}
	if root == defaultGoalRoot(ctx.cwd) {
		return filepath.Join(filepath.Dir(root), ".goals-pool-snapshot.json"), nil
	}
	return filepath.Join(root, ".metadata", ".goals-pool-snapshot.json"), nil
}

// ensureGoalStorageDirectory validates managed ancestors on writes, without adding I/O to steady-state reads.
func ensureGoalStorageDirectory(ctx *goalStorageContext, logical string, verifyDefault bool) error {
	root, err := goalStorageRoot(ctx)
	if err != nil {
		return err
	}
	target, err := goalStoragePath(ctx, logical)
	if err != nil {
		return err
	}
	if root == defaultGoalRoot(ctx.cwd) {
		if err := os.MkdirAll(target, 0o777); err != nil {
			return err
		}
		if fi, err := os.Lstat(target); verifyDefault && (err != nil || fi.Mode()&os.ModeSymlink != 0) {
			if err != nil {
				return err
			}
			return fmt.Errorf("Goal directory is a symlink: %s", target)
		}
		return nil
	}
	// An external pool removed during a session must not silently reappear.
	if fi, err := os.Lstat(root); err != nil {
		return err
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Goal root is a symlink: %s", root)
	}
	dir := root
	rel, _ := filepath.Rel(root, target)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o777); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if fi, err := os.Lstat(dir); err != nil {
			return err
		} else if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Goal directory is a symlink: %s", dir)
		}
	}
	return nil
}
