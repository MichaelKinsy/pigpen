package doctor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type toolchain struct {
	GoPath     string
	GoVersion  string // go1.26.1
	Compile    string // go1.26.7
	Goroot     string // effective
	NatGoroot  string // GOROOT without the inherited variable
	EnvGoroot  string
	EnvGotool  string
	Mise       []string
	Missing    bool
	Mismatch   bool
	ProbeError string
}

func envGet(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

func envWithout(env []string, keys ...string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, d := range keys {
			if k == d {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func lookPath(env []string, name string) string {
	for _, dir := range filepath.SplitList(envGet(env, "PATH")) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func lastToken(s, prefix string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, prefix) {
			return f
		}
	}
	return ""
}

func probeToolchain(o Options) toolchain {
	tc := toolchain{EnvGoroot: envGet(o.Env, "GOROOT"), EnvGotool: envGet(o.Env, "GOTOOLCHAIN")}
	tc.GoPath = lookPath(o.Env, "go")
	if tc.GoPath == "" {
		tc.Missing = true
	} else {
		if out, err := o.Exec(o.Env, tc.GoPath, "version"); err != nil {
			tc.ProbeError = err.Error()
		} else {
			tc.GoVersion = lastToken(out, "go1")
		}
		if out, err := o.Exec(o.Env, tc.GoPath, "tool", "compile", "-V"); err == nil {
			tc.Compile = lastToken(strings.ReplaceAll(out, "compile version ", " "), "go1")
		} else if tc.ProbeError == "" {
			tc.ProbeError = err.Error()
		}
		if out, err := o.Exec(o.Env, tc.GoPath, "env", "GOROOT"); err == nil {
			tc.Goroot = strings.TrimSpace(out)
		}
		if tc.EnvGoroot != "" {
			if out, err := o.Exec(envWithout(o.Env, "GOROOT"), tc.GoPath, "env", "GOROOT"); err == nil {
				tc.NatGoroot = strings.TrimSpace(out)
			}
		}
		tc.Mismatch = tc.GoVersion != "" && tc.Compile != "" && tc.GoVersion != tc.Compile
	}
	data := envGet(o.Env, "MISE_DATA_DIR")
	if data == "" && o.UserHome != "" {
		data = filepath.Join(o.UserHome, ".local", "share", "mise")
	}
	if data != "" {
		if es, err := os.ReadDir(filepath.Join(data, "installs", "go")); err == nil {
			for _, e := range es {
				if e.IsDir() {
					tc.Mise = append(tc.Mise, e.Name())
				}
			}
			sort.Strings(tc.Mise)
		}
	}
	return tc
}

// gotoolchainForces reports whether GOTOOLCHAIN pins a version different from the go on PATH.
func (tc toolchain) gotoolchainForces() bool {
	v := tc.EnvGotool
	if v == "" || v == "local" || v == "auto" || tc.GoVersion == "" {
		return false
	}
	v = strings.TrimSuffix(v, "+auto")
	if v == "path" || strings.HasSuffix(v, "+path") {
		return false
	}
	return strings.HasPrefix(v, "go") && v != tc.GoVersion
}
