//go:build windows

package autolearn

import (
	"fmt"
	"os"
	"syscall"
)

// upstream: packages/coding-agent/src/autolearn/managed-skills.ts:138-160

func assertManagedSkillFileSafeForUpdate(name string, fileStat os.FileInfo) error {
	if !fileStat.Mode().IsRegular() {
		return fmt.Errorf("Managed skill %q SKILL.md is not a regular file; refusing to overwrite it.", name)
	}
	return nil
}

func assertOpenedFileSafeForUpdate(name string, f *os.File) error {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks > 1 {
		return fmt.Errorf(
			"Managed skill %q SKILL.md has %d hard links; refusing to overwrite a file that may be user-authored elsewhere.",
			name, info.NumberOfLinks,
		)
	}
	return nil
}

func openManagedSkillFileForUpdate(name, file string) (*os.File, error) {
	st, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if st.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, fmt.Errorf("Managed skill %q SKILL.md is a symlink; refusing to overwrite it.", name)
	}
	return os.OpenFile(file, os.O_WRONLY, 0o644)
}
