//go:build android

package wax

import (
	"errors"

	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// NewOtoOutput: oto's Android driver needs cgo (Oboe), so Termux has no native
// audio output; the engine selection keeps Android on mpv.
func NewOtoOutput() (native.Output, error) {
	return nil, errors.New("the native engine has no audio output on Android; use mpv")
}
