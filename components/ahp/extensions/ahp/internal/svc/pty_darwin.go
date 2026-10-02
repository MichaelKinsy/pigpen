//go:build darwin

package svc

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	tiocPTYGRANT = 0x20007454
	tiocPTYUNLK  = 0x20007452
	tiocPTYGNAME = 0x40807453
)

func openPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*os.File, *os.File, error) { master.Close(); return nil, nil, e }
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocPTYGRANT, 0); e != 0 {
		return fail(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocPTYUNLK, 0); e != 0 {
		return fail(e)
	}
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		return fail(e)
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	slave, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	return master, slave, nil
}
