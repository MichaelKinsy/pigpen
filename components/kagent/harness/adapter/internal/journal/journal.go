// Package journal persists the one pending input request a parked PiG turn
// leaves behind. The record lives in the Actor's durable directory and is the
// only state besides PiG's own session file. It holds no credentials and no
// process identity.
package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/fsutil"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// FileName is the record's name inside the adapter directory.
const FileName = "pig-pending.json"

// Version is the record schema version.
const Version = 1

// MaxBytes bounds the record a restore will read. Calls carry model-written
// arguments, so the bound matches PiG's 16 MiB frame limit times four.
const MaxBytes = 64 << 20

// ErrCorrupt marks a record file that cannot be decoded or validated (as opposed to one that could not be read).
var ErrCorrupt = errors.New("corrupt PiG pending record")

// Phase is where Resume got to. A crash in executing or continuing leaves an
// outcome that cannot be known, which is never replayed automatically.
type Phase string

const (
	Waiting    Phase = "waiting"
	Executing  Phase = "executing"
	Result     Phase = "result"
	Continuing Phase = "continuing"
	Complete   Phase = "complete"
	Canceled   Phase = "canceled"
	Abandoned  Phase = "abandoned"
)

// Kind says what the parked turn asked for.
type Kind string

const (
	KindApproval Kind = "approval"
	KindAskUser  Kind = "ask_user"
)

// Call is one tool call of the interrupted assistant message.
type Call struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Block is one piece of tool-result content as PiG stores it.
type Block struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// Outcome is the tool result the adapter injects into the session.
type Outcome struct {
	Content []Block `json:"content"`
	IsError bool    `json:"isError"`
}

// Record is the durable pending request.
type Record struct {
	Version   int                 `json:"version"`
	TaskID    string              `json:"task_id"`
	ContextID string              `json:"context_id"`
	Origin    runtime.TraceOrigin `json:"origin,omitzero"`
	SessionID string              `json:"session_id"`
	Phase     Phase               `json:"phase"`
	Kind      Kind                `json:"kind"`
	// AssistantEntryID is the session entry the turn parked at. The session
	// file must still end there before anything is injected.
	AssistantEntryID string `json:"assistant_entry_id"`
	Calls            []Call `json:"calls"`
	// CallID is the call the request is about; Server and Tool name the MCP
	// target of an approval (Tool as PiG sanitised it).
	CallID string `json:"call_id"`
	Server string `json:"server,omitempty"`
	Tool   string `json:"tool,omitempty"`

	Approval *runtime.ApprovalRequest  `json:"approval,omitempty"`
	Question *runtime.AskUserRequest   `json:"question,omitempty"`
	Decision *runtime.ApprovalDecision `json:"decision,omitempty"`
	Answer   *runtime.AskUserResponse  `json:"answer,omitempty"`
	Result   *Outcome                  `json:"result,omitempty"`
}

// Request returns the public request this record stands for.
func (r *Record) Request() runtime.InputRequest {
	if r.Approval != nil {
		return r.Approval
	}
	return r.Question
}

// Validate rejects records the adapter could not have written.
func (r *Record) Validate() error {
	if r.Version != Version || r.TaskID == "" || r.ContextID == "" || r.SessionID == "" || r.AssistantEntryID == "" || r.CallID == "" || len(r.Calls) == 0 {
		return errors.New("invalid PiG pending record identity")
	}
	known := false
	for _, call := range r.Calls {
		if call.ID == "" || call.Name == "" {
			return errors.New("invalid PiG pending record call")
		}
		known = known || call.ID == r.CallID
	}
	if !known {
		return errors.New("PiG pending record names a call the assistant message does not contain")
	}
	switch r.Kind {
	case KindApproval:
		if r.Approval == nil || r.Question != nil || r.Answer != nil || r.Server == "" || r.Tool == "" ||
			r.Approval.ID == "" || r.Approval.CallID != r.CallID || r.Approval.Name == "" {
			return errors.New("invalid PiG pending approval")
		}
	case KindAskUser:
		if r.Question == nil || r.Approval != nil || r.Decision != nil || r.Server != "" || r.Tool != "" ||
			r.Question.ID == "" || len(r.Question.Questions) == 0 {
			return errors.New("invalid PiG pending question")
		}
	default:
		return fmt.Errorf("unknown PiG pending kind %q", r.Kind)
	}
	switch r.Phase {
	case Waiting:
		if r.Decision != nil || r.Answer != nil || r.Result != nil {
			return errors.New("waiting PiG continuation already has a response")
		}
	case Executing:
		if r.Kind != KindApproval || r.Decision == nil || !r.Decision.Approved || r.Result != nil {
			return errors.New("invalid PiG executing continuation")
		}
	case Result, Continuing, Complete:
		if r.Result == nil || r.Kind == KindApproval && r.Decision == nil || r.Kind == KindAskUser && r.Answer == nil {
			return errors.New("resolved PiG continuation has no response or result")
		}
	case Canceled, Abandoned:
	default:
		return fmt.Errorf("unknown PiG pending phase %q", r.Phase)
	}
	return nil
}

// Store reads and writes the record.
type Store struct{ path string }

// Open returns the store for adapterDir (<DurableDir>/adapter).
func Open(adapterDir string) *Store { return &Store{path: filepath.Join(adapterDir, FileName)} }

// Path is the record's location.
func (s *Store) Path() string { return s.path }

// Load reads the record, or returns nil when there is none. It reads the file
// on every call: Substrate can swap /data under a constructed process.
func (s *Store) Load() (*Record, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect PiG pending record: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, fmt.Errorf("%w: not a regular file or too large", ErrCorrupt)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read PiG pending record: %w", err)
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrCorrupt, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrCorrupt)
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return &record, nil
}

// Quarantine moves a corrupt record aside (FileName + ".corrupt") so the Actor can take new work.
func (s *Store) Quarantine() error {
	if err := os.Rename(s.path, s.path+".corrupt"); err != nil {
		return fmt.Errorf("quarantine the PiG pending record: %w", err)
	}
	return nil
}

// Save validates and atomically replaces the record (temp file, fsync, rename,
// directory fsync, mode 0600).
func (s *Store) Save(record *Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode PiG pending record: %w", err)
	}
	if len(data) > MaxBytes {
		return errors.New("PiG pending record exceeds the size limit")
	}
	if err := fsutil.ReplacePrivateFile(s.path, data); err != nil {
		return fmt.Errorf("persist PiG pending record: %w", err)
	}
	return nil
}
