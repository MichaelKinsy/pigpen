package ternbrowser

// Typed request structs for every Tern daemon "browser" op.
// Using typed structs guarantees S1: "op" and "block" are always set by
// ternbrowser code and can NEVER be overwritten by a model-supplied map.
//
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts

type opBlocksReq struct {
	Op string `json:"op"` // "blocks"
}

type opOpenReq struct {
	Op     string `json:"op"` // "open"
	Owner  int    `json:"owner"`
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Show   *bool  `json:"show,omitempty"`
	How    string `json:"how,omitempty"`
}

type opBlockOnlyReq struct {
	Op    string `json:"op"` // "close", "release", "state", "pdf", "cookies"
	Block int    `json:"block"`
}

type opGotoReq struct {
	Op    string `json:"op"` // "goto"
	Block int    `json:"block"`
	URL   string `json:"url"`
}

type opNavReq struct {
	Op    string `json:"op"` // "nav"
	Block int    `json:"block"`
	Go    string `json:"go"` // "back", "forward", "reload"
}

type opEvalReq struct {
	Op               string `json:"op"` // "eval"
	Block            int    `json:"block"`
	Function         string `json:"function"`
	Args             []any  `json:"args"`
	World            string `json:"world"` // "page" | "isolated"
	Frame            string `json:"frame,omitempty"`
	FailUnresponsive *bool  `json:"failUnresponsive,omitempty"`
}

type opInputReq struct {
	Op     string      `json:"op"` // "input"
	Block  int         `json:"block"`
	Events []InputStep `json:"events"`
}

type opEventsReq struct {
	Op    string `json:"op"` // "events"
	Block int    `json:"block"`
	After int    `json:"after"`
	Wait  bool   `json:"wait,omitempty"`
}

type scriptSpec struct {
	Source string `json:"source"`
	World  string `json:"world"`  // "page" | "isolated"
	Frames string `json:"frames"` // "main" | "all"
	At     string `json:"at"`     // "start" | "end"
}

type opScriptsReq struct {
	Op      string       `json:"op"` // "scripts"
	Block   int          `json:"block"`
	Scripts []scriptSpec `json:"scripts"`
}

type captureRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type opCaptureReq struct {
	Op               string       `json:"op"` // "capture"
	Block            int          `json:"block"`
	Scale            float64      `json:"scale"`
	Format           string       `json:"format"` // "png" | "jpeg"
	Quality          *int         `json:"quality,omitempty"`
	Rect             *captureRect `json:"rect,omitempty"`
	Full             bool         `json:"full,omitempty"`
	FailUnresponsive *bool        `json:"failUnresponsive,omitempty"`
}

type opViewportReq struct {
	Op     string `json:"op"` // "viewport"
	Block  int    `json:"block"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type opEditReq struct {
	Op     string `json:"op"` // "edit"
	Block  int    `json:"block"`
	Action string `json:"action"` // "selectAll" | "copy" | "paste"
}

type opClipboardReq struct {
	Op   string  `json:"op"` // "clipboard"
	Text *string `json:"text,omitempty"`
}

type ternCookiePayload struct {
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Domain   string   `json:"domain"`
	Path     string   `json:"path"`
	Expires  *float64 `json:"expires"`
	HTTPOnly bool     `json:"httpOnly"`
	Secure   bool     `json:"secure"`
	SameSite *string  `json:"sameSite"`
}

type opSetCookieReq struct {
	Op     string            `json:"op"` // "setCookie"
	Block  int               `json:"block"`
	Cookie ternCookiePayload `json:"cookie"`
}

type opDeleteCookieReq struct {
	Op     string `json:"op"` // "deleteCookie"
	Block  int    `json:"block"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

type opDialogReq struct {
	Op     string  `json:"op"` // "dialog"
	Block  int     `json:"block"`
	Accept bool    `json:"accept"`
	Text   *string `json:"text,omitempty"`
}

type opDialogsReq struct {
	Op     string `json:"op"` // "dialogs"
	Block  int    `json:"block"`
	Policy string `json:"policy"` // "default" | "accept" | "dismiss"
}

type opDownloadsReq struct {
	Op    string `json:"op"` // "downloads"
	Block int    `json:"block"`
	Dir   string `json:"dir"`
}

type opFilesReq struct {
	Op    string   `json:"op"` // "files"
	Block int      `json:"block"`
	Paths []string `json:"paths"` // nil marshals to JSON null (disarms preset)
}

type opAllowReq struct {
	Op    string   `json:"op"` // "allow"
	Block int      `json:"block"`
	Hosts []string `json:"hosts"`
}

type opAgentReq struct {
	Op    string  `json:"op"` // "agent"
	Block int     `json:"block"`
	Value *string `json:"value"`
}

type opAppearanceReq struct {
	Op    string  `json:"op"` // "appearance"
	Block int     `json:"block"`
	Value *string `json:"value"`
}

type opCredentialsReq struct {
	Op       string `json:"op"` // "credentials"
	Block    int    `json:"block"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type opBoolValueReq struct {
	Op    string `json:"op"` // "insecure", "fileAccess"
	Block int    `json:"block"`
	Value bool   `json:"value"`
}

type opIdentityReq struct {
	Op    string `json:"op"` // "identity"
	Block int    `json:"block"`
	Seed  string `json:"seed"`
}

type opDomSnapshotReq struct {
	Op               string `json:"op"` // "dom"
	Block            int    `json:"block"`
	Read             string `json:"read"` // "snapshot"
	Root             string `json:"root,omitempty"`
	IncludeAll       bool   `json:"includeAll,omitempty"`
	ViewportOnly     bool   `json:"viewportOnly,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	FailUnresponsive *bool  `json:"failUnresponsive,omitempty"`
}

type opDomResolveReq struct {
	Op               string `json:"op"` // "dom"
	Block            int    `json:"block"`
	Read             string `json:"read"` // "resolve"
	Ref              string `json:"ref"`
	Action           string `json:"action,omitempty"`
	Stable           *int   `json:"stable,omitempty"`
	Timeout          int    `json:"timeout,omitempty"`
	FailUnresponsive *bool  `json:"failUnresponsive,omitempty"`
}

type opDomQueryReq struct {
	Op               string   `json:"op"` // "dom"
	Block            int      `json:"block"`
	Read             string   `json:"read"` // "query"
	By               string   `json:"by"`
	Value            string   `json:"value,omitempty"`
	Name             string   `json:"name,omitempty"`
	Attribute        string   `json:"attribute,omitempty"`
	Exact            bool     `json:"exact,omitempty"`
	Root             string   `json:"root,omitempty"`
	VisibleOnly      bool     `json:"visibleOnly,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Include          []string `json:"include,omitempty"`
	FailUnresponsive *bool    `json:"failUnresponsive,omitempty"`
}

type opDomWaitReq struct {
	Op               string   `json:"op"` // "dom"
	Block            int      `json:"block"`
	Read             string   `json:"read"` // "wait"
	For              DomQuery `json:"for"`
	State            string   `json:"state,omitempty"`
	Timeout          int      `json:"timeout,omitempty"`
	FailUnresponsive *bool    `json:"failUnresponsive,omitempty"`
}
