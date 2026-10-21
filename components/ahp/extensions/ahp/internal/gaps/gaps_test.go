// Package gaps records the upstream (pi-ahp) test cases that have no runnable Go twin, each as a
// named skipped twin with its reason. Nothing here is silent: the twin gate counts these, and
// proof/PORT.md explains every family.
package gaps

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

func TestGapPiReplay(t *testing.T) {
	twin.Skip(t, "pi-replay", "plain-text", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "single-tool", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "parallel-tools", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-loop", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-error", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "abort", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "steering", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-edit", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-write", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-bash", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-ls", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-grep", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "tool-find", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "compaction", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
	twin.Skip(t, "pi-replay", "bash-long-output", "replays provider streams through a live Pi agent loop, which a Go extension cannot embed; the recorded-stream half is the mapper-fixtures twins and the live half is the layer-6 differential against a scripted provider")
}

func TestGapImageSession(t *testing.T) {
	twin.Skip(t, "image-session", "passes and persists images while honouring preflight cancellation", "needs Pi's InProcessPiBackend with a faux model; the SDK adapter's image path is covered by its own adapter tests")
}

func TestGapLiveTurn(t *testing.T) {
	twin.Skip(t, "live-turn", "live turn # SKIP needs a real model \u2014 set PI_AHP_LIVE_SETTINGS and PI_AHP_LIVE_AUTH", "upstream itself skips this case without a real model (PI_AHP_LIVE_SETTINGS and PI_AHP_LIVE_AUTH)")
}

func TestGapModelDiscovery(t *testing.T) {
	twin.Skip(t, "model-discovery", "advertises extension models and uses pi's configured default", "needs an embedded Pi model registry with extension-provided models; the SDK adapter reads models through the PiG SDK and is covered by adapter tests")
	twin.Skip(t, "model-discovery", "resolves stale and provider-qualified model selections safely", "needs an embedded Pi model registry with extension-provided models; the SDK adapter reads models through the PiG SDK and is covered by adapter tests")
}
