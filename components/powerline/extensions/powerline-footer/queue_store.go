package powerline_footer

import "errors"

// RED STUB: the types of queue/types.ts with unimplemented store methods.

type queueIntent string
type queueStatus string

const (
	intentSteer       queueIntent = "steer"
	intentFollowUp    queueIntent = "follow-up"
	intentPostCompact queueIntent = "post-compact"

	statusQueued     queueStatus = "queued"
	statusBlocked    queueStatus = "blocked"
	statusDelivering queueStatus = "delivering"
	statusSent       queueStatus = "sent"
	statusFailed     queueStatus = "failed"

	targetCurrentSession = "current-session"
	targetProject        = "project"
	targetGlobal         = "global"
)

type queueTarget struct{ Kind, Cwd, Alias string }
type queueSource struct{ Cwd, SessionID string }
type queueContext = queueSource

type queueItem struct {
	ID, Text             string
	CreatedAt, UpdatedAt float64
	Source               queueSource
	Target               queueTarget
	Intent               queueIntent
	Status               queueStatus
	Error                string
}

type createQueueItemInput struct {
	Text   string
	Source queueSource
	Target queueTarget
	Intent queueIntent
	Status queueStatus
	Now    *float64
}

type queueUpdates struct {
	Status   queueStatus
	SetError bool
	Error    string
	Target   *queueTarget
	Text     *string
}

type queueStore struct{}

var errNotImplemented = errors.New("not implemented")

func newQueueStore(inbox, aliases string) *queueStore { return &queueStore{} }

func (s *queueStore) list() ([]queueItem, error) { return nil, errNotImplemented }
func (s *queueStore) add(in createQueueItemInput) (queueItem, error) {
	return queueItem{}, errNotImplemented
}
func (s *queueStore) get(prefix string) (*queueItem, error) { return nil, errNotImplemented }
func (s *queueStore) clear(id string) (*queueItem, error)   { return nil, errNotImplemented }
func (s *queueStore) update(id string, u queueUpdates) (*queueItem, error) {
	return nil, errNotImplemented
}
func (s *queueStore) activeItems(c queueContext) ([]queueItem, error) { return nil, errNotImplemented }
func (s *queueStore) summarize(c queueContext, compacting bool) (queueSummary, error) {
	return queueSummary{}, errNotImplemented
}

func currentQueueContext(cwd, sessionID string) queueContext { return queueContext{} }
func formatQueueDeliveryText(item queueItem) string          { return "" }
func parseCompactQueuedPrompt(text string) (string, bool)    { return "", false }
