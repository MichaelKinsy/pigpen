// Package install copies the Pigtorio mod, layout and engine binary into a Herdr app home.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Reserved are the names in installed.json that Pigtorio must not replace.
var Reserved = []string{"pigtorio", "layout:pigtorio"}

// CheckHome refuses a home whose installed.json lists a Herdr-shipped Pigtorio.
func CheckHome(home string) error {
	b, err := os.ReadFile(filepath.Join(home, "installed.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("installed.json: %w", err)
	}
	for _, name := range Reserved {
		if _, ok := m[name]; ok {
			return fmt.Errorf("%s lists %q: Herdr ships that name, refusing to replace it", filepath.Join(home, "installed.json"), name)
		}
	}
	return nil
}

// within says whether path p is dir itself or lies inside it (both made real first).
func within(p, dir string) bool {
	rp, rd := real(p), real(dir)
	rel, err := filepath.Rel(rd, rp)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// real resolves symlinks of the longest existing prefix of p.
func real(p string) string {
	p = filepath.Clean(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	var rest []string
	for q := p; ; q = filepath.Dir(q) {
		if r, err := filepath.EvalSymlinks(q); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		if filepath.Dir(q) == q {
			return p
		}
		rest = append([]string{filepath.Base(q)}, rest...)
	}
}

// FindSource looks for a repo root (one with mods/pigtorio) near the executable or the cwd,
// never inside the Herdr home it is about to install into.
func FindSource(exe, home string) (string, error) {
	var cands []string
	if exe != "" {
		d := filepath.Dir(exe)
		cands = append(cands, filepath.Join(d, "..", ".."), filepath.Join(d, ".."), d)
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd, filepath.Join(wd, ".."))
	}
	for _, c := range cands {
		if home != "" && within(c, home) {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "mods", "pigtorio")); err == nil && st.IsDir() {
			return filepath.Clean(c), nil
		}
	}
	return "", errors.New("cannot find the Pigtorio sources (mods/pigtorio); pass --src DIR")
}

// Run installs from src into home. exe is the engine binary to copy to H/pigtorio/bin/pigtorio.
// The mod is copied into a staging folder first and swapped in by rename, so a failed copy
// leaves the installed mod as it was.
func Run(home, src, exe string) error {
	if err := CheckHome(home); err != nil {
		return err
	}
	mod := filepath.Join(src, "mods", "pigtorio")
	layout := filepath.Join(src, "layouts", "pigtorio.json")
	if st, err := os.Stat(mod); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", mod)
	}
	if _, err := os.Stat(layout); err != nil {
		return fmt.Errorf("missing %s", layout)
	}
	dst := filepath.Join(home, "mods", "pigtorio")
	if within(mod, dst) || within(dst, mod) {
		return fmt.Errorf("the source mod folder %s is the installed one (%s) or overlaps it: pass --src with the Pigtorio repo", mod, dst)
	}
	work := filepath.Join(home, "pigtorio", ".install")
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	staged, old := filepath.Join(work, "mod"), filepath.Join(work, "old")
	if err := copyDir(mod, staged); err != nil {
		return fmt.Errorf("copying the mod: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	hadOld := false
	if _, err := os.Lstat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
		hadOld = true
	}
	if err := os.Rename(staged, dst); err != nil {
		if hadOld {
			_ = os.Rename(old, dst)
		}
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, "layouts"), 0o755); err != nil {
		return err
	}
	if err := copyFile(layout, filepath.Join(home, "layouts", "pigtorio.json"), 0o644); err != nil {
		return err
	}
	// The picture Herdr's settings show for the layout (the layout's "preview").
	if preview := filepath.Join(src, "layouts", "pigtorio.png"); fileExists(preview) {
		if err := copyFile(preview, filepath.Join(home, "layouts", "pigtorio.png"), 0o644); err != nil {
			return err
		}
	}
	if exe != "" {
		bin := filepath.Join(home, "pigtorio", "bin", "pigtorio")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			return err
		}
		if a, err := filepath.EvalSymlinks(exe); err == nil {
			if b, err := filepath.EvalSymlinks(bin); err == nil && a == b {
				return nil
			}
		}
		if err := copyFile(exe, bin, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := os.Stat(p) // follows symlinks: real copies
		if err != nil {
			return err
		}
		if info.IsDir() {
			return copyDir(p, target)
		}
		return copyFile(p, target, info.Mode().Perm())
	})
}

// copyFile writes through a temp file and renames, so a running binary can be replaced.
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
