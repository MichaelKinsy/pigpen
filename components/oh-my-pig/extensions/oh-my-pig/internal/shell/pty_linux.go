//go:build linux

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// Linux PTY ioctl constants (asm-generic/ioctls.h):
//
//	TIOCGPTN   = _IOR('T', 0x30, unsigned int) = 0x80045430
//	TIOCSPTLCK = _IOW('T', 0x31, int)          = 0x40045431
//	TIOCSWINSZ = 0x5414
const (
	iocTIOCGPTN   = 0x80045430
	iocTIOCSPTLCK = 0x40045431
	iocTIOCSWINSZ = 0x5414
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
// Linux's TIOCGPTN/TIOCSPTLCK ioctls (stdlib only, no cgo).
func openPTY(cols, rows int) (ptmx *os.File, pts *os.File, err error) {
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	ptmx = os.NewFile(uintptr(fd), "/dev/ptmx")
	cleanup := func() { _ = ptmx.Close() }

	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		cleanup()
		return nil, nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), iocTIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		cleanup()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	slaveName := "/dev/pts/" + strconv.FormatUint(uint64(ptn), 10)
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
		Ctty:    0,
	}
}
