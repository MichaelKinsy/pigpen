//go:build !android

package wax

import (
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"io"
	"time"

	"github.com/ebitengine/oto/v3"
)

// otoOutput plays through oto: PulseAudio or PipeWire (pure Go) or ALSA on
// Linux, CoreAudio on macOS and WASAPI on Windows, all without cgo.
type otoOutput struct {
	ctx *oto.Context
	p   *oto.Player
}

// NewOtoOutput opens the audio device. oto allows one context per process.
func NewOtoOutput() (native.Output, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: native.OutputRate, ChannelCount: 2, Format: oto.FormatFloat32LE,
		BufferSize: 100 * time.Millisecond, ApplicationName: "pig-music",
	})
	if err != nil {
		return nil, native.Explain(native.RedactError(err))
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return nil, errors.New("the audio device did not come up within 10 s")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("no audio output: %s", native.Redact(err.Error()))
	}
	return &otoOutput{ctx: ctx}, nil
}

func (o *otoOutput) Start(src io.Reader) error {
	o.p = o.ctx.NewPlayer(src)
	return nil
}
func (o *otoOutput) Resume()           { o.p.Play() }
func (o *otoOutput) Pause()            { o.p.Pause() }
func (o *otoOutput) SetGain(g float64) { o.p.SetVolume(g) }
func (o *otoOutput) Buffered() int     { return o.p.BufferedSize() }

// Flush clears oto's buffer: a seek on the player does that, and the engine's
// reader accepts it without moving.
func (o *otoOutput) Flush() { _, _ = o.p.Seek(0, io.SeekCurrent) }
func (o *otoOutput) Close() error {
	if o.p != nil {
		o.p.Pause()
	}
	return nil
}
