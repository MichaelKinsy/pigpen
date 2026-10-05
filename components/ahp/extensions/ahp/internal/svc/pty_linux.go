//go:build linux

package svc

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	tiocGPTN   = 0x80045430
	tiocSPTLCK = 0x40045431
)

func openPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*os.File, *os.File, error) { master.Close(); return nil, nil, e }
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		return fail(e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		return fail(e)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	return master, slave, nil
}
