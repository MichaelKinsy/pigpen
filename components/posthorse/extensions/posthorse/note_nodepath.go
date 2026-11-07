package posthorse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
)

// Ports of the Node 24 pieces the notes store depends on, so its path and file system behaviour is
// Node's and not Go's:
//
//   - lib/path.js: posix and win32 resolve / relative / dirname / isAbsolute (normalizeString and the
//     root matching), as functions of an explicit working directory so both flavours run, and are
//     tested against Node's own answers, on any host;
//   - lib/fs.js realpathSync (the JS implementation note-scopes.ts:110 calls): `path.resolve` first
//     (`..` is removed lexically before any link is followed), then component by component with
//     lstat/stat/readlink, so errors name the component and the syscall Node names, and junctions are
//     followed on Windows as libuv reports them as links.
//
// The per-drive working directory of Windows (`process.env["=C:"]`) is not modelled: a path on another
// drive resolves against that drive's root, as Node does when the variable is unset.

// nodeStyle is a path flavour.
type nodeStyle struct{ win32 bool }

var (
	posixStyle = nodeStyle{}
	win32Style = nodeStyle{win32: true}
	// hostStyle is the flavour of the running platform (`node:path` is path.win32 on Windows).
	hostStyle = nodeStyle{win32: runtime.GOOS == "windows"}
)

func (s nodeStyle) sepByte() byte {
	if s.win32 {
		return '\\'
	}
	return '/'
}

func (s nodeStyle) isSep(c byte) bool {
	if s.win32 {
		return c == '/' || c == '\\'
	}
	return c == '/'
}

func isDeviceRoot(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }

// normalizeString is lib/path.js normalizeString.
func (s nodeStyle) normalizeString(path string, allowAboveRoot bool) string {
	sep := string(s.sepByte())
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if s.isSep(code) {
			break
		} else {
			code = '/'
		}
		if s.isSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := strings.LastIndex(res, sep)
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndex(res, sep)
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res += sep + ".."
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res += sep + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}
	return res
}

// resolve is path.resolve(...parts) with cwd as process.cwd().
func (s nodeStyle) resolve(cwd string, parts ...string) string {
	if s.win32 {
		return win32ResolveAll(cwd, parts...)
	}
	resolved := ""
	absolute := false
	for i := len(parts) - 1; i >= -1 && !absolute; i-- {
		path := cwd
		if i >= 0 {
			path = parts[i]
		}
		if len(path) == 0 {
			continue
		}
		resolved = path + "/" + resolved
		absolute = path[0] == '/'
	}
	resolved = s.normalizeString(resolved, !absolute)
	if absolute {
		return "/" + resolved
	}
	if len(resolved) > 0 {
		return resolved
	}
	return "."
}

// uncRoot matches path.win32's UNC root at path[0:2] == `\\`: it returns the device text and rootEnd, or
// ok=false when the leading double separator is not a complete UNC root.
func uncRoot(path string) (device string, rootEnd int, ok bool) {
	isSep := win32Style.isSep
	n := len(path)
	j := 2
	last := j
	for j < n && !isSep(path[j]) {
		j++
	}
	if j < n && j != last {
		firstPart := path[last:j]
		last = j
		for j < n && isSep(path[j]) {
			j++
		}
		if j < n && j != last {
			last = j
			for j < n && !isSep(path[j]) {
				j++
			}
			if j == n || j != last {
				if firstPart == "." || firstPart == "?" {
					// A device root (`\\.\pipe`, `\\?\C:`), not a UNC share: only the prefix is the device.
					return `\\` + firstPart, 4, true
				}
				return `\\` + firstPart + `\` + path[last:j], j, true
			}
		}
	}
	return "", 0, false
}

// win32ResolveAll is path.win32.resolve.
func win32ResolveAll(cwd string, args ...string) string {
	isSep := win32Style.isSep
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false
	for i := len(args) - 1; i >= -1; i-- {
		var path string
		if i >= 0 {
			path = args[i]
			if len(path) == 0 {
				continue
			}
		} else if len(resolvedDevice) == 0 {
			path = cwd
		} else {
			// No per-drive working directory: the cwd when it is on that drive, else the drive's root.
			path = cwd
			if len(path) < 3 || strings.ToLower(path[:2]) != strings.ToLower(resolvedDevice) || path[2] != '\\' {
				path = resolvedDevice + `\`
			}
		}
		n := len(path)
		rootEnd := 0
		device := ""
		isAbs := false
		var code byte
		if n > 0 {
			code = path[0]
		}
		if n == 1 {
			if isSep(code) {
				rootEnd = 1
				isAbs = true
			}
		} else if n > 1 && isSep(code) {
			isAbs = true
			if isSep(path[1]) {
				if d, end, ok := uncRoot(path); ok {
					device, rootEnd = d, end
				}
			} else {
				rootEnd = 1
			}
		} else if n > 1 && isDeviceRoot(code) && path[1] == ':' {
			device = path[:2]
			rootEnd = 2
			if n > 2 && isSep(path[2]) {
				isAbs = true
				rootEnd = 3
			}
		}
		if len(device) > 0 {
			if len(resolvedDevice) > 0 {
				if strings.ToLower(device) != strings.ToLower(resolvedDevice) {
					continue
				}
			} else {
				resolvedDevice = device
			}
		}
		if resolvedAbsolute {
			if len(resolvedDevice) > 0 {
				break
			}
		} else {
			resolvedTail = path[rootEnd:] + `\` + resolvedTail
			resolvedAbsolute = isAbs
			if isAbs && len(resolvedDevice) > 0 {
				break
			}
		}
	}
	resolvedTail = win32Style.normalizeString(resolvedTail, !resolvedAbsolute)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if out := resolvedDevice + resolvedTail; out != "" {
		return out
	}
	return "."
}

// relative is path.relative(from, to).
func (s nodeStyle) relative(cwd, from, to string) string {
	if s.win32 {
		return win32Relative(cwd, from, to)
	}
	if from == to {
		return ""
	}
	from = s.resolve(cwd, from)
	to = s.resolve(cwd, to)
	if from == to {
		return ""
	}
	fromStart, fromEnd := 1, len(from)
	fromLen := fromEnd - fromStart
	toStart := 1
	toLen := len(to) - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fromCode := from[fromStart+i]
		if fromCode != to[toStart+i] {
			break
		} else if fromCode == '/' {
			lastCommonSep = i
		}
	}
	if i == length {
		if toLen > length {
			if to[toStart+i] == '/' {
				return to[toStart+i+1:]
			}
			if i == 0 {
				return to[toStart+i:]
			}
		} else if fromLen > length {
			if from[fromStart+i] == '/' {
				lastCommonSep = i
			} else if i == 0 {
				lastCommonSep = 0
			}
		}
	}
	out := ""
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || from[i] == '/' {
			if len(out) == 0 {
				out += ".."
			} else {
				out += "/.."
			}
		}
	}
	return out + to[toStart+lastCommonSep:]
}

// win32Relative is path.win32.relative.
func win32Relative(cwd, from, to string) string {
	if from == to {
		return ""
	}
	fromOrig := win32ResolveAll(cwd, from)
	toOrig := win32ResolveAll(cwd, to)
	if fromOrig == toOrig {
		return ""
	}
	from = strings.ToLower(fromOrig)
	to = strings.ToLower(toOrig)
	if from == to {
		return ""
	}
	fromStart := 0
	for fromStart < len(from) && from[fromStart] == '\\' {
		fromStart++
	}
	fromEnd := len(from)
	for fromEnd-1 > fromStart && from[fromEnd-1] == '\\' {
		fromEnd--
	}
	fromLen := fromEnd - fromStart
	toStart := 0
	for toStart < len(to) && to[toStart] == '\\' {
		toStart++
	}
	toEnd := len(to)
	for toEnd-1 > toStart && to[toEnd-1] == '\\' {
		toEnd--
	}
	toLen := toEnd - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fromCode := from[fromStart+i]
		if fromCode != to[toStart+i] {
			break
		} else if fromCode == '\\' {
			lastCommonSep = i
		}
	}
	if i != length {
		if lastCommonSep == -1 {
			return toOrig
		}
	} else {
		if toLen > length {
			if to[toStart+i] == '\\' {
				return toOrig[toStart+i+1:]
			}
			if i == 2 {
				return toOrig[toStart+i:]
			}
		}
		if fromLen > length {
			if from[fromStart+i] == '\\' {
				lastCommonSep = i
			} else if i == 2 {
				lastCommonSep = 3
			}
		}
		if lastCommonSep == -1 {
			lastCommonSep = 0
		}
	}
	out := ""
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || from[i] == '\\' {
			if len(out) == 0 {
				out += ".."
			} else {
				out += `\..`
			}
		}
	}
	toStart += lastCommonSep
	if len(out) > 0 {
		return out + toOrig[toStart:toEnd]
	}
	if toStart < len(toOrig) && toOrig[toStart] == '\\' {
		toStart++
	}
	return toOrig[toStart:toEnd]
}

// dirname is path.dirname.
func (s nodeStyle) dirname(path string) string {
	if s.win32 {
		return win32Dirname(path)
	}
	if len(path) == 0 {
		return "."
	}
	hasRoot := path[0] == '/'
	end := -1
	matchedSlash := true
	for i := len(path) - 1; i >= 1; i-- {
		if path[i] == '/' {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if hasRoot {
			return "/"
		}
		return "."
	}
	if hasRoot && end == 1 {
		return "//"
	}
	return path[:end]
}

// win32Dirname is path.win32.dirname.
func win32Dirname(path string) string {
	isSep := win32Style.isSep
	n := len(path)
	if n == 0 {
		return "."
	}
	rootEnd := -1
	offset := 0
	code := path[0]
	if n == 1 {
		if isSep(code) {
			return path
		}
		return "."
	}
	if isSep(code) {
		rootEnd, offset = 1, 1
		if isSep(path[1]) {
			j := 2
			last := j
			for j < n && !isSep(path[j]) {
				j++
			}
			if j < n && j != last {
				last = j
				for j < n && isSep(path[j]) {
					j++
				}
				if j < n && j != last {
					last = j
					for j < n && !isSep(path[j]) {
						j++
					}
					if j == n {
						return path
					}
					if j != last {
						rootEnd, offset = j+1, j+1
					}
				}
			}
		}
	} else if isDeviceRoot(code) && path[1] == ':' {
		rootEnd = 2
		if n > 2 && isSep(path[2]) {
			rootEnd = 3
		}
		offset = rootEnd
	}
	end := -1
	matchedSlash := true
	for i := n - 1; i >= offset; i-- {
		if isSep(path[i]) {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if rootEnd == -1 {
			return "."
		}
		end = rootEnd
	}
	return path[:end]
}

// isAbsolute is path.isAbsolute.
func (s nodeStyle) isAbsolute(p string) bool {
	if !s.win32 {
		return len(p) > 0 && p[0] == '/'
	}
	if len(p) == 0 {
		return false
	}
	return s.isSep(p[0]) || (len(p) > 2 && isDeviceRoot(p[0]) && p[1] == ':' && s.isSep(p[2]))
}

// hostCwd is process.cwd().
func hostCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "/"
	}
	return cwd
}

// cwdFor is process.cwd() when resolving parts needs it: on posix a path that is absolute ends the
// search, so the syscall is skipped.
func cwdFor(parts ...string) string {
	if !hostStyle.win32 {
		for _, p := range parts {
			if len(p) > 0 && p[0] == '/' {
				return "/"
			}
		}
	}
	return hostCwd()
}

// jsResolve, jsRelative and jsDirname are node:path's resolve, relative and dirname.
func jsResolve(parts ...string) string { return hostStyle.resolve(cwdFor(parts...), parts...) }
func jsRelative(from, to string) string {
	return hostStyle.relative(cwdFor(from, to), from, to)
}
func jsDirname(path string) string { return hostStyle.dirname(path) }

// ---- fs.realpathSync (lib/fs.js, the JS implementation) -------------------------------------------

var win32SplitRoot = regexp.MustCompile(`^(?:[a-zA-Z]:|[\\/]{2}[^\\/]+[\\/][^\\/]+)?[\\/]*`)

// splitRoot is lib/fs.js splitRoot: the root of a resolved path.
func (s nodeStyle) splitRoot(str string) string {
	if s.win32 {
		return win32SplitRoot.FindString(str)
	}
	for i := 0; i < len(str); i++ {
		if str[i] != '/' {
			return str[:i]
		}
	}
	return str
}

// nextPart is the index of the next separator at or after i, or -1.
func (s nodeStyle) nextPart(p string, i int) int {
	for ; i < len(p); i++ {
		if s.isSep(p[i]) {
			return i
		}
	}
	return -1
}

// isLinkInfo is `isFileType(stats, S_IFLNK)` of libuv: a symlink, and on Windows a junction (Go 1.23+
// reports it as an irregular file; libuv reports it as a link because it can read its target).
func isLinkInfo(info fs.FileInfo, path string) bool {
	if info.Mode()&fs.ModeSymlink != 0 {
		return true
	}
	if runtime.GOOS == "windows" && info.Mode()&fs.ModeIrregular != 0 {
		_, err := os.Readlink(path)
		return err == nil
	}
	return false
}

// nodeRealpath is fs.realpathSync(p) (without the cache argument and without seenLinks, which only
// saves syscalls). Errors are Node's: `ELOOP: too many symbolic links encountered, stat '<component>'`.
func nodeRealpath(p string) (string, error) { return hostStyle.realpath(hostCwd(), p) }

func (s nodeStyle) realpath(cwd, p string) (string, error) {
	p = s.resolve(cwd, p)
	knownHard := map[string]struct{}{}
	current := s.splitRoot(p)
	base := current
	pos := len(current)
	if s.win32 {
		if _, err := os.Lstat(base); err != nil {
			return "", nodeFsError(err, "lstat", base)
		}
		knownHard[base] = struct{}{}
	}
	for pos < len(p) {
		previous := current
		if result := s.nextPart(p, pos); result == -1 {
			last := p[pos:]
			current += last
			base = previous + last
			pos = len(p)
		} else {
			current += p[pos : result+1]
			base = previous + p[pos:result]
			pos = result + 1
		}
		if _, ok := knownHard[base]; ok {
			continue
		}
		info, err := os.Lstat(base)
		if err != nil {
			return "", nodeFsError(err, "lstat", base)
		}
		if !isLinkInfo(info, base) {
			knownHard[base] = struct{}{}
			continue
		}
		if _, err := os.Stat(base); err != nil {
			return "", nodeFsError(err, "stat", base)
		}
		target, err := os.Readlink(base)
		if err != nil {
			return "", nodeFsError(err, "readlink", base)
		}
		resolvedLink := s.resolve(cwd, previous, target)
		p = s.resolve(cwd, resolvedLink, p[pos:])
		current = s.splitRoot(p)
		base = current
		pos = len(current)
		if s.win32 {
			if _, ok := knownHard[base]; !ok {
				if _, err := os.Lstat(base); err != nil {
					return "", nodeFsError(err, "lstat", base)
				}
				knownHard[base] = struct{}{}
			}
		}
	}
	return p, nil
}

// realpathNative is fs.promises.realpath (libuv realpath(3)): the walk of note-inventory.ts:70. On Unix
// the kernel-style resolution of filepath.EvalSymlinks gives the same path; errors are named `realpath`.
// On Windows the link-following JS port stands in (junctions are followed; the native call also
// normalizes the case of the path, which is not reproduced).
func realpathNative(p string) (string, error) {
	if runtime.GOOS == "windows" {
		real, err := nodeRealpath(p)
		if err != nil {
			var fe *fsError
			if errors.As(err, &fe) {
				return "", nodeFsError(fe.Err, "realpath", p)
			}
		}
		return real, err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if strings.Contains(err.Error(), "too many links") {
			err = &os.PathError{Op: "realpath", Path: p, Err: syscall.ELOOP}
		}
		return "", nodeFsError(err, "realpath", p)
	}
	return real, nil
}
