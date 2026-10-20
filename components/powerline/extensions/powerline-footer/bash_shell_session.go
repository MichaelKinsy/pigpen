package powerline_footer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// The managed shell session: one persistent shell process that bash mode feeds commands to, and whose cwd and exit codes it
// tracks through marker lines. upstream: bash-mode/shell-session.ts.
//
// The callbacks (onStateChange, onCommandSuccess) run on the shell's output goroutines, in the order the lines arrive. They
// may read the session (State) and the transcript but MUST NOT block on anything a handler holds (the extension's mutex): they
// are signals, as requestStatusRender is upstream.

const (
	readySentinel        = "__PI_READY__"
	commandStartSentinel = "__PI_CMD_START__"
	commandDoneSentinel  = "__PI_CMD_DONE__"
)

var (
	ansiCSI     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]") })
	ansiOSC     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("\x1b\\][^\x07]*(?:\x07|\x1b\\\\)") })
	controlByte = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]") })
)

// stripShellAnsi upstream: shell-session.ts:12: CSI and OSC sequences and C0 controls (except tab and line feed) removed.
func stripShellAnsi(value string) string {
	return controlByte().ReplaceAllString(ansiOSC().ReplaceAllString(ansiCSI().ReplaceAllString(value, ""), ""), "")
}

// quoteShellArg upstream: shell-session.ts:19.
func quoteShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// getShellInitScript upstream: shell-session.ts:44: the function that runs one command file and prints the markers, per shell.
func getShellInitScript(shellName string) string {
	if strings.Contains(shellName, "fish") {
		return `
function __pi_eval
  set -l __pi_id $argv[1]
  set -l __pi_file $argv[2]
  echo "` + commandStartSentinel + `:$__pi_id:$PWD"
  source $__pi_file
  set -l __pi_status $status
  rm -f $__pi_file
  echo "` + commandDoneSentinel + `:$__pi_id:$__pi_status:$PWD"
end
echo "` + readySentinel + `:$PWD"
`
	}
	if strings.Contains(shellName, "bash") {
		return `
__pi_eval() {
  local __pi_id="$1"
  local __pi_file="$2"
  printf '%s:%s:%s\n' '` + commandStartSentinel + `' "$__pi_id" "$PWD"
  source "$__pi_file"
  local __pi_status=$?
  rm -f "$__pi_file"
  printf '%s:%s:%s:%s\n' '` + commandDoneSentinel + `' "$__pi_id" "$__pi_status" "$PWD"
}
printf '%s:%s\n' '` + readySentinel + `' "$PWD"
`
	}
	return `
function __pi_eval() {
  local __pi_id="$1"
  local __pi_file="$2"
  print -r -- "` + commandStartSentinel + `:$__pi_id:$PWD"
  builtin source "$__pi_file"
  local __pi_status=$?
  rm -f "$__pi_file"
  print -r -- "` + commandDoneSentinel + `:$__pi_id:$__pi_status:$PWD"
}
print -r -- "` + readySentinel + `:$PWD"
`
}

// jsParseInt10 is Number.parseInt(text, 10): optional whitespace and sign, then the leading digits; ok is false for NaN.
func jsParseInt10(text string) (int, bool) {
	t := strings.TrimLeftFunc(text, isJSSpace)
	i := 0
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		i++
	}
	start := i
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == start {
		return 0, false
	}
	n, err := strconv.Atoi(t[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

// shellLaunch is one start of the shell process; ensureReady callers wait on its done channel.
type shellLaunch struct {
	done chan struct{}
	err  error
}

func (l *shellLaunch) finish(err error) {
	select {
	case <-l.done:
	default:
		l.err = err
		close(l.done)
	}
}

// managedShellSession is ManagedShellSession.
type managedShellSession struct {
	shellPath        string
	transcript       *bashTranscriptStore
	onStateChange    func()
	onCommandSuccess func(command, cwd string)
	tempDir          string
	chunkMu          sync.Mutex // serialises handleChunk, as one event loop does upstream
	mu               sync.Mutex // guards the fields below
	state            shellSessionState
	cmd              *exec.Cmd
	zombie           *exec.Cmd // the process dispose killed, whose close is still to come
	stdin            io.WriteCloser
	buffer           string
	commandCounter   int
	currentCommandID string
	launch           *shellLaunch
	disposed         bool
}

// newManagedShellSession upstream: shell-session.ts:100. The command files go into a private temp directory.
func newManagedShellSession(shellPath, cwd string, transcript *bashTranscriptStore, onStateChange func(), onCommandSuccess func(command, cwd string)) (*managedShellSession, error) {
	tempDir, err := os.MkdirTemp("", "powerline-bash-mode-")
	if err != nil {
		return nil, err
	}
	shellName := strings.ToLower(filepath.Base(shellPath))
	return &managedShellSession{
		shellPath: shellPath, transcript: transcript, onStateChange: onStateChange, onCommandSuccess: onCommandSuccess, tempDir: tempDir,
		state: shellSessionState{ShellPath: shellPath, ShellName: shellName, Cwd: cwd},
	}, nil
}

// State is a copy of the session state.
func (s *managedShellSession) State() shellSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	if st.LastExitCode != nil {
		code := *st.LastExitCode
		st.LastExitCode = &code
	}
	return st
}

// spawnError renders a start failure as Node's spawn error text.
func spawnError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("spawn %s ENOENT", path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("spawn %s EACCES", path)
	}
	return err
}

// ensureReady starts the shell if needed and waits until it printed its ready marker. upstream: shell-session.ts:122. A caller
// whose ctx ends stops waiting; the shell keeps starting. A session disposed while a caller waits releases it with an error
// (upstream leaves that promise pending forever).
func (s *managedShellSession) ensureReady(ctx context.Context) error {
	s.mu.Lock()
	if s.state.Ready {
		s.mu.Unlock()
		return nil
	}
	launch := s.launch
	if launch == nil {
		var err error
		if launch, err = s.startLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	select {
	case <-launch.done:
		return launch.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startLocked spawns the shell and sends its init script. s.mu is held.
func (s *managedShellSession) startLocked() (*shellLaunch, error) {
	launch := &shellLaunch{done: make(chan struct{})}
	cmd := exec.Command(s.shellPath)
	cmd.Dir = s.state.Cwd
	cmd.Env = append(os.Environ(), "DISABLE_AUTO_UPDATE=true", "DISABLE_UPDATE_PROMPT=true")
	configureShellProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, spawnError(s.shellPath, err)
	}
	s.cmd, s.stdin, s.launch, s.disposed = cmd, stdin, launch, false
	var readers sync.WaitGroup
	for _, r := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		go func() { defer readers.Done(); s.readStream(r) }()
	}
	go func() {
		readers.Wait() // "close" follows the end of both streams, as in Node
		_ = cmd.Wait()
		exitCode := 1
		if cmd.ProcessState != nil {
			exitCode = closeExitCode(cmd.ProcessState)
		}
		s.handleClose(cmd, launch, exitCode)
	}()
	if _, err := io.WriteString(stdin, getShellInitScript(s.state.ShellName)+"\n"); err != nil {
		return launch, nil // the close handler reports a shell that died at once
	}
	return launch, nil
}

// readStream decodes one output stream as UTF-8 (a character split across reads is held back) and feeds handleChunk.
func (s *managedShellSession) readStream(r io.Reader) {
	buf := make([]byte, 32*1024)
	var carry []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := append(carry, buf[:n]...)
			cut := len(data)
			for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
				if utf8.RuneStart(data[len(data)-back]) {
					if !utf8.FullRune(data[len(data)-back:]) {
						cut = len(data) - back
					}
					break
				}
			}
			carry = append([]byte{}, data[cut:]...)
			s.handleChunk(strings.ToValidUTF8(string(data[:cut]), "\uFFFD"))
		}
		if err != nil {
			if len(carry) > 0 {
				s.handleChunk(strings.ToValidUTF8(string(carry), "\uFFFD"))
			}
			return
		}
	}
}

// handleClose is the "close" listener. upstream: shell-session.ts:155.
func (s *managedShellSession) handleClose(cmd *exec.Cmd, launch *shellLaunch, exitCode int) {
	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()
	s.mu.Lock()
	if s.cmd != cmd && s.zombie != cmd { // an older launch: only its waiters are released
		s.mu.Unlock()
		launch.finish(errors.New("shell session disposed"))
		return
	}
	if !s.state.Ready {
		if s.disposed {
			launch.finish(errors.New("shell session disposed"))
		} else {
			launch.finish(fmt.Errorf("Shell failed to start (exit %d)", exitCode))
		}
	}
	if id := s.currentCommandID; id != "" {
		s.transcript.finishCommand(id, exitCode)
		s.state.LastExitCode = &exitCode
		s.currentCommandID = ""
	}
	if s.cmd == cmd {
		s.cmd, s.stdin = nil, nil
	}
	if s.zombie == cmd {
		s.zombie = nil
	}
	if s.launch == launch {
		s.launch = nil
	}
	s.buffer = ""
	s.state.Ready, s.state.Running = false, false
	s.mu.Unlock()
	s.onStateChange()
}

// runCommand writes the command to a file and has the shell source it. upstream: shell-session.ts:187.
func (s *managedShellSession) runCommand(ctx context.Context, command string) error {
	if err := s.ensureReady(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if s.cmd == nil {
		s.mu.Unlock()
		return errors.New("Shell process not available")
	}
	if s.state.Running {
		s.mu.Unlock()
		return errors.New("Shell command already running")
	}
	s.commandCounter++
	id := fmt.Sprintf("cmd-%d", s.commandCounter)
	extension := "sh"
	if strings.Contains(s.state.ShellName, "fish") {
		extension = "fish"
	}
	filePath := filepath.Join(s.tempDir, id+"."+extension)
	body := command
	if !strings.HasSuffix(command, "\n") {
		body += "\n"
	}
	if err := os.MkdirAll(s.tempDir, 0o700); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := os.WriteFile(filePath, []byte(body), 0o666); err != nil {
		s.mu.Unlock()
		return err
	}
	s.currentCommandID = id
	s.state.Running = true
	cwd := s.state.Cwd
	stdin := s.stdin
	s.transcript.startCommand(id, command, cwd)
	s.mu.Unlock()
	s.onStateChange()
	_, err := io.WriteString(stdin, "__pi_eval "+quoteShellArg(id)+" "+quoteShellArg(filePath)+"\n")
	return err
}

// interrupt sends SIGINT to the shell's process group while a command runs. upstream: shell-session.ts:205.
func (s *managedShellSession) interrupt() {
	s.mu.Lock()
	cmd, running := s.cmd, s.state.Running
	s.mu.Unlock()
	if cmd == nil || !running {
		return
	}
	_ = signalShellGroup(cmd.Process, shellSignalInterrupt)
}

// dispose kills the shell and everything in its process group, and removes the command files. upstream: shell-session.ts:216.
func (s *managedShellSession) dispose() {
	s.mu.Lock()
	s.disposed = true
	cmd := s.cmd
	if cmd != nil {
		s.zombie = cmd
	}
	s.cmd, s.stdin, s.launch = nil, nil, nil
	s.mu.Unlock()
	if cmd != nil {
		_ = signalShellGroup(cmd.Process, shellSignalKill)
	}
	_ = os.RemoveAll(s.tempDir)
}

// handleChunk upstream: shell-session.ts:238: split the output into lines, recognise the marker lines, and hand the rest to
// the transcript.
func (s *managedShellSession) handleChunk(chunk string) {
	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()
	sanitized := strings.ReplaceAll(stripShellAnsi(chunk), "\r", "")
	if sanitized == "" {
		return
	}
	s.mu.Lock()
	s.buffer += sanitized
	parts := strings.Split(s.buffer, "\n")
	s.buffer = parts[len(parts)-1]
	parts = parts[:len(parts)-1]
	s.mu.Unlock()

	for _, rawLine := range parts {
		line := strings.TrimRightFunc(rawLine, isJSSpace)
		s.mu.Lock()
		ready := s.state.Ready
		s.mu.Unlock()
		if !ready {
			if strings.HasPrefix(line, readySentinel+":") {
				s.mu.Lock()
				s.state.Ready = true
				if cwd := line[len(readySentinel)+1:]; cwd != "" {
					s.state.Cwd = cwd
				}
				launch := s.launch
				s.mu.Unlock()
				if launch != nil {
					launch.finish(nil)
				}
				s.onStateChange()
			}
			continue
		}

		if strings.HasPrefix(line, commandStartSentinel+":") {
			fields := strings.Split(line, ":")
			s.mu.Lock()
			if len(fields) > 2 && fields[2] != "" {
				s.state.Cwd = fields[2]
			}
			if len(fields) > 1 {
				s.currentCommandID = fields[1]
			}
			s.mu.Unlock()
			s.onStateChange()
			continue
		}

		if strings.HasPrefix(line, commandDoneSentinel+":") {
			fields := strings.Split(line, ":")
			exitCode, ok := 1, false
			if len(fields) > 2 {
				exitCode, ok = jsParseInt10(fields[2])
			}
			if !ok {
				exitCode = 1
			}
			var id, cwdField string
			if len(fields) > 1 {
				id = fields[1]
			}
			if len(fields) > 3 {
				cwdField = fields[3]
			}
			s.mu.Lock()
			s.state.Running = false
			s.state.LastExitCode = &exitCode
			if cwdField != "" {
				s.state.Cwd = cwdField
			}
			cwd := s.state.Cwd
			s.mu.Unlock()
			if id != "" {
				s.transcript.finishCommand(id, exitCode)
				if exitCode == 0 {
					for _, c := range s.transcript.getSnapshot().Commands {
						if c.ID == id {
							s.onCommandSuccess(c.Command, cwd)
							break
						}
					}
				}
			}
			s.mu.Lock()
			s.currentCommandID = ""
			s.mu.Unlock()
			s.onStateChange()
			continue
		}

		s.mu.Lock()
		id := s.currentCommandID
		s.mu.Unlock()
		if id == "" {
			continue
		}
		s.transcript.appendOutput(id, line)
		s.onStateChange()
	}
}
