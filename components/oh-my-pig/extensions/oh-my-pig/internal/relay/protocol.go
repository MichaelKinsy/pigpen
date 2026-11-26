package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:1-344

const (
	// CancelledReleasePoint is where omp releases a cancelled mouse press on both axes:
	// off the page, at a point no real pointer reports, so page code and the relay can
	// tell it apart. The relay draws no overlay input there.
	// upstream: protocol.ts:62
	CancelledReleasePoint = -7919

	// OverlayProtocolVersion is the hello capability of an extension that draws the
	// page overlay and answers `overlay` RPCs.
	// upstream: protocol.ts:116
	OverlayProtocolVersion = 1

	// OverlayHostTag is the tag name of the page overlay's host element.
	// upstream: protocol.ts:122
	OverlayHostTag = "omp-overlay"

	// PanelProtocolVersion is the hello capability of an extension whose side panel
	// sends PanelCall messages.
	// upstream: protocol.ts:125
	PanelProtocolVersion = 1

	// DiscardedTabsProtocolVersion is the required capability for safe relay target
	// discovery and auto-attach.
	// upstream: protocol.ts:152
	DiscardedTabsProtocolVersion = 1

	// ExtensionReloadProtocolVersion is the hello capability of an extension that
	// reloads itself on a relay `reload` message.
	// upstream: protocol.ts:155
	ExtensionReloadProtocolVersion = 1

	// DownloadsProtocolVersion is the hello capability of an extension that answers
	// `findDownload`.
	// upstream: protocol.ts:184
	DownloadsProtocolVersion = 1

	// RelayRecheckPath is the POST path on the relay after install folders change.
	// upstream: protocol.ts:216
	RelayRecheckPath = "/omp/extensions/recheck"

	// RelayRestartPath is the POST path on the relay asking the native host serving
	// it to exit so Chrome restarts it.
	// upstream: protocol.ts:222
	RelayRestartPath = "/omp/relay/restart"
)

// ForeignFrameRefusal matches Chrome's refusal of a debugger request while another
// extension's frame is in the tab.
// upstream: protocol.ts:158
var ForeignFrameRefusal = regexp.MustCompile(`(?i)chrome-extension:// URL of different extension`)

// TabSnapshot is the minimal view of a Chrome tab shared between extension and relay.
// upstream: protocol.ts:10-22
type TabSnapshot struct {
	TabID     int    `json:"tabId"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Active    bool   `json:"active"`
	Discarded bool   `json:"discarded"`
	WindowID  int    `json:"windowId"`
	Pinned    bool   `json:"pinned"`
	GroupID   int    `json:"groupId"`
}

// OverlayAction is what a relay `overlay` RPC asks of a tab's page overlay.
// upstream: protocol.ts:65
type OverlayAction string

const (
	OverlayMount   OverlayAction = "mount"
	OverlayUnmount OverlayAction = "unmount"
	OverlayHide    OverlayAction = "hide"
	OverlayShow    OverlayAction = "show"
)

// OverlayInput is input a claiming CDP client dispatched to a tab's root session
// and Chrome acknowledged, for the overlay to draw.
// upstream: protocol.ts:71-80
type OverlayInput struct {
	Kind       string   `json:"kind"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Button     string   `json:"button,omitempty"`
	ClickCount *int     `json:"clickCount,omitempty"`
}

// PanelCall is a side-panel request the extension forwards to the relay.
// upstream: protocol.ts:83-89
type PanelCall struct {
	Op         string `json:"op"`
	InstanceID string `json:"instanceId,omitempty"`
}

// PanelInstall is one registered extension install folder as the relay sees it.
// upstream: protocol.ts:92-98
type PanelInstall struct {
	Dir     string  `json:"dir"`
	Version *string `json:"version"`
	Missing bool    `json:"missing"`
}

// PanelCollabHost is one local omp session a panel can attach to.
// upstream: protocol.ts:101-113
type PanelCollabHost struct {
	InstanceID    string            `json:"instanceId"`
	Generation    int               `json:"generation"`
	PID           int               `json:"pid"`
	SessionID     string            `json:"sessionId"`
	SessionName   *string           `json:"sessionName"`
	Cwd           string            `json:"cwd"`
	Model         *PanelCollabModel `json:"model"`
	StartedAt     int64             `json:"startedAt"`
	Busy          *bool             `json:"busy"`
	InputRequired bool              `json:"inputRequired"`
	Access        string            `json:"access"`
}

// PanelCollabModel identifies a model in PanelCollabHost.
type PanelCollabModel struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// FoundDownload is the extension's answer to `findDownload`.
// upstream: protocol.ts:128-131
type FoundDownload struct {
	Filename string `json:"filename"`
	State    string `json:"state"`
}

// HiddenFrame is another extension's frame the relay extension emptied and hid.
// upstream: protocol.ts:161-168
type HiddenFrame struct {
	ExtensionID string `json:"extensionId"`
	Path        string `json:"path"`
	Title       string `json:"title,omitempty"`
}

// IsHiddenFrame checks whether raw JSON represents a well-formed HiddenFrame.
// upstream: protocol.ts:171-181
func IsHiddenFrame(raw map[string]any) (HiddenFrame, bool) {
	if raw == nil {
		return HiddenFrame{}, false
	}
	extID, ok1 := raw["extensionId"].(string)
	pathVal, ok2 := raw["path"].(string)
	if !ok1 || !ok2 {
		return HiddenFrame{}, false
	}
	var title string
	if tv, exists := raw["title"]; exists && tv != nil {
		ts, ok := tv.(string)
		if !ok {
			return HiddenFrame{}, false
		}
		title = ts
	}
	return HiddenFrame{ExtensionID: extID, Path: pathVal, Title: title}, true
}

// RelayRpcRequest describes an RPC sent from relay to extension.
// upstream: protocol.ts:25-56
type RelayRpcRequest struct {
	Op                 string         `json:"op"`
	TabID              int            `json:"tabId,omitempty"`
	SessionID          string         `json:"sessionId,omitempty"`
	Method             string         `json:"method,omitempty"`
	Params             map[string]any `json:"params,omitempty"`
	ClearForeignFrames *bool          `json:"clearForeignFrames,omitempty"`
	URL                string         `json:"url,omitempty"`
	Active             *bool          `json:"active,omitempty"`
	TabIDs             []int          `json:"tabIds,omitempty"`
	Title              string         `json:"title,omitempty"`
	Color              string         `json:"color,omitempty"`
	GUID               string         `json:"guid,omitempty"`
	Action             OverlayAction  `json:"action,omitempty"`
}

// RelayToExtMessage is the wire envelope sent relay -> extension.
// upstream: protocol.ts:137-149
type RelayToExtMessage struct {
	T                  string         `json:"t"`
	ID                 int            `json:"id,omitempty"`
	Op                 string         `json:"op,omitempty"`
	TabID              int            `json:"tabId,omitempty"`
	SessionID          string         `json:"sessionId,omitempty"`
	Method             string         `json:"method,omitempty"`
	Params             map[string]any `json:"params,omitempty"`
	ClearForeignFrames *bool          `json:"clearForeignFrames,omitempty"`
	URL                string         `json:"url,omitempty"`
	Active             *bool          `json:"active,omitempty"`
	TabIDs             []int          `json:"tabIds,omitempty"`
	Title              string         `json:"title,omitempty"`
	Color              string         `json:"color,omitempty"`
	GUID               string         `json:"guid,omitempty"`
	Action             OverlayAction  `json:"action,omitempty"`
	Input              *OverlayInput  `json:"input,omitempty"`
	OK                 *bool          `json:"ok,omitempty"`
	Result             any            `json:"result,omitempty"`
	Error              string         `json:"error,omitempty"`
}

// ExtToRelayMessage is the wire envelope sent extension -> relay.
// upstream: protocol.ts:251-314
type ExtToRelayMessage struct {
	T                     string           `json:"t"`
	UserAgent             string           `json:"userAgent,omitempty"`
	BrowserVersion        string           `json:"browserVersion,omitempty"`
	Tabs                  []TabSnapshot    `json:"tabs,omitempty"`
	AttachedTabIDs        []int            `json:"attachedTabIds,omitempty"`
	DiscardedTabsProtocol int              `json:"discardedTabsProtocol,omitempty"`
	InstanceID            string           `json:"instanceId,omitempty"`
	BrowserSession        string           `json:"browserSession,omitempty"`
	OmpVersion            *string          `json:"ompVersion,omitempty"`
	OmpBuild              *string          `json:"ompBuild,omitempty"`
	ReloadProtocol        int              `json:"reloadProtocol,omitempty"`
	DownloadsProtocol     int              `json:"downloadsProtocol,omitempty"`
	OverlayProtocol       int              `json:"overlayProtocol,omitempty"`
	PanelProtocol         int              `json:"panelProtocol,omitempty"`
	ID                    int              `json:"id,omitempty"`
	Call                  *PanelCall       `json:"call,omitempty"`
	Busy                  bool             `json:"busy,omitempty"`
	TabID                 int              `json:"tabId,omitempty"`
	SessionID             string           `json:"sessionId,omitempty"`
	Method                string           `json:"method,omitempty"`
	Params                map[string]any   `json:"params,omitempty"`
	Reason                string           `json:"reason,omitempty"`
	RelayInitiated        bool             `json:"relayInitiated,omitempty"`
	Frames                []map[string]any `json:"frames,omitempty"`
	Tab                   *TabSnapshot     `json:"tab,omitempty"`
	OK                    bool             `json:"ok,omitempty"`
	Result                json.RawMessage  `json:"result,omitempty"`
	Error                 string           `json:"error,omitempty"`
}

// ToNativeHost is a message sent extension -> native host.
// upstream: protocol.ts:325-329
type ToNativeHost struct {
	T           string  `json:"t"`
	Cwd         *string `json:"cwd,omitempty"`
	ExtensionID *string `json:"extensionId,omitempty"`
	Line        *string `json:"line,omitempty"`
}

// FromNativeHost is a message sent native host -> extension.
// upstream: protocol.ts:337-344
type FromNativeHost struct {
	T      string `json:"t"`
	PID    int    `json:"pid,omitempty"`
	Line   string `json:"line,omitempty"`
	Reason string `json:"reason,omitempty"`
	Relay  bool   `json:"relay,omitempty"`
	ID     int    `json:"id,omitempty"`
	Index  *int   `json:"index,omitempty"`
	Last   *bool  `json:"last,omitempty"`
	Data   string `json:"data,omitempty"`
	Code   *int   `json:"code"`
	Stderr string `json:"stderr,omitempty"`
}

// MarshalJSON ensures FromNativeHost emits only the fields relevant to its message type,
// matching omp's discriminated union serialization (especially `code: null` on `exit`
// and omitting `code` on all other messages).
func (m FromNativeHost) MarshalJSON() ([]byte, error) {
	switch m.T {
	case "ready":
		return json.Marshal(struct {
			T   string `json:"t"`
			PID int    `json:"pid"`
		}{T: "ready", PID: m.PID})
	case "rpc":
		return json.Marshal(struct {
			T    string `json:"t"`
			Line string `json:"line"`
		}{T: "rpc", Line: m.Line})
	case "linked":
		return []byte(`{"t":"linked"}`), nil
	case "unlinked":
		return json.Marshal(struct {
			T      string `json:"t"`
			Reason string `json:"reason"`
		}{T: "unlinked", Reason: m.Reason})
	case "relay":
		return json.Marshal(struct {
			T    string `json:"t"`
			Line string `json:"line"`
		}{T: "relay", Line: m.Line})
	case "chunk":
		idx := 0
		if m.Index != nil {
			idx = *m.Index
		}
		last := false
		if m.Last != nil {
			last = *m.Last
		}
		if m.Relay {
			return json.Marshal(struct {
				T     string `json:"t"`
				Relay bool   `json:"relay"`
				ID    int    `json:"id"`
				Index int    `json:"index"`
				Last  bool   `json:"last"`
				Data  string `json:"data"`
			}{T: "chunk", Relay: true, ID: m.ID, Index: idx, Last: last, Data: m.Data})
		}
		return json.Marshal(struct {
			T     string `json:"t"`
			ID    int    `json:"id"`
			Index int    `json:"index"`
			Last  bool   `json:"last"`
			Data  string `json:"data"`
		}{T: "chunk", ID: m.ID, Index: idx, Last: last, Data: m.Data})
	case "exit":
		return json.Marshal(struct {
			T      string `json:"t"`
			Code   *int   `json:"code"`
			Stderr string `json:"stderr"`
		}{T: "exit", Code: m.Code, Stderr: m.Stderr})
	default:
		return nil, fmt.Errorf("unknown FromNativeHost type %q", m.T)
	}
}

var ompVersionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?$`)

// IsOmpVersion reports whether version is a well-formed omp release version.
// upstream: protocol.ts:190-192
func IsOmpVersion(version string) bool {
	if len(version) < 5 || len(version) > 64 {
		return false
	}
	return ompVersionRE.MatchString(version)
}

// IsOlderOmpVersion reports whether version is provably older than than:
// unreported ("") is older than anything reported; otherwise both must be valid
// semver omp versions.
// upstream: protocol.ts:199-202
func IsOlderOmpVersion(version, than string) bool {
	if version == "" {
		return than != ""
	}
	if !IsOmpVersion(version) || !IsOmpVersion(than) {
		return false
	}
	return compareSemver(version, than) < 0
}

func compareSemver(a, b string) int {
	aCore, aPre, aHasPre := strings.Cut(a, "-")
	bCore, bPre, bHasPre := strings.Cut(b, "-")
	aParts := strings.Split(aCore, ".")
	bParts := strings.Split(bCore, ".")
	for i := range 3 {
		ai, _ := strconv.Atoi(aParts[i])
		bi, _ := strconv.Atoi(bParts[i])
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	if !aHasPre && !bHasPre {
		return 0
	}
	if !aHasPre && bHasPre {
		return 1 // release > prerelease
	}
	if aHasPre && !bHasPre {
		return -1
	}
	aIds := strings.Split(aPre, ".")
	bIds := strings.Split(bPre, ".")
	for i := 0; i < len(aIds) && i < len(bIds); i++ {
		if aIds[i] == bIds[i] {
			continue
		}
		aNum, aErr := strconv.Atoi(aIds[i])
		bNum, bErr := strconv.Atoi(bIds[i])
		switch {
		case aErr == nil && bErr == nil:
			if aNum < bNum {
				return -1
			}
			return 1
		case aErr == nil:
			return -1 // numeric identifiers always have lower precedence than non-numeric
		case bErr == nil:
			return 1
		default:
			if aIds[i] < bIds[i] {
				return -1
			}
			return 1
		}
	}
	if len(aIds) < len(bIds) {
		return -1
	}
	if len(aIds) > len(bIds) {
		return 1
	}
	return 0
}

// RelayExtensionStamp is what an extension build declares above its background.js.
// upstream: protocol.ts:225-229
type RelayExtensionStamp struct {
	OmpVersion string
	OmpBuild   *string
}

// StampRelayExtensionScript prepends OMP_INSTALL_VERSION and OMP_INSTALL_BUILD to script.
// upstream: protocol.ts:211-213
func StampRelayExtensionScript(script, ompVersion, build string) string {
	v, _ := json.Marshal(ompVersion)
	b, _ := json.Marshal(build)
	return fmt.Sprintf("const OMP_INSTALL_VERSION = %s;\nconst OMP_INSTALL_BUILD = %s;\n%s", v, b, script)
}

var extensionStampRE = regexp.MustCompile(`^const OMP_INSTALL_VERSION = ("(?:[^"\\]|\\.)*");\n(?:const OMP_INSTALL_BUILD = ("(?:[^"\\]|\\.)*");\n)?`)

// ReadRelayExtensionStamp reads the stamp declared at the top of an installed background.js.
// upstream: protocol.ts:232-243
func ReadRelayExtensionStamp(script string) *RelayExtensionStamp {
	m := extensionStampRE.FindStringSubmatch(script)
	if len(m) < 2 || m[1] == "" {
		return nil
	}
	var version string
	if err := json.Unmarshal([]byte(m[1]), &version); err != nil {
		return nil
	}
	var buildPtr *string
	if len(m) >= 3 && m[2] != "" {
		var build string
		if err := json.Unmarshal([]byte(m[2]), &build); err != nil {
			return nil
		}
		buildPtr = &build
	}
	return &RelayExtensionStamp{
		OmpVersion: version,
		OmpBuild:   buildPtr,
	}
}

// StampRelayExtensionManifest sets version_name in manifestJson to ompVersion.
// upstream: protocol.ts:246-248
func StampRelayExtensionManifest(manifestJSON, ompVersion string) (string, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(manifestJSON), &obj); err != nil {
		return "", err
	}
	obj["version_name"] = ompVersion
	out, err := json.MarshalIndent(obj, "", "\t")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}
