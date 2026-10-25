//go:build linux

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

type key int

const (
	keyOther key = iota
	keyUp
	keyDown
	keySpace
	keyEnter
	keyQuit
	keyEsc
)

func rawMode() (func(), error) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, e := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0); e != 0 {
		return nil, e
	}
	n := old
	n.Lflag &^= syscall.ICANON | syscall.ECHO
	n.Iflag &^= syscall.ICRNL | syscall.IXON
	n.Cc[syscall.VMIN] = 1
	n.Cc[syscall.VTIME] = 0
	if _, _, e := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&n)), 0, 0, 0); e != 0 {
		return nil, e
	}
	return func() {
		syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}, nil
}
func readKey() (key, error) {
	b := make([]byte, 1)
	_, e := os.Stdin.Read(b)
	if e != nil {
		return keyOther, e
	}
	switch b[0] {
	case 'q', 'Q':
		return keyQuit, nil
	case ' ':
		return keySpace, nil
	case '\r', '\n':
		return keyEnter, nil
	case 27:
		seq := make([]byte, 2)
		n, _ := os.Stdin.Read(seq)
		if n == 2 && seq[0] == '[' {
			if seq[1] == 'A' {
				return keyUp, nil
			}
			if seq[1] == 'B' {
				return keyDown, nil
			}
		}
		return keyEsc, nil
	}
	return keyOther, nil
}
func waitAny() { _, _ = readKey() }
