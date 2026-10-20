// Package tunnel drives Microsoft's `devtunnel` CLI so that the AHP listener can be reached
// through a dev tunnel that compatible clients discover (port of pi-ahp's src/tunnel/discovery.ts
// and devtunnel.ts). The CLI is a separate .NET program that only tunnel users need: it is not
// bundled, and it must be installed and logged in before these operations can run.
package tunnel

import (
	"regexp"
	"strings"
)

// LauncherLabel is required for clients using the VS Code-compatible discovery convention.
const LauncherLabel = "vscode-server-launcher"

// ProtocolLabel selects direct forwarding. Protocol 6 expects an `/agent-host/select` gateway,
// which a plain `devtunnel` port forward cannot provide. VS Code appends a tunnel-id-derived
// `tkn` on the v5 path; the host ignores it and relies on the dev tunnel access boundary. This
// versions the tunnel arrangement, not AHP itself.
const ProtocolLabel = "protocolv5"

// Port is the fixed forwarded port used by the discovery convention.
const Port = 31546

// IdentityLabel finds this host's tunnel on later runs without becoming its display name.
// Compatible clients exclude underscore-prefixed labels from that name.
const IdentityLabel = "_pi_ahp"

// DisplayLabel is the client-facing tunnel name, when one exists.
func DisplayLabel(labels []string) (string, bool) {
	for _, label := range labels {
		if !strings.HasPrefix(label, "_") && label != LauncherLabel && !strings.HasPrefix(label, "protocolv") {
			return label, true
		}
	}
	return "", false
}

var (
	leadingDashes = regexp.MustCompile(`^-+`)
	notLabelChar  = regexp.MustCompile(`[^A-Za-z0-9_-]`)
)

// NameLabel applies the discovery convention's normalization to a user-supplied name; false when
// nothing is left.
func NameLabel(name string) (string, bool) {
	slug := leadingDashes.ReplaceAllString(name, "")
	slug = notLabelChar.ReplaceAllString(slug, "")
	if len(slug) > 20 {
		slug = slug[:20]
	}
	return slug, slug != ""
}
