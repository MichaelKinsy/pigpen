package a2aext

import (
	"context"
	"errors"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

var errNotImplemented = errors.New("a2a: not implemented")

// storeOptions configures the task store.
type storeOptions struct {
	// Dir holds one file per task. Empty keeps tasks in memory only.
	Dir string
	// Retention is how long a finished task is kept. Zero keeps them for ever.
	Retention time.Duration
	Now       func() time.Time
	Auth      taskstore.Authenticator
	Logf      func(format string, args ...any)
}

// fileStore is the task store: tasks survive a restart of the listener.
type fileStore struct{}

func openTaskStore(o storeOptions) (*fileStore, error) { return nil, errNotImplemented }

// writes counts the task files written since the store was opened.
func (s *fileStore) writes() int { return 0 }

// interrupted counts the tasks that were still running when the previous listener stopped.
func (s *fileStore) interrupted() int { return 0 }

func (s *fileStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	return 0, errNotImplemented
}

func (s *fileStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	return 0, errNotImplemented
}

func (s *fileStore) Get(ctx context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
	return nil, errNotImplemented
}

func (s *fileStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return nil, errNotImplemented
}

var _ taskstore.Store = (*fileStore)(nil)
