//go:build !unix && !windows

package autolearn

import (
	"fmt"
	"os"
)

// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:138-160

func assertManagedSkillFileSafeForUpdate(name string, fileStat os.FileInfo) error {
	if !fileStat.Mode().IsRegular() {
		return fmt.Errorf("Managed skill %q SKILL.md is not a regular file; refusing to overwrite it.", name)
	}
	return nil
}

func assertOpenedFileSafeForUpdate(_ string, _ *os.File) error {
	return nil
}

func openManagedSkillFileForUpdate(name, file string) (*os.File, error) {
	// Windows lacks O_NOFOLLOW; verify via Lstat before opening.
	st, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Managed skill %q SKILL.md is a symlink; refusing to overwrite it.", name)
	}
	return os.OpenFile(file, os.O_WRONLY, 0o644)
}
