//go:build unix

package autolearn

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:138-160

func assertManagedSkillFileSafeForUpdate(name string, fileStat os.FileInfo) error {
	if !fileStat.Mode().IsRegular() {
		return fmt.Errorf("Managed skill %q SKILL.md is not a regular file; refusing to overwrite it.", name)
	}
	if st, ok := fileStat.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return fmt.Errorf(
			"Managed skill %q SKILL.md has %d hard links; refusing to overwrite a file that may be user-authored elsewhere.",
			name, st.Nlink,
		)
	}
	return nil
}

func assertOpenedFileSafeForUpdate(_ string, _ *os.File) error {
	return nil
}

func openManagedSkillFileForUpdate(name, file string) (*os.File, error) {
	f, err := os.OpenFile(file, os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("Managed skill %q SKILL.md is a symlink; refusing to overwrite it.", name)
		}
		return nil, err
	}
	return f, nil
}
