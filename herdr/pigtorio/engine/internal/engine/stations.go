package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// penOf is the pen a crate belongs to: its own, else its line's.
func (e *Engine) penOf(c *Crate) string {
	if c.Pen != "" {
		return c.Pen
	}
	if l := e.line(c.Line); l != nil {
		return l.Pen
	}
	return ""
}

// folder is where the crate's work lives: the pen's folder, else the cwd of the
// first pig on its line (or of the pig it was handed to).
func (e *Engine) folder(c *Crate) (string, error) {
	if f := e.farm.Pens[e.penOf(c)].Folder; f != "" {
		return f, nil
	}
	if l := e.line(c.Line); l != nil {
		for _, s := range l.Stops {
			if s.Kind == "pig" {
				if t, ok := e.terms[s.Pig]; ok && t.Cwd != "" {
					return t.Cwd, nil
				}
			}
		}
	}
	if c.At != nil && c.At.Pig != "" {
		if t, ok := e.terms[c.At.Pig]; ok && t.Cwd != "" {
			return t.Cwd, nil
		}
	}
	return "", errors.New("cannot tell which folder this crate lives in (set the pen's folder, or keep its pigs running)")
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// RunShell runs `sh -c script` in dir with a time limit and returns the combined output and exit code.
func RunShell(dir, script string, limit time.Duration) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	if ctx.Err() != nil {
		return out.String() + "\n[killed: the Oven's time limit passed]", 124
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode()
	}
	return out.String() + "\n" + err.Error(), 127
}

// NoTestCommand is the Oven log of a crate held because its pen has no test command.
const NoTestCommand = "No test command: set one in the pen menu (Oven's test command)"

func (e *Engine) startOven(c *Crate) {
	c.State, c.At = StOven, &At{Kind: "oven"}
	test := strings.TrimSpace(e.farm.Pens[e.penOf(c)].Test)
	if test == "" {
		// nothing to run is not green: hold the crate until the pen has a test command
		c.Log = NoTestCommand
		c.State = StBurnt
		e.hist(c, "Held at the Oven", "no test command for this pen")
		e.emit(c, "oven-burnt", "", "no test command")
		return
	}
	dir, err := e.folder(c)
	if err != nil {
		e.fail(c, err.Error())
		return
	}
	e.running[c.ID] = true
	e.hist(c, "Oven is baking", test)
	id, limit := c.ID, e.OvenTimeout
	e.startJob(func() func() {
		out, code := RunShell(dir, test, limit)
		return func() {
			delete(e.running, id)
			if cc := e.crate(id); cc != nil && cc.State == StOven && cc.At != nil && cc.At.Kind == "oven" {
				e.ovenResult(cc, code, out)
			}
		}
	})
}

func (e *Engine) ovenResult(c *Crate, code int, out string) {
	c.Log = tailLines(out, 60)
	if code == 0 {
		e.hist(c, "Oven: green", "")
		e.emit(c, "oven-green", "", "tests passed")
		e.advance(c)
		return
	}
	c.Burns++
	detail := fmt.Sprintf("exit %d, burn %d", code, c.Burns)
	e.hist(c, "Oven: burnt", detail)
	e.emit(c, "oven-burnt", "", detail)
	if c.Burns >= 2 {
		c.State = StBurnt
		e.hist(c, "Held at the Oven", "burnt twice: waiting for you")
		return
	}
	note := fmt.Sprintf("The Oven burnt your last work (tests exited %d). Last lines of its log:\n%s\nFix it and hand off again.", code, c.Log)
	if err := e.returnToPig(c, note); err != nil {
		c.State = StBurnt
		e.hist(c, "Held at the Oven", err.Error())
	}
}

// ShipResult is what the Barn did.
type ShipResult struct {
	Commit string // the new commit; "" when there was nothing to commit
	Branch string // the branch the commit went to
	PR     string
	Notes  []string // non-fatal trouble or remarks, e.g. "nothing to commit"
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// protectedBranch says whether pigs must not commit on branch ("" means detached HEAD):
// main, master, or the branch origin/HEAD points to.
func protectedBranch(dir, branch string) bool {
	if branch == "" || branch == "main" || branch == "master" {
		return true
	}
	if ref, err := git(dir, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		return branch == strings.TrimPrefix(ref, "origin/")
	}
	return false
}

// Ship runs the Barn in dir: add everything and, when something changed, commit it on a
// pig/<crate> branch (never on the default branch). Only a new commit is pushed, and only when
// origin exists and gh is installed does it open a pull request.
func Ship(dir, title, crateID string) (ShipResult, error) {
	var r ShipResult
	if out, err := git(dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return r, fmt.Errorf("%s is not a git folder: %s", dir, out)
	}
	if out, err := git(dir, "add", "-A"); err != nil {
		return r, fmt.Errorf("git add: %s", out)
	}
	status, err := git(dir, "status", "--porcelain")
	if err != nil {
		return r, fmt.Errorf("git status: %s", status)
	}
	branch, _ := git(dir, "symbolic-ref", "--short", "-q", "HEAD")
	if status == "" {
		r.Branch = branch
		r.Notes = append(r.Notes, "nothing to commit")
		return r, nil
	}
	if protectedBranch(dir, branch) {
		target := "pig/" + crateID
		args := []string{"switch", "-c", target}
		if _, err := git(dir, "rev-parse", "--verify", "-q", "refs/heads/"+target); err == nil {
			args = []string{"switch", target}
		}
		if out, err := git(dir, args...); err != nil {
			return r, fmt.Errorf("git switch %s: %s", target, out)
		}
		branch = target
	}
	r.Branch = branch
	if out, err := git(dir, "commit", "-m", title); err != nil {
		return r, fmt.Errorf("git commit: %s", out)
	}
	r.Commit, _ = git(dir, "rev-parse", "--short", "HEAD")
	if _, err := git(dir, "remote", "get-url", "origin"); err != nil {
		return r, nil
	}
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		r.Notes = append(r.Notes, "origin exists but gh is not installed: committed, not pushed")
		return r, nil
	}
	if out, err := git(dir, "push", "-u", "origin", branch); err != nil {
		return r, fmt.Errorf("git push: %s", out)
	}
	cmd := exec.Command(ghBin, "pr", "create", "--fill")
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		r.Notes = append(r.Notes, "gh pr create: "+strings.TrimSpace(se.String()+so.String()))
		return r, nil
	}
	for _, ln := range strings.Split(so.String(), "\n") {
		if ln = strings.TrimSpace(ln); strings.HasPrefix(ln, "http") {
			r.PR = ln
		}
	}
	return r, nil
}

func (e *Engine) startBarn(c *Crate) {
	if e.shipping[c.ID] {
		return
	}
	c.State, c.At = StBarn, &At{Kind: "barn"}
	dir, err := e.folder(c)
	if err != nil {
		e.fail(c, err.Error())
		return
	}
	e.hist(c, "Barn is shipping", "")
	e.shipping[c.ID] = true
	id, title, ship := c.ID, c.Title, e.ShipFunc
	e.startJob(func() func() {
		res, err := ship(dir, title, id)
		return func() {
			delete(e.shipping, id)
			cc := e.crate(id)
			if cc == nil || cc.State != StBarn || cc.At == nil || cc.At.Kind != "barn" {
				return
			}
			if err != nil {
				e.fail(cc, err.Error())
				return
			}
			cc.State, cc.Commit = StShipped, res.Commit
			if res.PR != "" {
				cc.PR = res.PR
			}
			for _, n := range res.Notes {
				e.hist(cc, "Barn note", n)
			}
			detail := "nothing to commit"
			if res.Commit != "" {
				detail = "commit " + res.Commit + " on " + res.Branch
			}
			if res.PR != "" {
				detail += ", " + res.PR
			}
			e.hist(cc, "Shipped", detail)
			e.emit(cc, "shipped", "", detail)
		}
	})
}
