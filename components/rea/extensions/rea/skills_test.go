package rea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSkills(body string) map[string][]byte {
	return map[string][]byte{
		"pigpen-one/SKILL.md":            []byte("---\nname: pigpen-one\n---\n" + body),
		"pigpen-one/references/notes.md": []byte("notes\n"),
		"pigpen-two/SKILL.md":            []byte("---\nname: pigpen-two\n---\ntwo\n"),
		"stray.txt":                      []byte("not a skill\n"),
	}
}

// The skills are unpacked whole, once per content: the same Binary reuses its copy, and a Binary with other skill text
// gets a directory of its own, so neither reads the other's.
func TestEnsureSkillsUnpacksOncePerContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rea")
	set, err := ensureSkills(root, testSkills("one\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(set.names, ","); got != "pigpen-one,pigpen-two" {
		t.Fatalf("names = %q", got)
	}
	for name, want := range map[string]string{"pigpen-one/references/notes.md": "notes\n", "pigpen-two/SKILL.md": "---\nname: pigpen-two\n---\ntwo\n"} {
		got, err := os.ReadFile(filepath.Join(set.dir, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
	// A file changed on disk stays: the copy is complete, so it is not unpacked again.
	marker := filepath.Join(set.dir, "pigpen-two", "SKILL.md")
	if err := os.WriteFile(marker, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := ensureSkills(root, testSkills("one\n"))
	if err != nil || again.dir != set.dir {
		t.Fatalf("second start: %+v, %v", again, err)
	}
	if got, _ := os.ReadFile(marker); string(got) != "edited" {
		t.Errorf("a complete copy was unpacked again: %q", got)
	}
	other, err := ensureSkills(root, testSkills("one, changed\n"))
	if err != nil || other.dir == set.dir {
		t.Fatalf("changed skills: %+v, %v", other, err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Errorf("staging directory left behind: %s", e.Name())
		}
	}
	// gc keeps this Binary's skills and removes the other's.
	removed, err := gcSkills(root, other.dir)
	if err != nil || len(removed) != 1 || filepath.Join(root, removed[0]) != set.dir {
		t.Fatalf("gcSkills = %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(other.dir, "pigpen-one", "SKILL.md")); err != nil {
		t.Errorf("gc removed the kept skills: %v", err)
	}
}

// A Binary built without skills, or with files that are no skill, has none and says nothing about them; a path that
// would leave the directory is refused, not written.
func TestEnsureSkillsWithoutSkillsAndBadNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rea")
	for _, tree := range []map[string][]byte{nil, {}, {"README.md": []byte("x")}} {
		set, err := ensureSkills(root, tree)
		if err != nil || len(set.names) != 0 || skillsParagraph(set) != "" {
			t.Errorf("%v: set = %+v, %v", tree, set, err)
		}
	}
	for _, name := range []string{"../out/SKILL.md", "/abs/SKILL.md", "a/../../b/SKILL.md", "a//SKILL.md"} {
		if _, err := ensureSkills(root, map[string][]byte{name: []byte("x"), "ok/SKILL.md": []byte("y")}); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("nothing was to be unpacked, and the cache directory exists")
	}
}

// The model is told the file of every skill, at the place the files are.
func TestSkillsParagraphNamesTheFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rea")
	set, err := ensureSkills(root, testSkills("one\n"))
	if err != nil {
		t.Fatal(err)
	}
	text := skillsParagraph(set)
	for _, name := range set.names {
		file := filepath.Join(set.dir, name, "SKILL.md")
		if !strings.Contains(text, "- "+name+": "+file+"\n") {
			t.Errorf("paragraph lacks %s:\n%s", file, text)
		}
		if _, err := os.Stat(file); err != nil {
			t.Errorf("the paragraph names a file that is not there: %v", err)
		}
	}
}
