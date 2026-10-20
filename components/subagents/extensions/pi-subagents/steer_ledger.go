package pi_subagents

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Port of src/runs/background/steering.ts: the steering ledger a run carries in status.json (`steering`, and one per step), the
// receipt text of a steer, the one-claim recovery lock and the budgets a recovery keeps. The ledger lives in the status file's
// own layout, so a run written by the original is read as it is.

const (
	maxSteeringRequests          = 20
	steeringMessagePreviewLimit  = 160
	steeringRecoveryClaimVersion = 1
)

// steerDeliveryMode is the original's SteerDeliveryMode.
type steerDeliveryMode string

const (
	steerModeSteer    steerDeliveryMode = "steer"
	steerModeFollowUp steerDeliveryMode = "follow_up"
	steerModeAuto     steerDeliveryMode = "auto"
)

// steeringTarget is SteeringTargetStatus.
type steeringTarget struct {
	Index            int    `json:"index"`
	State            string `json:"state"`
	RoutedAt         *int64 `json:"routedAt,omitempty"`
	DeliveredAt      *int64 `json:"deliveredAt,omitempty"`
	LateDeliveredAt  *int64 `json:"lateDeliveredAt,omitempty"`
	FailedAt         *int64 `json:"failedAt,omitempty"`
	RecoveredAt      *int64 `json:"recoveredAt,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ReplacementRunID string `json:"replacementRunId,omitempty"`
}

// steeringRequest is SteeringRequestStatus.
type steeringRequest struct {
	ID             string           `json:"id"`
	RequestedAt    int64            `json:"requestedAt"`
	Source         string           `json:"source,omitempty"`
	MessagePreview string           `json:"messagePreview"`
	Targets        []steeringTarget `json:"targets"`
}

// steeringStatus is SteeringStatus.
type steeringStatus struct {
	Requested       int               `json:"requested"`
	Scheduled       int               `json:"scheduled"`
	Pending         int               `json:"pending"`
	Delivered       int               `json:"delivered"`
	Failed          int               `json:"failed"`
	Recovered       int               `json:"recovered"`
	LastRequestedAt *int64            `json:"lastRequestedAt,omitempty"`
	LastDeliveredAt *int64            `json:"lastDeliveredAt,omitempty"`
	Recent          []steeringRequest `json:"recent"`
}

// steerActionTarget is SteerActionTarget.
type steerActionTarget struct {
	Index            int    `json:"index"`
	State            string `json:"state"`
	DeliveredAt      *int64 `json:"deliveredAt,omitempty"`
	LateDeliveredAt  *int64 `json:"lateDeliveredAt,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ReplacementRunID string `json:"replacementRunId,omitempty"`
}

// steerActionResult is SteerActionResult (details.steering of a steer reply).
type steerActionResult struct {
	RequestID        string              `json:"requestId"`
	State            string              `json:"state"`
	DeliveryStatus   string              `json:"deliveryStatus"`
	SourceRunID      string              `json:"sourceRunId"`
	ReplacementRunID string              `json:"replacementRunId,omitempty"`
	Targets          []steerActionTarget `json:"targets"`
}

func createSteeringStatus() *steeringStatus {
	return &steeringStatus{Recent: []steeringRequest{}}
}

// steeringMessagePreview redacts secrets, collapses whitespace and bounds the message to 160 characters.
func steeringMessagePreview(message string) string {
	return previewDisplayText(redactSecretValues(message), steeringMessagePreviewLimit)
}

var backtickRun = regexp.MustCompile("`{3,}")

// steeringReceipt is the receipt text: the sent message in a fence longer than any fence inside it.
func steeringReceipt(message, receipt string) string {
	preview := steeringMessagePreview(message)
	longest := 2
	for _, m := range backtickRun.FindAllString(preview, -1) {
		if len(m) > longest {
			longest = len(m)
		}
	}
	fence := strings.Repeat("`", longest+1)
	return receipt + "\n\nMessage sent:\n" + fence + "text\n" + preview + "\n" + fence
}

// unconsumedSteerReason is the settlement reason of an accepted request that never got a matching user message_end.
func unconsumedSteerReason(mode steerDeliveryMode) string {
	if mode == steerModeFollowUp {
		return "child completed before consuming follow-up"
	}
	return "child completed before consuming steering"
}

// takeMatchingAcceptedSteer removes and returns the oldest accepted steer whose text is messageText (FIFO on equal text).
func takeMatchingAcceptedSteer[T any](accepted []T, text func(T) string, messageText string) (match T, rest []T, ok bool) {
	for i, entry := range accepted {
		if text(entry) == messageText {
			rest = append(append([]T{}, accepted[:i]...), accepted[i+1:]...)
			return entry, rest, true
		}
	}
	return match, accepted, false
}

type steerRequestInput struct {
	ID          string
	RequestedAt int64
	Source      string
	Message     string
	Targets     []steeringTarget
}

func incrementSteeringState(s *steeringStatus, state string) {
	switch state {
	case "scheduled":
		s.Scheduled++
	case "routed", "queued":
		s.Pending++
	case "delivered", "late":
		s.Delivered++
	case "failed":
		s.Failed++
	case "recovered":
		s.Recovered++
	}
}

// recordSteeringRequest adds a request to the ledger (a known id returns the existing one), keeping the 20 most recent.
func recordSteeringRequest(s *steeringStatus, in steerRequestInput) *steeringRequest {
	for i := range s.Recent {
		if s.Recent[i].ID == in.ID {
			return &s.Recent[i]
		}
	}
	req := steeringRequest{ID: in.ID, RequestedAt: in.RequestedAt, Source: in.Source, MessagePreview: steeringMessagePreview(in.Message), Targets: []steeringTarget{}}
	for _, t := range in.Targets {
		req.Targets = append(req.Targets, steeringTarget{Index: t.Index, State: t.State, Reason: t.Reason})
	}
	s.Requested++
	s.LastRequestedAt = new(in.RequestedAt)
	s.Recent = append(s.Recent, req)
	if len(s.Recent) > maxSteeringRequests {
		s.Recent = append([]steeringRequest{}, s.Recent[len(s.Recent)-maxSteeringRequests:]...)
	}
	for _, t := range req.Targets {
		incrementSteeringState(s, t.State)
	}
	return &s.Recent[len(s.Recent)-1]
}

type steeringTargetFields struct{ Reason, ReplacementRunID string }

// updateSteeringTarget moves one target to a state (the original's updateSteeringTarget, counts included).
func updateSteeringTarget(s *steeringStatus, requestID string, index int, state string, now int64, fields steeringTargetFields) *steeringTarget {
	var target *steeringTarget
	for ri := range s.Recent {
		if s.Recent[ri].ID != requestID {
			continue
		}
		for ti := range s.Recent[ri].Targets {
			if s.Recent[ri].Targets[ti].Index == index {
				target = &s.Recent[ri].Targets[ti]
			}
		}
		break
	}
	if target == nil {
		return nil
	}
	if state == "late" && target.State == "recovered" {
		if target.LateDeliveredAt == nil {
			target.LateDeliveredAt = new(now)
			s.Delivered++
			s.LastDeliveredAt = new(now)
		}
		if fields.Reason != "" {
			target.Reason = fields.Reason
		}
		return target
	}
	if target.State == state {
		if state == "routed" && target.RoutedAt == nil {
			target.RoutedAt = new(now)
		}
		if state == "delivered" && target.DeliveredAt == nil {
			target.DeliveredAt = new(now)
		}
		if state == "late" && target.LateDeliveredAt == nil {
			target.LateDeliveredAt = new(now)
		}
		if fields.Reason != "" {
			target.Reason = fields.Reason
		}
		if fields.ReplacementRunID != "" {
			target.ReplacementRunID = fields.ReplacementRunID
		}
		return target
	}
	wasPending := target.State == "routed" || target.State == "queued"
	nowPending := state == "routed" || state == "queued"
	if wasPending && !nowPending && s.Pending > 0 {
		s.Pending--
	}
	target.State = state
	switch state {
	case "routed":
		target.RoutedAt = new(now)
	case "delivered":
		target.DeliveredAt = new(now)
		s.LastDeliveredAt = new(now)
	case "late":
		target.LateDeliveredAt = new(now)
		s.LastDeliveredAt = new(now)
	case "failed":
		target.FailedAt = new(now)
	case "recovered":
		target.RecoveredAt = new(now)
	}
	if fields.Reason != "" {
		target.Reason = fields.Reason
	}
	if fields.ReplacementRunID != "" {
		target.ReplacementRunID = fields.ReplacementRunID
	}
	if !wasPending || !nowPending {
		incrementSteeringState(s, state)
	}
	return target
}

func findSteeringRequest(s *steeringStatus, requestID string) *steeringRequest {
	for i := range s.Recent {
		if s.Recent[i].ID == requestID {
			return &s.Recent[i]
		}
	}
	return nil
}

func allTargets(ts []steerActionTarget, pred func(string) bool) bool {
	for _, t := range ts {
		if !pred(t.State) {
			return false
		}
	}
	return true
}

func anyTarget(ts []steerActionTarget, pred func(string) bool) bool {
	for _, t := range ts {
		if pred(t.State) {
			return true
		}
	}
	return false
}

// actionResultFromSteeringStatus folds a request's targets into one action result; nil when the request is unknown.
func actionResultFromSteeringStatus(s *steeringStatus, sourceRunID, requestID, replacementRunID string) *steerActionResult {
	req := findSteeringRequest(s, requestID)
	if req == nil {
		return nil
	}
	targets := make([]steerActionTarget, 0, len(req.Targets))
	for _, t := range req.Targets {
		targets = append(targets, steerActionTarget{Index: t.Index, State: t.State, DeliveredAt: t.DeliveredAt, LateDeliveredAt: t.LateDeliveredAt, Reason: t.Reason, ReplacementRunID: t.ReplacementRunID})
	}
	is := func(want string) func(string) bool { return func(s string) bool { return s == want } }
	failedOrLate := func(s string) bool { return s == "failed" || s == "late" }
	state := "pending"
	switch {
	case len(targets) > 0 && allTargets(targets, is("delivered")):
		state = "delivered"
	case len(targets) > 0 && allTargets(targets, is("scheduled")):
		state = "scheduled"
	case len(targets) > 0 && allTargets(targets, is("recovered")):
		state = "recovered"
	case len(targets) > 0 && allTargets(targets, failedOrLate):
		state = "failed"
	case anyTarget(targets, failedOrLate) && anyTarget(targets, func(s string) bool { return !failedOrLate(s) }):
		state = "partial"
	}
	replacement := replacementRunID
	if replacement == "" {
		for _, t := range req.Targets {
			if t.ReplacementRunID != "" {
				replacement = t.ReplacementRunID
				break
			}
		}
	}
	delivery := "queued"
	if len(targets) > 0 && allTargets(targets, func(s string) bool { return s == "delivered" || s == "recovered" }) {
		delivery = "delivered"
	}
	return &steerActionResult{RequestID: requestID, State: state, DeliveryStatus: delivery, SourceRunID: sourceRunID, ReplacementRunID: replacement, Targets: targets}
}

// steeringActionIsTerminal says whether a result needs no further waiting.
func steeringActionIsTerminal(r *steerActionResult) bool {
	if r == nil {
		return false
	}
	if len(r.Targets) > 0 && allTargets(r.Targets, func(s string) bool { return s == "queued" || s == "delivered" }) {
		return true
	}
	switch r.State {
	case "delivered", "scheduled", "partial", "recovered", "failed":
		return true
	}
	return false
}

// terminalSteeringNoticeState is "failed" or "partial" once every target of a request settled with a failure; "" otherwise.
func terminalSteeringNoticeState(s *steeringStatus, requestID string) string {
	req := findSteeringRequest(s, requestID)
	if req == nil {
		return ""
	}
	hasSuccess, hasFailure := false, false
	for _, t := range req.Targets {
		switch t.State {
		case "routed", "queued", "scheduled":
			return ""
		case "delivered", "recovered":
			hasSuccess = true
		case "failed", "late":
			hasFailure = true
		}
	}
	if hasSuccess && hasFailure {
		return "partial"
	}
	if hasFailure {
		return "failed"
	}
	return ""
}

// steeringClaim is what claimSteeringRecovery persists (claim.json and the per-request marker).
type steeringClaim struct {
	Version     int    `json:"version"`
	RequestID   string `json:"requestId"`
	SourceRunID string `json:"sourceRunId"`
	CommittedAt int64  `json:"committedAt"`
}

func base64URL(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func steerRecoveryDir(asyncDir string) string {
	return filepath.Join(controlInboxDir(asyncDir), "steer-recovery")
}

var errSteerRecoveryClaimed = errors.New("Another steering recovery is already committed for this source run.")

// claimSteeringRecovery takes the run's one recovery claim (O_EXCL claim.json, 0600) and writes the request's marker.
func claimSteeringRecovery(asyncDir string, c steeringClaim) (claimPath, markerPath string, err error) {
	dir := steerRecoveryDir(asyncDir)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	claimPath = filepath.Join(dir, "claim.json")
	c.Version = steeringRecoveryClaimVersion
	body, _ := json.MarshalIndent(c, "", "  ")
	f, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", "", errSteerRecoveryClaimed
		}
		return "", "", err
	}
	_, werr := f.Write(body)
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(claimPath)
		return "", "", werr
	}
	markerPath = filepath.Join(dir, base64URL(c.RequestID)+".json")
	if err = asyncWritePrivateJSON(markerPath, c); err != nil {
		_ = os.Remove(claimPath)
		return "", "", err
	}
	return claimPath, markerPath, nil
}

// readSteeringStatusOf reads the `steering` ledger of a run's status.json; nil when there is none.
func readSteeringStatus(asyncDir string) *steeringStatus {
	st, err := readStatus(asyncDir)
	if err != nil || st == nil {
		return nil
	}
	return statusSteering(st)
}

// statusSteering decodes the ledger a status carries (it is not a modelled field: it rides in the status's extra keys).
func statusSteering(st *AsyncStatus) *steeringStatus {
	raw, ok := st.Extra["steering"]
	if !ok {
		return nil
	}
	var s steeringStatus
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	if s.Recent == nil {
		s.Recent = []steeringRequest{}
	}
	return &s
}

func setStatusSteering(st *AsyncStatus, s *steeringStatus) {
	b, _ := json.Marshal(s)
	if st.Extra == nil {
		st.Extra = map[string]json.RawMessage{}
	}
	st.Extra["steering"] = b
}

func stepSteering(step *AsyncStep) *steeringStatus {
	raw, ok := step.Extra["steering"]
	if !ok {
		return nil
	}
	var s steeringStatus
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	if s.Recent == nil {
		s.Recent = []steeringRequest{}
	}
	return &s
}

func setStepSteering(step *AsyncStep, s *steeringStatus) {
	b, _ := json.Marshal(s)
	if step.Extra == nil {
		step.Extra = map[string]json.RawMessage{}
	}
	step.Extra["steering"] = b
}

// steeringRecoveryLimits is what a recovered run may still spend.
type steeringRecoveryLimits struct {
	TimeoutMs          *int64
	AbsoluteDeadlineAt *int64
	ToolBudget         *resolvedToolBudget
}

// remainingSteeringRecoveryLimits keeps only the deadline and the tool budget that remain; an exhausted one is an error.
// toolCount is the consumed count of the paused status (its toolBudget.toolCount, else its toolCount).
func remainingSteeringRecoveryLimits(absoluteDeadlineAt *int64, initial *resolvedToolBudget, toolCount int, now int64) (steeringRecoveryLimits, error) {
	var limits steeringRecoveryLimits
	if absoluteDeadlineAt != nil {
		timeout := *absoluteDeadlineAt - now
		if timeout <= 0 {
			return limits, errors.New("Source run has no remaining deadline budget; it remains paused.")
		}
		limits.TimeoutMs = &timeout
		limits.AbsoluteDeadlineAt = absoluteDeadlineAt
	}
	if initial != nil {
		hard := initial.Hard - toolCount
		if hard <= 0 {
			return limits, errors.New("Source run has no remaining tool budget; it remains paused.")
		}
		b := resolvedToolBudget{Hard: hard, AllBlocked: initial.AllBlocked, Block: initial.Block}
		if initial.HasSoft {
			if soft := initial.Soft - toolCount; soft > 0 && soft < hard {
				b.Soft, b.HasSoft = soft, true
			}
		}
		limits.ToolBudget = &b
	}
	return limits, nil
}

func (r steerActionResult) String() string { return fmt.Sprintf("%s:%s", r.RequestID, r.State) }
