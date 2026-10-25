//go:build !linux && !windows

package builderui

import "errors"

type key int

const (
	keyOther key = iota
	keyUp
	keyDown
	keyEnter
	keyQuit
	keyEsc
)

func rawMode() (func(), error) { return nil, errors.New("raw terminal mode unavailable") }
func readKey() (key, error)    { return keyOther, errors.New("raw terminal mode unavailable") }
