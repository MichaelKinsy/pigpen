// Package svc holds the services that expose the machine to a remote client: file resources,
// resource watches and terminals. Every one is opt-in and confined; nothing here runs unless the
// extension was explicitly configured to enable it (port of pi-ahp's resource-*, terminal
// services).
package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// canonicalPath resolves symlinks in path even when the path (or its tail) does not exist yet: a
// dangling symlink is followed to where it would create the file, so a write through a link that
// points outside the permitted roots cannot slip past the check.
func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(absolute); err == nil {
		return real, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(absolute)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(absolute), target)
			}
			return canonicalPath(target)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return absolute, nil
	}
	canonicalParent, err := canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonicalParent, filepath.Base(absolute)), nil
}

// PathPolicy turns file: URIs into local paths and, when roots are configured, refuses anything
// that resolves outside them. With no roots every path is allowed; the extension never builds a
// policy without roots unless the user explicitly asked for an unrestricted one.
type PathPolicy struct{ roots []string }

// NewPathPolicy creates a policy over roots (each must exist; they are canonicalised).
func NewPathPolicy(roots ...string) (*PathPolicy, error) {
	p := &PathPolicy{}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resource root %s: %w", root, err)
		}
		p.roots = append(p.roots, real)
	}
	return p, nil
}

// Restricted reports whether the policy confines paths to roots.
func (p *PathPolicy) Restricted() bool { return len(p.roots) > 0 }

// PathFor returns the local path of a file: URI, or a protocol error: InvalidParams for a wrong
// scheme or malformed URI, PermissionDenied for a path outside the roots.
func (p *PathPolicy) PathFor(uri string) (string, error) {
	if !strings.HasPrefix(uri, "file://") {
		return "", wire.InvalidParams("Only file: URIs are supported, got: " + uri)
	}
	path, err := wire.FileURIToPath(uri)
	if err != nil {
		return "", wire.InvalidParams("Malformed file URI: " + uri)
	}
	if len(p.roots) == 0 {
		return path, nil
	}
	candidate, err := canonicalPath(path)
	if err != nil {
		return "", wire.Coded(wire.CodePermissionDenied, err.Error())
	}
	for _, root := range p.roots {
		prefix := root
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if candidate == root || strings.HasPrefix(candidate, prefix) {
			return path, nil
		}
	}
	return "", wire.Coded(wire.CodePermissionDenied, "Outside the permitted roots: "+uri)
}
