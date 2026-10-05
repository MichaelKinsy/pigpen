package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxProcSource(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, cmdline, environ string) {
		d := filepath.Join(root, pid)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
		if environ != "" {
			_ = os.WriteFile(filepath.Join(d, "environ"), []byte(environ), 0o644)
		}
		_ = os.Symlink("/opt/pig/bin/pig", filepath.Join(d, "exe"))
		_ = os.Symlink("/work", filepath.Join(d, "cwd"))
	}
	mk("100", "/usr/local/bin/pig\x00--mode\x00rpc\x00", "PIG_CODING_AGENT_DIR=/h/x-agent\x00HOME=/h\x00")
	mk("101", "vim\x00a\x00", "")
	_ = os.MkdirAll(filepath.Join(root, "self"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "notapid"), 0o755)
	ps, err := (&procFS{Root: root}).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	var pig, vim Proc
	for _, p := range ps {
		if p.PID == 100 {
			pig = p
		} else {
			vim = p
		}
	}
	if pig.Args[0] != "/usr/local/bin/pig" || pig.Env["PIG_CODING_AGENT_DIR"] != "/h/x-agent" || pig.Exe != "/opt/pig/bin/pig" || pig.Cwd != "/work" {
		t.Errorf("%+v", pig)
	}
	if vim.Env != nil {
		t.Errorf("unreadable environment must be nil, not empty: %+v", vim)
	}
}

func TestPsProcSource(t *testing.T) {
	calls := 0
	exec := func(env []string, name string, args ...string) (string, error) {
		calls++
		if args[0] == "-axo" {
			return "  100 /usr/local/bin/pig --mode rpc\n  200 /bin/zsh -l\n", nil
		}
		// ps eww -p 100 -o command=
		return "/usr/local/bin/pig --mode rpc PIG_CODING_AGENT_DIR=/h/x-agent HOME=/h\n", nil
	}
	ps, err := (&psProcs{Exec: exec}).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].PID != 100 || ps[0].Args[0] != "/usr/local/bin/pig" {
		t.Fatalf("%+v", ps)
	}
	if ps[0].Env["PIG_CODING_AGENT_DIR"] != "/h/x-agent" {
		t.Errorf("env from ps eww: %+v", ps[0].Env)
	}
	if calls != 2 {
		t.Errorf("only pig-like processes get an environment lookup, got %d exec calls", calls)
	}
}

func TestIsPigProcess(t *testing.T) {
	home := "/h/.pig"
	yes := []Proc{
		{PID: 1, Args: []string{"pig"}},
		{PID: 2, Args: []string{"/usr/local/bin/pig", "-p", "x"}},
		{PID: 3, Args: []string{"/x/pig-standard"}, Exe: "/h/.pig/artifacts/piglets/pig-standard/d/unversioned/linux-amd64/pig-standard"},
		{PID: 4, Args: []string{"node", "x"}, Env: map[string]string{"PIG_HOME": "/h/.pig"}},
	}
	no := []Proc{
		{PID: 5, Args: []string{"vim"}},
		{PID: 6, Args: []string{"pig-doctor", "check"}},
		{PID: 999999, Args: []string{"pig"}}, // self
		{PID: 7, Args: []string{"pigz", "-d"}},
	}
	for _, p := range yes {
		if !isPigProcess(p, home, 999999) {
			t.Errorf("%+v should count", p)
		}
	}
	for _, p := range no {
		if isPigProcess(p, home, 999999) {
			t.Errorf("%+v should not count", p)
		}
	}
}
