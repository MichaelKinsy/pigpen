package pi_permission_system

import (
	"os"
	"runtime"
	"strings"
	"unicode/utf16"
)

// Port of the parts of Node's `path` module (lib/path.js, v24) the original reads through `PathFlavor.impl`: resolve, normalize,
// join, relative, isAbsolute, dirname, basename and the root of parse(), for both the posix and the win32 strategy. Every
// separator, drive letter and dot is ASCII, so the byte-wise walks equal Node's UTF-16 walks; win32 relative() folds case with
// JavaScript's toLowerCase (which can change a string's length) and so works on UTF-16 units.

// PlatformPath is Node's PlatformPath: `path.posix` or `path.win32`.
type PlatformPath struct {
	// Sep is the preferred separator.
	Sep     string
	windows bool
}

var (
	// PosixPath is `path.posix`.
	PosixPath = &PlatformPath{Sep: "/"}
	// Win32Path is `path.win32`.
	Win32Path = &PlatformPath{Sep: `\`, windows: true}
)

// HostPath is the `path` the process itself gets from `import "node:path"`: win32 on Windows, posix elsewhere.
var HostPath = func() *PlatformPath {
	if runtime.GOOS == "windows" {
		return Win32Path
	}
	return PosixPath
}()

// processCwd is process.cwd(), replaceable in tests.
var processCwd = func() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// processEnv is process.env[name], replaceable in tests.
var processEnv = os.Getenv

func isPosixSep(c byte) bool { return c == '/' }
func isWinSep(c byte) bool   { return c == '/' || c == '\\' }
func isWinDeviceRoot(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// normalizeString is Node's normalizeString (v24), over UTF-16 units because it compares lengths and slices by unit index.
func normalizeString(pathS string, allowAboveRoot bool, separator byte, isSep func(byte) bool) string {
	path := u16(pathS)
	sep := uint16(separator)
	isS := func(c uint16) bool { return c < 0x80 && isSep(byte(c)) }
	var res []uint16
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code uint16
	lastIndex := func(u []uint16) int {
		for i := len(u) - 1; i >= 0; i-- {
			if u[i] == sep {
				return i
			}
		}
		return -1
	}
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if isS(code) {
			break
		} else {
			code = '/'
		}
		if isS(code) {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = nil
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - lastIndex(res)
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = nil
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res = append(res, sep, '.', '.')
					} else {
						res = []uint16{'.', '.'}
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res = append(append(res, sep), path[lastSlash+1:i]...)
				} else {
					res = append([]uint16{}, path[lastSlash+1:i]...)
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
	return s16(res)
}

// posixCwd is Node's posixCwd: on a Windows host the cwd is rewritten into a posix path.
func posixCwd() string {
	cwd := processCwd()
	if runtime.GOOS == "windows" {
		cwd = strings.ReplaceAll(cwd, `\`, "/")
		if i := strings.IndexByte(cwd, '/'); i >= 0 {
			return cwd[i:]
		}
		return ""
	}
	return cwd
}

// Resolve is path.resolve.
func (p *PlatformPath) Resolve(args ...string) string {
	if p.windows {
		return win32Resolve(args)
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "" || args[0] == ".")) {
		if cwd := posixCwd(); len(cwd) > 0 && cwd[0] == '/' {
			return cwd
		}
	}
	resolvedPath := ""
	resolvedAbsolute := false
	for i := len(args) - 1; i >= 0 && !resolvedAbsolute; i-- {
		path := args[i]
		if len(path) == 0 {
			continue
		}
		resolvedPath = path + "/" + resolvedPath
		resolvedAbsolute = path[0] == '/'
	}
	if !resolvedAbsolute {
		cwd := posixCwd()
		resolvedPath = cwd + "/" + resolvedPath
		resolvedAbsolute = len(cwd) > 0 && cwd[0] == '/'
	}
	resolvedPath = normalizeString(resolvedPath, !resolvedAbsolute, '/', isPosixSep)
	if resolvedAbsolute {
		return "/" + resolvedPath
	}
	if len(resolvedPath) > 0 {
		return resolvedPath
	}
	return "."
}

// scanUNC matches a UNC root at the start of path the way resolve, normalize and parse do.
func scanUNC(path string) (firstEnd int, shareStart int, j int, stage int) {
	// stage 0: nothing matched; 1: server matched; 2: server, separators matched and share scanned.
	n := len(path)
	j = 2
	last := j
	for j < n && !isWinSep(path[j]) {
		j++
	}
	if j < n && j != last {
		firstEnd = j
		last = j
		for j < n && isWinSep(path[j]) {
			j++
		}
		if j < n && j != last {
			last = j
			shareStart = last
			for j < n && !isWinSep(path[j]) {
				j++
			}
			return firstEnd, shareStart, j, 2
		}
		return firstEnd, 0, j, 1
	}
	return 0, 0, j, 0
}

func win32Resolve(args []string) string {
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
			path = processCwd()
			// Fast path for the current directory.
			if len(args) == 0 || (len(args) == 1 && (args[0] == "" || args[0] == ".") && len(path) > 0 && isWinSep(path[0])) {
				if runtime.GOOS != "windows" {
					path = strings.ReplaceAll(path, "/", `\`)
				}
				return path
			}
		} else {
			path = processEnv("=" + resolvedDevice)
			if path == "" {
				path = processCwd()
			}
			if len(path) < 3 || (strings.ToLower(sliceUnits(path, 0, 2)) != strings.ToLower(resolvedDevice) && path[2] == '\\') {
				path = resolvedDevice + `\`
			}
		}
		n := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		var code byte
		if n > 0 {
			code = path[0]
		}
		if n == 1 {
			if isWinSep(code) {
				rootEnd = 1
				isAbsolute = true
			}
		} else if n > 1 && isWinSep(code) {
			isAbsolute = true
			if isWinSep(path[1]) {
				firstEnd, shareStart, j, stage := scanUNC(path)
				if stage == 2 && (j == n || j != shareStart) {
					first := path[2:firstEnd]
					if first != "." && first != "?" {
						device = `\\` + first + `\` + path[shareStart:j]
						rootEnd = j
					} else {
						device = `\\` + first
						rootEnd = 4
					}
				}
			} else {
				rootEnd = 1
			}
		} else if n > 1 && isWinDeviceRoot(code) && path[1] == ':' {
			device = path[:2]
			rootEnd = 2
			if n > 2 && isWinSep(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		}
		if len(device) > 0 {
			if len(resolvedDevice) > 0 {
				if jsToLowerCase(device) != jsToLowerCase(resolvedDevice) {
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
			resolvedAbsolute = isAbsolute
			if isAbsolute && len(resolvedDevice) > 0 {
				break
			}
		}
	}
	resolvedTail = normalizeString(resolvedTail, !resolvedAbsolute, '\\', isWinSep)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if s := resolvedDevice + resolvedTail; s != "" {
		return s
	}
	return "."
}

// sliceUnits is String.prototype.slice over UTF-16 units for the short device prefixes Node slices (always ASCII before the drive
// colon, so a byte slice is exact; a longer or shorter string slices what it has).
func sliceUnits(s string, from, to int) string {
	if to > len(s) {
		to = len(s)
	}
	if from > to {
		return ""
	}
	return s[from:to]
}

// Normalize is path.normalize.
func (p *PlatformPath) Normalize(path string) string {
	if p.windows {
		return win32Normalize(path)
	}
	if len(path) == 0 {
		return "."
	}
	isAbsolute := path[0] == '/'
	trailing := path[len(path)-1] == '/'
	path = normalizeString(path, !isAbsolute, '/', isPosixSep)
	if len(path) == 0 {
		if isAbsolute {
			return "/"
		}
		if trailing {
			return "./"
		}
		return "."
	}
	if trailing {
		path += "/"
	}
	if isAbsolute {
		return "/" + path
	}
	return path
}

// windowsReservedNames is WINDOWS_RESERVED_NAMES.
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"COM\u00b9": true, "COM\u00b2": true, "COM\u00b3": true, "LPT\u00b9": true, "LPT\u00b2": true, "LPT\u00b3": true,
}

// isWindowsReservedName is Node's isWindowsReservedName: the upper-cased path.slice(0, colonIndex) is a reserved device name. A
// colonIndex of -1 slices off the last unit (JavaScript's negative index), which normalize's final check relies on.
func isWindowsReservedName(path string, colonIndex int) bool {
	var part string
	if colonIndex < 0 {
		rs := []rune(path)
		if len(rs) == 0 {
			return false
		}
		part = string(rs[:len(rs)-1])
	} else {
		part = path[:colonIndex]
	}
	return windowsReservedNames[strings.ToUpper(part)]
}

func win32Normalize(path string) string {
	n := len(path)
	if n == 0 {
		return "."
	}
	rootEnd := 0
	device := ""
	haveDevice := false
	isAbsolute := false
	code := path[0]
	if n == 1 {
		if isPosixSep(code) {
			return `\`
		}
		return path
	}
	if isWinSep(code) {
		isAbsolute = true
		if isWinSep(path[1]) {
			firstEnd, shareStart, j, stage := scanUNC(path)
			if stage == 2 && (j == n || j != shareStart) {
				first := path[2:firstEnd]
				if first == "." || first == "?" {
					device = `\\` + first
					haveDevice = true
					rootEnd = 4
					colon := strings.IndexByte(path, ':')
					possible := ""
					if colon+1 > 4 {
						possible = path[4 : colon+1]
					}
					if isWindowsReservedName(possible, len(possible)-1) {
						device = `\\?\` + possible
						rootEnd = 4 + len(possible)
					}
				} else if j == n {
					return `\\` + first + `\` + path[shareStart:] + `\`
				} else {
					device = `\\` + first + `\` + path[shareStart:j]
					haveDevice = true
					rootEnd = j
				}
			}
		} else {
			rootEnd = 1
		}
	} else if colon := strings.IndexByte(path, ':'); colon > 0 {
		if isWinDeviceRoot(code) && colon == 1 {
			device = path[:2]
			haveDevice = true
			rootEnd = 2
			if n > 2 && isWinSep(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		} else if isWindowsReservedName(path, colon) {
			device = path[:colon+1]
			haveDevice = true
			rootEnd = colon + 1
		}
	}
	tail := ""
	if rootEnd < n {
		tail = normalizeString(path[rootEnd:], !isAbsolute, '\\', isWinSep)
	}
	if len(tail) == 0 && !isAbsolute {
		tail = "."
	}
	if len(tail) > 0 && isWinSep(path[n-1]) {
		tail += `\`
	}
	if !isAbsolute && !haveDevice && strings.Contains(path, ":") {
		// CVE-2024-36139: a relative tail must not turn into something Windows reads as a drive.
		if len(tail) >= 2 && isWinDeviceRoot(tail[0]) && tail[1] == ':' {
			return `.\` + tail
		}
		index := strings.IndexByte(path, ':')
		for index != -1 {
			if index == n-1 || isWinSep(path[index+1]) {
				return `.\` + tail
			}
			next := strings.IndexByte(path[index+1:], ':')
			if next < 0 {
				break
			}
			index = index + 1 + next
		}
	}
	if isWindowsReservedName(path, strings.IndexByte(path, ':')) {
		return `.\` + device + tail
	}
	if !haveDevice {
		if isAbsolute {
			return `\` + tail
		}
		return tail
	}
	if isAbsolute {
		return device + `\` + tail
	}
	return device + tail
}

// Join is path.join.
func (p *PlatformPath) Join(args ...string) string {
	if p.windows {
		return win32Join(args)
	}
	joined := ""
	have := false
	for _, a := range args {
		if len(a) > 0 {
			if !have {
				joined = a
				have = true
			} else {
				joined += "/" + a
			}
		}
	}
	if !have {
		return "."
	}
	return p.Normalize(joined)
}

func win32Join(args []string) string {
	var parts []string
	for _, a := range args {
		if len(a) > 0 {
			parts = append(parts, a)
		}
	}
	if len(parts) == 0 {
		return "."
	}
	firstPart := parts[0]
	joined := strings.Join(parts, `\`)
	needsReplace := true
	slashCount := 0
	if isWinSep(firstPart[0]) {
		slashCount++
		firstLen := len(firstPart)
		if firstLen > 1 && isWinSep(firstPart[1]) {
			slashCount++
			if firstLen > 2 {
				if isWinSep(firstPart[2]) {
					slashCount++
				} else {
					needsReplace = false
				}
			}
		}
	}
	if needsReplace {
		for slashCount < len(joined) && isWinSep(joined[slashCount]) {
			slashCount++
		}
		if slashCount >= 2 {
			joined = `\` + joined[slashCount:]
		}
	}
	// Normalization is skipped when a part carries a reserved device name.
	var segs []string
	part := ""
	for i := 0; i < len(joined); i++ {
		if joined[i] == '\\' {
			if part != "" {
				segs = append(segs, part)
			}
			part = ""
			for i+1 < len(joined) && joined[i+1] == '\\' {
				i++
			}
		} else {
			part += joined[i : i+1]
		}
	}
	if part != "" {
		segs = append(segs, part)
	}
	for _, p := range segs {
		if colon := strings.IndexByte(p, ':'); colon != -1 && isWindowsReservedName(p, colon) {
			return strings.ReplaceAll(joined, "/", `\`)
		}
	}
	return win32Normalize(joined)
}

// IsAbsolute is path.isAbsolute.
func (p *PlatformPath) IsAbsolute(path string) bool {
	if !p.windows {
		return len(path) > 0 && path[0] == '/'
	}
	n := len(path)
	if n == 0 {
		return false
	}
	code := path[0]
	return isWinSep(code) || (n > 2 && isWinDeviceRoot(code) && path[1] == ':' && isWinSep(path[2]))
}

// Relative is path.relative.
func (p *PlatformPath) Relative(from, to string) string {
	if p.windows {
		return win32Relative(from, to)
	}
	if from == to {
		return ""
	}
	from = p.Resolve(from)
	to = p.Resolve(to)
	if from == to {
		return ""
	}
	const fromStart, toStart = 1, 1
	fromEnd := len(from)
	fromLen := fromEnd - fromStart
	toLen := len(to) - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fc := from[fromStart+i]
		if fc != to[toStart+i] {
			break
		} else if fc == '/' {
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

func u16(s string) []uint16 { return utf16.Encode([]rune(s)) }
func s16(u []uint16) string { return string(utf16.Decode(u)) }

func win32Relative(fromArg, toArg string) string {
	if fromArg == toArg {
		return ""
	}
	fromOrigS := win32Resolve([]string{fromArg})
	toOrigS := win32Resolve([]string{toArg})
	if fromOrigS == toOrigS {
		return ""
	}
	fromS, toS := jsToLowerCase(fromOrigS), jsToLowerCase(toOrigS)
	if fromS == toS {
		return ""
	}
	if jsLen(fromOrigS) != jsLen(fromS) || jsLen(toOrigS) != jsLen(toS) {
		// Lower-casing changed a length, so the index arithmetic below would be off: compare segment by segment.
		fromSplit := strings.Split(fromOrigS, `\`)
		toSplit := strings.Split(toOrigS, `\`)
		if fromSplit[len(fromSplit)-1] == "" {
			fromSplit = fromSplit[:len(fromSplit)-1]
		}
		if toSplit[len(toSplit)-1] == "" {
			toSplit = toSplit[:len(toSplit)-1]
		}
		fromLen, toLen := len(fromSplit), len(toSplit)
		length := min(fromLen, toLen)
		i := 0
		for ; i < length; i++ {
			if jsToLowerCase(fromSplit[i]) != jsToLowerCase(toSplit[i]) {
				break
			}
		}
		if i == 0 {
			return toOrigS
		} else if i == length {
			if toLen > length {
				return strings.Join(toSplit[i:], `\`)
			}
			if fromLen > length {
				return strings.Repeat(`..\`, fromLen-1-i) + ".."
			}
			return ""
		}
		return strings.Repeat(`..\`, fromLen-i) + strings.Join(toSplit[i:], `\`)
	}
	toOrig := u16(toOrigS)
	from := u16(fromS)
	to := u16(toS)
	const bs = uint16('\\')
	fromStart := 0
	for fromStart < len(from) && from[fromStart] == bs {
		fromStart++
	}
	fromEnd := len(from)
	for fromEnd-1 > fromStart && from[fromEnd-1] == bs {
		fromEnd--
	}
	fromLen := fromEnd - fromStart
	toStart := 0
	for toStart < len(to) && to[toStart] == bs {
		toStart++
	}
	toEnd := len(to)
	for toEnd-1 > toStart && to[toEnd-1] == bs {
		toEnd--
	}
	toLen := toEnd - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fc := from[fromStart+i]
		if fc != to[toStart+i] {
			break
		} else if fc == bs {
			lastCommonSep = i
		}
	}
	if i != length {
		if lastCommonSep == -1 {
			return toOrigS
		}
	} else {
		if toLen > length {
			if to[toStart+i] == bs {
				return s16(sliceU16(toOrig, toStart+i+1, len(toOrig)))
			}
			if i == 2 {
				return s16(sliceU16(toOrig, toStart+i, len(toOrig)))
			}
		}
		if fromLen > length {
			if from[fromStart+i] == bs {
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
		if i == fromEnd || from[i] == bs {
			if len(out) == 0 {
				out += ".."
			} else {
				out += `\..`
			}
		}
	}
	toStart += lastCommonSep
	if len(out) > 0 {
		return out + s16(sliceU16(toOrig, toStart, toEnd))
	}
	if toStart < len(toOrig) && toOrig[toStart] == bs {
		toStart++
	}
	return s16(sliceU16(toOrig, toStart, toEnd))
}

// sliceU16 is String.prototype.slice(from, to) for non-negative indexes.
func sliceU16(u []uint16, from, to int) []uint16 {
	if to > len(u) {
		to = len(u)
	}
	if from >= to {
		return nil
	}
	return u[from:to]
}

// Root is path.parse(path).root.
func (p *PlatformPath) Root(path string) string {
	if !p.windows {
		if len(path) > 0 && path[0] == '/' {
			return "/"
		}
		return ""
	}
	n := len(path)
	if n == 0 {
		return ""
	}
	code := path[0]
	if n == 1 {
		if isWinSep(code) {
			return path
		}
		return ""
	}
	rootEnd := 0
	if isWinSep(code) {
		rootEnd = 1
		if isWinSep(path[1]) {
			_, shareStart, j, stage := scanUNC(path)
			if stage == 2 {
				if j == n {
					rootEnd = j
				} else if j != shareStart {
					rootEnd = j + 1
				}
			}
		}
	} else if isWinDeviceRoot(code) && path[1] == ':' {
		if n <= 2 {
			return path
		}
		rootEnd = 2
		if isWinSep(path[2]) {
			if n == 3 {
				return path
			}
			rootEnd = 3
		}
	}
	return path[:rootEnd]
}

// Dirname is path.dirname.
func (p *PlatformPath) Dirname(path string) string {
	if p.windows {
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

func win32Dirname(path string) string {
	n := len(path)
	if n == 0 {
		return "."
	}
	rootEnd := -1
	offset := 0
	code := path[0]
	if n == 1 {
		if isWinSep(code) {
			return path
		}
		return "."
	}
	if isWinSep(code) {
		rootEnd = 1
		offset = 1
		if isWinSep(path[1]) {
			j := 2
			last := j
			for j < n && !isWinSep(path[j]) {
				j++
			}
			if j < n && j != last {
				last = j
				for j < n && isWinSep(path[j]) {
					j++
				}
				if j < n && j != last {
					last = j
					for j < n && !isWinSep(path[j]) {
						j++
					}
					if j == n {
						return path
					}
					if j != last {
						rootEnd = j + 1
						offset = j + 1
					}
				}
			}
		}
	} else if isWinDeviceRoot(code) && path[1] == ':' {
		if n <= 2 {
			return path
		}
		rootEnd = 2
		offset = 2
		if isWinSep(path[2]) {
			if n == 3 {
				return path
			}
			rootEnd = 3
			offset = 3
		}
	}
	end := -1
	matchedSlash := true
	for i := n - 1; i >= offset; i-- {
		if isWinSep(path[i]) {
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

// Basename is path.basename(path) without an extension argument.
func (p *PlatformPath) Basename(path string) string {
	if p.windows {
		return win32Basename(path)
	}
	start := 0
	end := -1
	matchedSlash := true
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if !matchedSlash {
				start = i + 1
				break
			}
		} else if end == -1 {
			matchedSlash = false
			end = i + 1
		}
	}
	if end == -1 {
		return ""
	}
	return path[start:end]
}

func win32Basename(path string) string {
	start := 0
	end := -1
	matchedSlash := true
	if len(path) >= 2 && isWinDeviceRoot(path[0]) && path[1] == ':' {
		start = 2
	}
	for i := len(path) - 1; i >= start; i-- {
		if isWinSep(path[i]) {
			if !matchedSlash {
				start = i + 1
				break
			}
		} else if end == -1 {
			matchedSlash = false
			end = i + 1
		}
	}
	if end == -1 {
		return ""
	}
	return path[start:end]
}
