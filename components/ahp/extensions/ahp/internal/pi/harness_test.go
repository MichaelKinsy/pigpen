package pi_test

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
)

// harness is upstream test/harness.ts: a live host with the session services over a fixture-owned
// session root. Clients attach in memory (the transport itself is covered by internal/ws).
type harness struct {
	t        *testing.T
	host     *host.Host
	services *pi.Services
	root     string

	mu           sync.Mutex
	deletedFiles []string
}

type harnessOptions struct {
	createBackend pi.BackendFactory
	deleteFile    func(path string) (pi.SessionFileDeletionResult, error)
	sessionRoot   string
	replayBuffer  int
	workingDir    string
}

func startHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	h := &harness{t: t}
	h.root = o.sessionRoot
	if h.root == "" {
		h.root = t.TempDir()
	}
	h.host = testkit.NewHost(host.Options{ReplayBufferCapacity: o.replayBuffer})
	deleteFile := o.deleteFile
	if deleteFile == nil {
		deleteFile = func(path string) (pi.SessionFileDeletionResult, error) {
			h.mu.Lock()
			h.deletedFiles = append(h.deletedFiles, path)
			h.mu.Unlock()
			return pi.SessionFileDeletionResult{OK: true}, nil
		}
	}
	workingDir := o.workingDir
	if workingDir == "" {
		workingDir = t.TempDir()
	}
	h.services = pi.NewServices(pi.ServicesOptions{
		Host: h.host, SessionRoot: h.root, DefaultWorkingDirectory: workingDir,
		CreateBackend: o.createBackend, CreateSessionManager: pi.PersistentStorage(filepath.Join(h.root, "created")),
		DeleteFile: deleteFile,
	})
	h.host.Serve(h.services.Capabilities())
	return h
}

func (h *harness) deleted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.deletedFiles...)
}

var clientCounter atomic.Int64

func nextClientID() string { return "test-client-" + itoa(int(clientCounter.Add(1))) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}
