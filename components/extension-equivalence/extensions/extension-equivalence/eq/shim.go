package eq

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed shim/eqshim.go.txt
var shimSource []byte

// shimSet is the directory of shimmed commands plus the socket they report to.
type shimSet struct {
	dir      string // prepended to PATH
	sockPath string
	ln       net.Listener
	specs    map[string]CommandSpec
	real     map[string]string
	record   func(ch string, data any)
}

// buildShimBinary compiles the embedded shim once into dir.
func buildShimBinary(dir string) (string, error) {
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), shimSource, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module eqshim\n\ngo 1.22\n"), 0o644); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "eqshim")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = src
	// The shim is a standalone module of standard-library code: ignore the
	// caller's workspace and flags.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GO111MODULE=on")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build eqshim: %v\n%s", err, b)
	}
	return out, nil
}

// newShimSet installs a shim for every command that is not "missing".
// origPath is the PATH used to find the real tool.
func newShimSet(root string, specs map[string]CommandSpec, origPath string, record func(string, any)) (*shimSet, error) {
	s := &shimSet{
		dir: filepath.Join(root, "shims"), sockPath: filepath.Join(root, "s"),
		specs: specs, real: map[string]string{}, record: record,
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return s, nil
	}
	bin, err := buildShimBinary(filepath.Join(root, "shimbuild"))
	if err != nil {
		return nil, err
	}
	for name, spec := range specs {
		if spec.Mode == "missing" {
			continue
		}
		if spec.Mode == "" || spec.Mode == "real" {
			path, err := lookPath(name, origPath)
			if err != nil {
				return nil, fmt.Errorf("scenario command %q is mode real but not on PATH: %w", name, err)
			}
			s.real[name] = path
		}
		if err := os.Symlink(bin, filepath.Join(s.dir, name)); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return nil, err
	}
	s.ln = ln
	go s.serve()
	return s, nil
}

func lookPath(name, pathList string) (string, error) {
	for _, dir := range filepath.SplitList(pathList) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// pathWithout returns the PATH entries that do not hold any of the named commands.
func pathWithout(pathList string, names []string) string {
	var keep []string
	for _, dir := range filepath.SplitList(pathList) {
		holds := false
		for _, n := range names {
			if info, err := os.Stat(filepath.Join(dir, n)); err == nil && !info.IsDir() {
				holds = true
			}
		}
		if !holds {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

func (s *shimSet) close() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

func (s *shimSet) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *shimSet) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var start struct {
		Cmd  string   `json:"cmd"`
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}
	if json.Unmarshal(line, &start) != nil {
		return
	}
	spec := s.specs[start.Cmd]
	reply := map[string]any{"mode": "real", "path": s.real[start.Cmd]}
	if spec.Mode == "canned" {
		reply = map[string]any{"mode": "canned", "stdout": spec.Stdout, "stderr": spec.Stderr, "exit": spec.Exit}
	}
	args := start.Args
	if args == nil {
		args = []string{}
	}
	s.record(ChExec, map[string]any{"cmd": start.Cmd, "args": args, "cwd": start.Cwd})
	b, _ := json.Marshal(reply)
	_, _ = conn.Write(append(b, '\n'))
	line, err = rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var end struct {
		Cmd  string `json:"cmd"`
		Exit int    `json:"exit"`
	}
	if json.Unmarshal(line, &end) == nil {
		s.record(ChExecEnd, map[string]any{"cmd": end.Cmd, "exit": end.Exit})
	}
	_, _ = conn.Write([]byte("ok\n"))
}
