package rea

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// The Piglet's skills as files.
//
// A Binary bakes only the body of each SKILL.md. PiG lists a baked skill to the model with the location "builtin:piglet"
// and tells it to load the skill's file with the read tool; no tool can read that location, so the model cannot load the
// skill, and the files a skill links (references/) are not in the Binary at all. Only a user who types /skill:<name>
// gets the body. The member therefore carries the skill trees (payload/skills/<name>/, written by scripts/payload.mjs
// from components/rea/skills), unpacks them beside REA, and tells the model where they are.

// skillsPrefix starts the name of the directory the skills are unpacked into: skills-<first 16 hex of their digest>.
const skillsPrefix = "skills-"

// skillsSentinel is written last into an unpacked skills directory and holds the digest of the embedded trees.
const skillsSentinel = ".rea-skills"

// skillSet is the unpacked skills: the directory that holds one directory per skill, and the skills' names.
type skillSet struct {
	dir   string
	names []string
}

// embeddedSkills is payload/skills.json of this Binary as slash path -> bytes, or nil when the Binary was built without it.
// The trees travel as one file: a directory of SKILL.md copies would be a second set of skills to Pigpen's rules.
func embeddedSkills() map[string][]byte {
	data, err := payloadFS.ReadFile("payload/skills.json")
	if err != nil {
		return nil
	}
	var doc struct {
		Files map[string][]byte `json:"files"` // encoding/json reads base64 into []byte
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc.Files
}

// skillFile is one file of the embedded skills.
type skillFile struct {
	name string // slash path below the skills root: <skill>/SKILL.md, <skill>/references/x.md
	data []byte
}

// readSkills returns the files sorted by name, with the digest of the whole (each file's name, length and bytes) and the
// names of the top-level directories that hold a SKILL.md. A name that is not a clean relative slash path is refused.
func readSkills(tree map[string][]byte) (files []skillFile, names []string, digest string, err error) {
	for name, data := range tree {
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return nil, nil, "", fmt.Errorf("%q: not a relative file path", name)
		}
		files = append(files, skillFile{name: name, data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	sum := sha256.New()
	for _, f := range files {
		fmt.Fprintf(sum, "%s\x00%d\x00", f.name, len(f.data))
		sum.Write(f.data)
		if skill, rest, ok := strings.Cut(f.name, "/"); ok && rest == "SKILL.md" {
			names = append(names, skill)
		}
	}
	return files, names, hex.EncodeToString(sum.Sum(nil)), nil
}

// skillsComplete reports whether dir holds the unpacked skills whose digest is sum.
func skillsComplete(dir, sum string) bool {
	data, err := os.ReadFile(filepath.Join(dir, skillsSentinel))
	return err == nil && strings.TrimSpace(string(data)) == sum
}

// ensureSkills unpacks the embedded skills under root when no complete copy of them is there, the way ensureUnpacked
// does for REA: into a staging directory, the sentinel last, then one rename. A Binary without skills gives an empty set.
func ensureSkills(root string, tree map[string][]byte) (skillSet, error) {
	files, names, digest, err := readSkills(tree)
	if err != nil {
		return skillSet{}, fmt.Errorf("embedded skills: %w", err)
	}
	if len(names) == 0 {
		return skillSet{}, nil
	}
	set := skillSet{dir: filepath.Join(root, skillsPrefix+digest[:16]), names: names}
	if skillsComplete(set.dir, digest) {
		return set, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return skillSet{}, fmt.Errorf("REA cache %s: %w", root, err)
	}
	staging, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		return skillSet{}, fmt.Errorf("REA cache %s: %w", root, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, f := range files {
		target := filepath.Join(staging, filepath.FromSlash(f.name)) // readSkills refused every name that leaves the root
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return skillSet{}, err
		}
		if err := os.WriteFile(target, f.data, 0o644); err != nil {
			return skillSet{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(staging, skillsSentinel), []byte(digest+"\n"), 0o644); err != nil {
		return skillSet{}, err
	}
	if err := os.Rename(staging, set.dir); err != nil {
		if skillsComplete(set.dir, digest) {
			return set, nil
		}
		if rmErr := os.RemoveAll(set.dir); rmErr != nil {
			return skillSet{}, fmt.Errorf("replace incomplete %s: %w", set.dir, rmErr)
		}
		if err := os.Rename(staging, set.dir); err != nil {
			return skillSet{}, fmt.Errorf("install %s: %w", set.dir, err)
		}
	}
	return set, nil
}

// skillsParagraph tells the model where the skills are. PiG's own list names them with a location nothing can read.
func skillsParagraph(set skillSet) string {
	if len(set.names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The skills of this harness are files on disk. Where the skills list gives the location builtin:piglet, " +
		"load the skill with the read tool from here instead:\n")
	for _, name := range set.names {
		b.WriteString("- " + name + ": " + filepath.Join(set.dir, name, "SKILL.md") + "\n")
	}
	b.WriteString("Paths inside a skill (for example references/...) are relative to the directory of its SKILL.md.")
	return b.String()
}

// gcSkills removes the unpacked skills of other Binaries: every skills-* directory under root but keep.
func gcSkills(root, keep string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, skillsPrefix) || filepath.Join(root, name) == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, errors.Join(errs...)
}
