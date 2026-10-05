package pi

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// DeleteResult is the outcome of deleting a session file.
type DeleteResult struct {
	OK bool
	// Method is "trash", "unlink" or "missing".
	Method string
	Error  string
}

func isMissing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// DeleteSessionFile removes a session file the way Pi's own /resume delete does: through the
// system trash when there is one, else by unlinking. Success is judged by the file being gone,
// not by any exit status: a wrapper or broken installation can exit successfully without moving
// the file, and a trash that moved it may still exit non-zero (port of src/pi/delete-session.ts).
func DeleteSessionFile(path string) DeleteResult {
	if isMissing(path) {
		return DeleteResult{OK: true, Method: "missing"}
	}
	// "--" guards against a path that looks like a flag.
	args := []string{path}
	if strings.HasPrefix(path, "-") {
		args = []string{"--", path}
	}
	if bin, err := exec.LookPath("trash"); err == nil {
		_ = exec.Command(bin, args...).Run()
	}
	if isMissing(path) {
		return DeleteResult{OK: true, Method: "trash"}
	}
	if err := unlink(path); err != nil {
		// Another process deleting the same session is still the desired outcome.
		if isMissing(path) {
			return DeleteResult{OK: true, Method: "missing"}
		}
		return DeleteResult{OK: false, Method: "unlink", Error: err.Error()}
	}
	return DeleteResult{OK: true, Method: "unlink"}
}

// unlink removes a file but never a directory (os.Remove would also rmdir an empty one).
func unlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &os.PathError{Op: "unlink", Path: path, Err: errors.New("is a directory")}
	}
	return os.Remove(path)
}

// FileDeleter adapts DeleteSessionFile to the registry's deletion boundary.
func FileDeleter(path string) (SessionFileDeletionResult, error) {
	r := DeleteSessionFile(path)
	return SessionFileDeletionResult{OK: r.OK, Error: r.Error}, nil
}
