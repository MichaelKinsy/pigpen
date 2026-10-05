package pi_subagents

import (
	"regexp"
	"strings"
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(?:\.[a-z0-9][a-z0-9-]*)*$`)
	nonPackageChars   = regexp.MustCompile(`[^a-z0-9.\-]`)
	dashRun           = regexp.MustCompile(`-+`)
	dotRun            = regexp.MustCompile(`\.+`)
	packageEdges      = regexp.MustCompile(`^[-.]+|[-.]+$`)
)

// normalizePackageName sanitizes a package name: trimmed, lower case, white space to dashes, only [a-z0-9.-], runs collapsed.
func normalizePackageName(value string) string {
	v := jsTrim(value)
	if v == "" {
		return ""
	}
	var b strings.Builder
	inSpace := false
	for _, r := range strings.ToLower(v) {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte('-')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	s := nonPackageChars.ReplaceAllString(b.String(), "")
	s = dashRun.ReplaceAllString(s, "-")
	s = dotRun.ReplaceAllString(s, ".")
	return packageEdges.ReplaceAllString(s, "")
}

// parsePackageName validates the `package` frontmatter value: "" (absent, empty or false) yields no package.
func parsePackageName(value string, present bool, label string) (name string, err string) {
	if !present || value == "" {
		return "", ""
	}
	name = normalizePackageName(value)
	if name == "" || !identifierPattern.MatchString(name) {
		return "", label + " is invalid after sanitization."
	}
	return name, ""
}

func buildRuntimeName(local, pkg string) string {
	if p := jsTrim(pkg); p != "" {
		return p + "." + local
	}
	return local
}

// frontmatterName is the name written back to a file: the local name, without the package prefix.
func frontmatterName(name, local, pkg string) string {
	if local != "" {
		return local
	}
	if pkg != "" && strings.HasPrefix(name, pkg+".") {
		return name[len(pkg)+1:]
	}
	return name
}
