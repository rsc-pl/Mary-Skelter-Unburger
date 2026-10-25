//go:build windows

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
const (
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableVTInput        = 0x0200
	enableVTOutput       = 0x0004
)

var k32 = syscall.NewLazyDLL("kernel32.dll")
var getMode = k32.NewProc("GetConsoleMode")
var setMode = k32.NewProc("SetConsoleMode")

func mode(h syscall.Handle) (uint32, error) {
	var m uint32
	r, _, e := getMode.Call(uintptr(h), uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, e
	}
	return m, nil
}
func set(h syscall.Handle, m uint32) error {
	r, _, e := setMode.Call(uintptr(h), uintptr(m))
	if r == 0 {
		return e
	}
	return nil
}
func rawMode() (func(), error) {
	in := syscall.Handle(os.Stdin.Fd())
	out := syscall.Handle(os.Stdout.Fd())
	im, e := mode(in)
	if e != nil {
		return nil, e
	}
	om, _ := mode(out)
	n := im &^ (enableLineInput | enableEchoInput)
	n |= enableProcessedInput | enableVTInput
	if e = set(in, n); e != nil {
		return nil, e
	}
	_ = set(out, om|enableVTOutput)
	return func() { _ = set(in, im); _ = set(out, om) }, nil
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
