package audit

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// SessionFileName is the audit file in the session directory.
const SessionFileName = "pigpen-audit.jsonl"

// Open returns the Logger for an audit configuration: stderr for SinkStderr, the file SessionFileName in sessionDir
// for SinkSessionFile. It does no work and creates no file: the file is opened on the first event. A nil
// configuration returns a nil Logger (audit is off). A session-file sink needs an absolute session directory.
func Open(pkg string, cfg *profile.Audit, sessionDir string, stderr io.Writer) (*Logger, error) {
	if cfg == nil {
		return nil, nil
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	l := &Logger{stderr: stderr, required: cfg.Required, now: time.Now, pkg: pkg}
	switch cfg.Sink {
	case profile.SinkStderr, "":
		l.w = stderr
	case profile.SinkSessionFile:
		if !filepath.IsAbs(sessionDir) {
			return nil, profile.NewError(profile.ProfileMisconfigured, profile.FlagAudit, pkg)
		}
		f := &fileSink{path: filepath.Join(sessionDir, SessionFileName)}
		l.w, l.closer = f, f
	default:
		return nil, profile.NewError(profile.ProfileMisconfigured, profile.FlagAudit, pkg)
	}
	return l, nil
}

// fileSink appends to one file, opened on first use. A failure to open is not remembered: the next event tries again.
//
// The session directory is writable by the agent's own tools, so a symbolic link planted at the audit path would
// turn the sink into an append primitive on any file the process can write (auth.json, settings, a file a policy
// protects). The last path element is therefore opened with O_NOFOLLOW, and the descriptor must be a regular file.
type fileSink struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func (s *fileSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NONBLOCK|noFollow, 0o600)
		if err != nil {
			return 0, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			return 0, os.ErrInvalid
		}
		s.f = f
	}
	return s.f.Write(p)
}

func (s *fileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}
