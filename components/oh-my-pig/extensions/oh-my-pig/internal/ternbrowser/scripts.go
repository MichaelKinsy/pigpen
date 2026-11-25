package ternbrowser

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Embedded upstream JavaScript page scripts from omp v19.1.1 (31ffee513f).
// Generated directly from page-kit.ts (TERN_KIT_SOURCE, TERN_FRAME_ANNOUNCE_SOURCE)
// and page-capture.ts (TERN_CAPTURE_INSTALLER) and injected via Tern's "scripts"
// and "eval" ops.
//
// upstream: packages/coding-agent/src/tools/browser/tern/page-kit.ts:1171-1195
// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:51-418

//go:embed scripts/tern-kit.js
var TernKitSource string

//go:embed scripts/frame-announce.js
var TernFrameAnnounceSource string

//go:embed scripts/page-capture.js
var TernCaptureInstaller string

const (
	// TernCaptureGlobal is the page-world global holding capture state.
	// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:51
	TernCaptureGlobal = "__ompTernCapture"

	// kitCallSource invokes kit[method](...args) in the isolated world.
	// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:299-300
	kitCallSource = `async function (method, args) { const kit = globalThis.__ompTernKit; if (!kit) return { missing: true }; return { value: await kit[method](...args) }; }`
)

// ternCaptureUpdater updates an already-installed capture without installing a new one.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:471
var ternCaptureUpdater = fmt.Sprintf(
	`function (config) { if (!globalThis[%q]) return false; return (%s)(config); }`,
	TernCaptureGlobal,
	TernCaptureInstaller,
)

// PageEmulation holds JS-visible emulation overrides applied in the page world.
// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:33
type PageEmulation struct {
	Geolocation *GeolocationCoords `json:"geolocation,omitempty"`
	Locale      string             `json:"locale,omitempty"`
	Offline     *bool              `json:"offline,omitempty"`
}

type GeolocationCoords struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
}

// CaptureConfig is the argument to TernCaptureInstaller.
// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:43
type CaptureConfig struct {
	Routes    []RouteRule   `json:"routes"`
	Emulation PageEmulation `json:"emulation"`
}

// TernCaptureScript returns the document-start script installing the capture with config.
// upstream: packages/coding-agent/src/tools/browser/tern/page-capture.ts:416
func TernCaptureScript(cfg CaptureConfig) string {
	if cfg.Routes == nil {
		cfg.Routes = []RouteRule{}
	}
	b, _ := json.Marshal(cfg)
	return fmt.Sprintf("(%s)(%s);", TernCaptureInstaller, string(b))
}
