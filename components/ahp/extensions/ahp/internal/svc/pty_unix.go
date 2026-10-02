//go:build linux || darwin

package svc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// SpawnPty starts file on a new pseudoterminal using only the standard library: it opens the
// master, unlocks and opens the slave, makes it the child's controlling terminal (new session) and
// reads the master until the child side closes.
func SpawnPty(file string, args []string, opts PtyOptions, h PtyHandlers) (PtyProcess, error) {
	master, slave, err := openPty()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	p := &unixPty{master: master}
	if err := p.Resize(opts.Cols, opts.Rows); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	cmd := exec.Command(file, args...)
	cmd.Dir = opts.Cwd
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	name := opts.Name
	if name == "" {
		name = "xterm-256color"
	}
	cmd.Env = append(append([]string(nil), env...), "TERM="+name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	slave.Close() // the child owns the only slave end now
	p.cmd = cmd
	p.exited = make(chan struct{})

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		var dec utf8Decoder
		buf := make([]byte, 32*1024)
		for {
			n, err := master.Read(buf)
			if n > 0 && h.OnData != nil {
				if text := dec.Write(buf[:n]); text != "" {
					h.OnData(text)
				}
			}
			if err != nil {
				// EIO is how a Linux master reports that every slave end closed.
				if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
					_ = err
				}
				if tail := dec.Flush(); tail != "" && h.OnData != nil {
					h.OnData(tail)
				}
				return
			}
		}
	}()
	go func() {
		waitErr := cmd.Wait()
		code := 0
		if state := cmd.ProcessState; state != nil {
			code = state.ExitCode()
			if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				code = 128 + int(ws.Signal())
			}
		} else if waitErr != nil {
			code = 1
		}
		// Let the reader drain what the child wrote before it died; a grandchild that kept the slave
		// open must not hold the exit back forever.
		select {
		case <-readDone:
		case <-time.After(250 * time.Millisecond):
		}
		master.Close()
		<-readDone
		close(p.exited)
		if h.OnExit != nil {
			h.OnExit(code)
		}
	}()
	return p, nil
}

type unixPty struct {
	master *os.File
	cmd    *exec.Cmd
	exited chan struct{}

	killOnce sync.Once
}

func (p *unixPty) Write(data string) error {
	_, err := p.master.WriteString(data)
	return err
}

type winsize struct{ Row, Col, X, Y uint16 }

func (p *unixPty) Resize(cols, rows int) error {
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, p.master.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws))); e != 0 {
		return e
	}
	return nil
}

// Kill hangs up the session's process group and escalates to SIGKILL if it lingers.
func (p *unixPty) Kill() error {
	var err error
	p.killOnce.Do(func() {
		if p.cmd == nil || p.cmd.Process == nil {
			return
		}
		pid := p.cmd.Process.Pid
		if e := syscall.Kill(-pid, syscall.SIGHUP); e != nil && !errors.Is(e, syscall.ESRCH) {
			err = e
		}
		go func() {
			select {
			case <-p.exited:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
	})
	return err
}
