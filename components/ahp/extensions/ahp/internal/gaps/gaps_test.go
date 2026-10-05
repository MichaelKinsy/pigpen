// Package gaps records the upstream (pi-ahp) test cases that have no runnable Go twin, each as a
// named skipped twin with its reason. Nothing here is silent: the twin gate counts these, and
// proof/PORT.md explains every family.
package gaps

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

func TestGapTunnel(t *testing.T) {
	twin.Skip(t, "tunnel", "uses the shared discovery labels and port", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "keeps the identity label out of the display name", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "folds a name the way VS Code does", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "reports when the CLI is not installed", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "requires a logged-in account", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "reports account inspection failures", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "finds this host's first labelled tunnel", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "returns no tunnel for a successful empty listing", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "reports tunnel listing failures and malformed responses", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "creates a tunnel with identity, protocol, launcher, and display labels", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "rejects failed or malformed tunnel creation", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "renames a tunnel without disturbing reserved labels", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "reports a failed rename", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "creates port 31546 when a successful listing is empty", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "keeps an existing HTTP port", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "replaces an old HTTPS port with an HTTP port", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "does not discard port-specific access control while migrating", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "uses current port details when the protocol changes during inspection", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "rejects malformed port inspection output", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
	twin.Skip(t, "tunnel", "reports listing, inspection, deletion, and creation failures", "dev tunnel is not bundled: an owner decision (ACP, AHP and A2A are separate extensions, tunnel management stays outside this port)")
}

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

func TestGapChangeset(t *testing.T) {
	twin.Skip(t, "changeset", "keeps the latest commit separate from uncommitted work and refreshes it", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "limits changes to the session working-directory subtree", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "ignores ambient Git repository overrides", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "omits changesets when Git is unavailable", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "omits changesets outside Git workspaces", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "omits Latest Commit before the repository has a commit", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "resumes changeset updates when durable session deletion fails", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset", "hydrates the parent session when its changeset is subscribed directly", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
}

func TestGapChangesetLifecycle(t *testing.T) {
	twin.Skip(t, "changeset-lifecycle", "observes changes during the initial scan and rebuilds changed ignore rules", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset-lifecycle", "refreshes file changes while a turn is still active", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset-lifecycle", "does not recursively watch when ignored paths cannot be determined", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset-lifecycle", "publishes an explicit error when Git cannot compute the changeset", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset-lifecycle", "drains an in-flight scan before deleting its channel", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
}

func TestGapChangesetURI(t *testing.T) {
	twin.Skip(t, "changeset-uri", "builds stable static catalogue channels", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
	twin.Skip(t, "changeset-uri", "rejects channels the host did not advertise", "git changeset service not ported in this release (documented gap: no changeset channels are advertised, see PORT.md)")
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
