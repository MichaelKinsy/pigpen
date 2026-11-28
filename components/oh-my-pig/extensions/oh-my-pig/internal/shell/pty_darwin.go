//go:build darwin

package shell

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Darwin PTY ioctl constants from <sys/ttycom.h> (verified against MacOSX.sdk):
//
//	TIOCPTYGRANT = _IO('t', 84)            = 0x20007454
//	TIOCPTYGNAME = _IOC(IOC_OUT,'t',83,128)= 0x40807453
//	TIOCPTYUNLK  = _IO('t', 82)            = 0x20007452
//	TIOCSWINSZ   = _IOW('t', 103, winsize) = 0x80087467
const (
	iocTIOCPTYGRANT = 0x20007454
	iocTIOCPTYGNAME = 0x40807453
	iocTIOCPTYUNLK  = 0x20007452
	iocTIOCSWINSZ   = 0x80087467
)

type ptyWinsize struct {
	Rows uint16
	Cols uint16
	X    uint16
	Y    uint16
}

// ptySupported reports whether openPTY is available on this platform.
const ptySupported = true

// openPTY allocates a pseudo-terminal master/slave pair using /dev/ptmx and
// Darwin's TIOCPTY* ioctls (stdlib only, no cgo).
func openPTY(cols, rows int) (ptmx *os.File, pts *os.File, err error) {
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	ptmx = os.NewFile(uintptr(fd), "/dev/ptmx")
	cleanup := func() { _ = ptmx.Close() }

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCPTYGRANT, 0); errno != 0 {
		cleanup()
		return nil, nil, fmt.Errorf("TIOCPTYGRANT: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCPTYUNLK, 0); errno != 0 {
		cleanup()
		return nil, nil, fmt.Errorf("TIOCPTYUNLK: %w", errno)
	}

	var nameBuf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCPTYGNAME, uintptr(unsafe.Pointer(&nameBuf[0]))); errno != 0 {
		cleanup()
		return nil, nil, fmt.Errorf("TIOCPTYGNAME: %w", errno)
	}
	n := bytes.IndexByte(nameBuf[:], 0)
	if n < 0 {
		n = len(nameBuf)
	}
	slaveName := string(nameBuf[:n])

	slaveFD, err := syscall.Open(slaveName, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("open %s: %w", slaveName, err)
	}
	pts = os.NewFile(uintptr(slaveFD), slaveName)

	if cols > 0 && rows > 0 {
		ws := ptyWinsize{Rows: uint16(rows), Cols: uint16(cols)}
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	}
	return ptmx, pts, nil
}

// configurePTYCommand attaches pts as stdio and sets it as the controlling
// terminal of a new session so child processes see isatty() == true and
// killing -pid reaps the process tree.
func configurePTYCommand(cmd *exec.Cmd, pts *os.File) {
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0, // stdin is fd 0 in the child
	}
}
