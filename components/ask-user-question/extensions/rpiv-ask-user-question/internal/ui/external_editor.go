package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ExternalEditorTui is the TUI lifecycle the external editor needs. upstream: ExternalEditorTui.
type ExternalEditorTui interface {
	Stop()
	Start()
	RequestRender(force bool)
}

// ExternalEditorBanner receives the "Launching external editor" notice the original writes to process.stdout;
// a subprocess extension owns no terminal, so it is injectable. nil discards it.
var ExternalEditorBanner io.Writer = os.Stdout

var trailingNewline = regexp.MustCompile(`\r?\n\z`)

// runEditor keeps the command grammar identical to Pi's built-in external-editor flow: split on single spaces.
func runEditor(command, file string) error {
	parts := strings.Split(command, " ")
	editor, args := parts[0], parts[1:]
	if editor == "" {
		return errors.New("External editor command is empty")
	}
	cmd := exec.Command(editor, append(append([]string{}, args...), file)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if err == nil {
		return nil
	}
	if !errors.As(err, &exitErr) {
		return err
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return fmt.Errorf("External editor exited with signal %s", signalName(ws.Signal()))
	}
	code := exitErr.ExitCode()
	if code < 0 {
		return errors.New("External editor exited with exit code unknown")
	}
	return fmt.Errorf("External editor exited with exit code %d", code)
}

func signalName(sig syscall.Signal) string {
	names := map[syscall.Signal]string{
		syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT", syscall.SIGKILL: "SIGKILL",
		syscall.SIGSEGV: "SIGSEGV", syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
		syscall.SIGABRT: "SIGABRT", syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGILL: "SIGILL",
	}
	if n, ok := names[sig]; ok {
		return n
	}
	return sig.String()
}

// EditWithExternalEditor edits a custom answer with Pi's configured external-editor command. The TUI lifecycle
// and one-trailing-newline normalization intentionally match Pi's main editor flow. upstream: editWithExternalEditor.
func EditWithExternalEditor(tui ExternalEditorTui, command, value string) (result string, err error) {
	tempDir, err := os.MkdirTemp("", "rpiv-ask-user-question-")
	if err != nil {
		return "", err
	}
	tempFile := filepath.Join(tempDir, "answer.md")
	tuiStopped := false
	defer func() {
		// Temp cleanup is best effort; never leave the TUI stopped because it failed.
		_ = os.RemoveAll(tempDir)
		if tuiStopped {
			tui.Start()
			tui.RequestRender(true)
		}
	}()

	if err := os.WriteFile(tempFile, []byte(value), 0o644); err != nil {
		return "", err
	}
	tui.Stop()
	tuiStopped = true
	if ExternalEditorBanner != nil {
		fmt.Fprintf(ExternalEditorBanner, "Launching external editor: %s\nPi will resume when the editor exits.\n", command)
	}
	if err := runEditor(command, tempFile); err != nil {
		return "", err
	}
	data, err := os.ReadFile(tempFile)
	if err != nil {
		return "", err
	}
	return trailingNewline.ReplaceAllString(string(data), ""), nil
}
