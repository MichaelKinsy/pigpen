package posthorse

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Node's path and url helpers as note-guards.ts uses them (note-guards.ts:25-42). They are
// pure functions of their arguments (and an explicit platform flag) so one table covers both
// platforms.

// normalizeString is path.js normalizeString: collapse `.` and `..` segments of a path whose
// separators are `sep` (and, with isSep, any other separator).
func normalizeString(path string, allowAboveRoot bool, sep byte, isSep func(byte) bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if isSep(code) {
			break
		} else {
			code = '/'
		}
		if isSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := strings.LastIndexByte(res, sep)
						if lastSlashIndex == -1 {
							res, lastSegmentLength = "", 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, sep)
						}
						lastSlash, dots = i, 0
						continue
					} else if len(res) != 0 {
						res, lastSegmentLength = "", 0
						lastSlash, dots = i, 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res += string(sep) + ".."
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res += string(sep) + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash, dots = i, 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}
	return res
}

func isPosixSep(c byte) bool { return c == '/' }
func isWinSep(c byte) bool   { return c == '/' || c == '\\' }
func isWinDeviceRoot(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// posixResolve is path.posix.resolve(cwd, path) for an absolute cwd.
func posixResolve(cwd, path string) string {
	resolved := ""
	absolute := false
	for _, arg := range [...]string{path, cwd} {
		if arg == "" {
			continue
		}
		resolved = arg + "/" + resolved
		absolute = arg[0] == '/'
		if absolute {
			break
		}
	}
	resolved = normalizeString(resolved, !absolute, '/', isPosixSep)
	if absolute {
		return "/" + resolved
	}
	if resolved == "" {
		return "."
	}
	return resolved
}

// win32Resolve is path.win32.resolve(cwd, path) for an absolute cwd (path.js win32.resolve).
func win32Resolve(cwd, path string) string {
	resolvedDevice, resolvedTail := "", ""
	resolvedAbsolute := false
	for _, arg := range [...]string{path, cwd} {
		if arg == "" {
			continue
		}
		rootEnd := 0
		device := ""
		absolute := false
		switch {
		case len(arg) == 1:
			if isWinSep(arg[0]) {
				rootEnd, absolute = 1, true
			}
		case isWinSep(arg[0]):
			// Possible UNC root.
			absolute = true
			if isWinSep(arg[1]) {
				j := 2
				last := j
				for j < len(arg) && !isWinSep(arg[j]) {
					j++
				}
				if j < len(arg) && j != last {
					firstPart := arg[last:j]
					last = j
					for j < len(arg) && isWinSep(arg[j]) {
						j++
					}
					if j < len(arg) && j != last {
						last = j
						for j < len(arg) && !isWinSep(arg[j]) {
							j++
						}
						if j == len(arg) {
							device, rootEnd = `\\`+firstPart+`\`+arg[last:], j
						} else if j != last {
							device, rootEnd = `\\`+firstPart+`\`+arg[last:j], j
						}
					}
				}
			} else {
				rootEnd = 1
			}
		case isWinDeviceRoot(arg[0]) && arg[1] == ':':
			device, rootEnd = arg[:2], 2
			if len(arg) > 2 && isWinSep(arg[2]) {
				absolute, rootEnd = true, 3
			}
		}
		if device != "" {
			if resolvedDevice != "" {
				if strings.ToLower(device) != strings.ToLower(resolvedDevice) {
					continue // a different drive's path cannot extend this one
				}
			} else {
				resolvedDevice = device
			}
		}
		if resolvedAbsolute {
			if resolvedDevice != "" {
				break
			}
		} else {
			resolvedTail = arg[rootEnd:] + `\` + resolvedTail
			resolvedAbsolute = absolute
			if absolute && resolvedDevice != "" {
				break
			}
		}
	}
	resolvedTail = normalizeString(resolvedTail, !resolvedAbsolute, '\\', isWinSep)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if out := resolvedDevice + resolvedTail; out != "" {
		return out
	}
	return "."
}

// nodeResolve is path.resolve(cwd, path) of the host platform.
func nodeResolve(cwd, path string) string {
	if runtime.GOOS == "windows" {
		return win32Resolve(cwd, path)
	}
	return posixResolve(cwd, path)
}

// nodeHomedir is os.homedir(): libuv returns $HOME (USERPROFILE on Windows) verbatim when it is
// set, even empty, and the passwd entry (the user profile directory) otherwise.
func nodeHomedir() (string, error) {
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	if home, ok := os.LookupEnv(key); ok {
		return home, nil
	}
	current, err := user.Current()
	if err != nil || current.HomeDir == "" {
		return "", errors.New("A system error occurred: uv_os_homedir returned ENOENT (no such file or directory)")
	}
	return current.HomeDir, nil
}

// nodeJoin2 is path.join(a, b) of the host platform (empty arguments are skipped, the result is
// normalised, and nothing at all is ".").
func nodeJoin2(a, b string) string {
	switch {
	case a == "":
		a = b
	case b != "":
		a += "/" + b
	}
	return filepath.Clean(a) // Clean("") is "."
}

// uriError is decodeURIComponent's URIError.
type uriError struct{}

func (uriError) Error() string { return "URI malformed" }

// decodeURIComponentJS is decodeURIComponent: every %XX sequence is decoded, a multi-byte
// sequence must be one well-formed UTF-8 character, and anything else throws URIError.
func decodeURIComponentJS(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	hex := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	octet := func(k int) (byte, bool) {
		if k+2 >= len(s) || s[k] != '%' {
			return 0, false
		}
		hi, ok1 := hex(s[k+1])
		lo, ok2 := hex(s[k+2])
		return hi<<4 | lo, ok1 && ok2
	}
	var out strings.Builder
	out.Grow(len(s))
	for k := 0; k < len(s); k++ {
		if s[k] != '%' {
			out.WriteByte(s[k])
			continue
		}
		b, ok := octet(k)
		if !ok {
			return "", uriError{}
		}
		if b < 0x80 {
			out.WriteByte(b)
			k += 2
			continue
		}
		n := 0
		switch {
		case b&0xE0 == 0xC0:
			n = 2
		case b&0xF0 == 0xE0:
			n = 3
		case b&0xF8 == 0xF0:
			n = 4
		default:
			return "", uriError{}
		}
		var seq [4]byte
		seq[0] = b
		for j := 1; j < n; j++ {
			k += 3
			next, ok := octet(k)
			if !ok || next&0xC0 != 0x80 {
				return "", uriError{}
			}
			seq[j] = next
		}
		if r, size := utf8.DecodeRune(seq[:n]); r == utf8.RuneError || size != n {
			return "", uriError{}
		}
		out.Write(seq[:n])
		k += 2
	}
	return out.String(), nil
}

// hostForbidden are the WHATWG forbidden host code points (url.spec "forbidden host code point").
func hostForbidden(host string) bool {
	for i := 0; i < len(host); i++ {
		if c := host[i]; c <= 0x20 || c == 0x7F || strings.IndexByte("#/:<>?@[\\]^|%", c) >= 0 {
			return true
		}
	}
	return false
}

// fileURLToPathOS is node:url fileURLToPath for a `file://` string on the given platform:
// the WHATWG parse of the file scheme (backslash as slash, host lower-casing, `localhost`
// as the empty host, dot segments, drive-letter host and `|`), then Node's getPathFromURLPosix
// or getPathFromURLWin32 including their errors (url.js).
func fileURLToPathOS(raw string, windows bool) (string, error) {
	input := string(jsUTF8Bytes(raw)) // a URL is a USVString: lone surrogates become U+FFFD
	input = strings.TrimFunc(input, func(r rune) bool { return r <= 0x20 })
	input = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, input)
	if len(input) < 7 || !strings.EqualFold(input[:5], "file:") || (input[5] != '/' && input[5] != '\\') || (input[6] != '/' && input[6] != '\\') {
		return "", errors.New("Invalid URL")
	}
	rest := input[5:]
	if end := strings.IndexAny(rest, "?#"); end >= 0 {
		rest = rest[:end]
	}
	rest = strings.ReplaceAll(rest, `\`, "/")
	authority := rest[2:]
	host, path := authority, ""
	if slash := strings.IndexByte(authority, '/'); slash >= 0 {
		host, path = authority[:slash], authority[slash:]
	}
	if len(host) == 2 && isWinDeviceRoot(host[0]) && (host[1] == ':' || host[1] == '|') {
		// A drive letter in the host position starts the path (file host state).
		host, path = "", "/"+host+path
	}
	if host != "" {
		decoded, err := decodeURIComponentJS(host)
		if err != nil || hostForbidden(decoded) {
			return "", errors.New("Invalid URL")
		}
		host = strings.ToLower(decoded)
		if host == "localhost" {
			host = ""
		}
	}
	pathname := wipeDotSegments(path)
	if !windows {
		if host != "" {
			return "", errors.New("File URL host must be \"localhost\" or empty on " + runtime.GOOS)
		}
		if strings.Contains(strings.ToLower(pathname), "%2f") {
			return "", errors.New("File URL path must not include encoded / characters")
		}
		return decodeURIComponentJS(pathname)
	}
	lower := strings.ToLower(pathname)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return "", errors.New("File URL path must not include encoded \\ or / characters")
	}
	decoded, err := decodeURIComponentJS(strings.ReplaceAll(pathname, "/", `\`))
	if err != nil {
		return "", err
	}
	if host != "" {
		// UNC path; domainToUnicode of an `xn--` host is not applied.
		return `\\` + host + decoded, nil
	}
	if len(decoded) < 3 || !isWinDeviceRoot(decoded[1]) || decoded[2] != ':' {
		return "", errors.New("File URL path must be absolute")
	}
	return decoded[1:], nil
}

// wipeDotSegments is the WHATWG path state for a special URL: `.` and `..` (also `%2e`) segments
// are resolved, and a leading `C|` becomes `C:`; an empty path is "/".
func wipeDotSegments(path string) string {
	if path == "" {
		return "/"
	}
	isDot := func(s string) bool { return s == "." || strings.EqualFold(s, "%2e") }
	isDoubleDot := func(s string) bool {
		switch strings.ToLower(s) {
		case "..", ".%2e", "%2e.", "%2e%2e":
			return true
		}
		return false
	}
	isDrive := func(s string) bool {
		return len(s) == 2 && isWinDeviceRoot(s[0]) && (s[1] == ':' || s[1] == '|')
	}
	parts := strings.Split(path, "/")[1:]
	segments := make([]string, 0, len(parts))
	for i, part := range parts {
		last := i == len(parts)-1
		switch {
		case isDoubleDot(part):
			onlyDrive := len(segments) == 1 && len(segments[0]) == 2 && isWinDeviceRoot(segments[0][0]) && segments[0][1] == ':'
			if len(segments) > 0 && !onlyDrive {
				segments = segments[:len(segments)-1]
			}
			if last {
				segments = append(segments, "")
			}
		case isDot(part):
			if last {
				segments = append(segments, "")
			}
		default:
			if len(segments) == 0 && isDrive(part) {
				part = part[:1] + ":"
			}
			segments = append(segments, part)
		}
	}
	return "/" + strings.Join(segments, "/")
}
