package pi_permission_system

import "strings"

// Port of src/access-intent/bash/msys-bash-tokens.ts: the shape of a bash-command token on a win32 host, where Pi runs commands
// through Git Bash and POSIX-shaped absolute tokens carry MSYS mount semantics.

// BashTokenKind is the `kind` of a BashTokenShape.
type BashTokenKind string

const (
	// BashTokenDevice is a safe MSYS runtime device (/dev/null, /dev/std{in,out,err}).
	BashTokenDevice BashTokenKind = "device"
	// BashTokenDriveMount is an MSYS drive mount (/c/...), WindowsPath its Windows equivalent.
	BashTokenDriveMount BashTokenKind = "drive-mount"
	// BashTokenPosixAbsolute is any other absolute POSIX path, treated literally.
	BashTokenPosixAbsolute BashTokenKind = "posix-absolute"
	// BashTokenPlain is everything else, handled by ordinary win32 resolution.
	BashTokenPlain BashTokenKind = "plain"
)

// BashTokenShape is the original's BashTokenShape union.
type BashTokenShape struct {
	Kind BashTokenKind
	// WindowsPath is set for BashTokenDriveMount only.
	WindowsPath string
}

// ClassifyWin32BashToken is classifyWin32BashToken.
func ClassifyWin32BashToken(token string) BashTokenShape {
	if IsSafeSystemPath(token) {
		return BashTokenShape{Kind: BashTokenDevice}
	}
	// /^\/([a-zA-Z])(\/.*)?$/: `.` stops at a line terminator and `$` only matches the end.
	if len(token) >= 2 && token[0] == '/' && isASCIIAlpha(rune(token[1])) {
		rest := token[2:]
		if rest == "" || (rest[0] == '/' && !hasLineTerminator(rest[1:])) {
			drive := strings.ToUpper(token[1:2]) + ":"
			tail := strings.ReplaceAll(strings.TrimPrefix(rest, "/"), "/", `\`)
			if tail != "" {
				return BashTokenShape{Kind: BashTokenDriveMount, WindowsPath: drive + `\` + tail}
			}
			return BashTokenShape{Kind: BashTokenDriveMount, WindowsPath: drive + `\`}
		}
	}
	if strings.HasPrefix(token, "/") {
		return BashTokenShape{Kind: BashTokenPosixAbsolute}
	}
	return BashTokenShape{Kind: BashTokenPlain}
}
