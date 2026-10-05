package wax

import (
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"io"

	"github.com/colespringer/waxflow/audio"
	"github.com/colespringer/waxflow/container"
	"github.com/colespringer/waxflow/dsp/resample"
	"github.com/colespringer/waxflow/format"
)

// resamplePreRoll is how many source samples a seek reads before its target to settle the resampler's filter.
const resamplePreRoll = 2048

// mediaStream adapts a WaxFlow media to Decoded: stereo, 48 kHz, interleaved.
type mediaStream struct {
	med    format.Media
	in     *audio.Buffer
	inFmt  audio.Format
	rs     *resample.Resampler // nil when the source is already 48 kHz
	frames int64
	eof    bool
	skip   int64 // output frames still to drop after a seek

	// out is interleaved output waiting to be handed out.
	out []float32
	// scratch for the resampler, one slice per channel
	rsOut [2][]float32
	// conv holds an integer chunk converted to float, one slice per channel
	conv [2][]float32
}

// OpenDecoded opens a demuxer and decoder over src. hint is the container's
// extension ("webm", "m4a"); it may be empty, since the container is sniffed.
func OpenDecoded(src container.Source, hint string) (native.Decoded, error) {
	med, err := format.Open(src, hint, nil)
	if err != nil {
		return nil, fmt.Errorf("opening the stream: %w", err)
	}
	tr := med.Info().Default()
	if tr.Fmt.Channels < 1 {
		_ = med.Close()
		return nil, fmt.Errorf("unsupported audio format %v", tr.Fmt)
	}
	s := &mediaStream{med: med, in: audio.Get(tr.Fmt, 4096), inFmt: tr.Fmt}
	if tr.Fmt.Rate != native.OutputRate {
		if s.rs, err = resample.New(tr.Fmt.Rate, native.OutputRate, 2, resample.HQ); err != nil {
			_ = med.Close()
			return nil, err
		}
		for c := range s.rsOut {
			s.rsOut[c] = make([]float32, 16384)
		}
	}
	if tr.Samples >= 0 {
		s.frames = resample.OutputLen(tr.Samples, tr.Fmt.Rate, native.OutputRate)
		if tr.Fmt.Rate == native.OutputRate {
			s.frames = tr.Samples
		}
	} else {
		s.frames = -1
	}
	return s, nil
}

func (s *mediaStream) Frames() int64 { return s.frames }

func (s *mediaStream) Close() error {
	audio.Put(s.in)
	s.in = nil
	return s.med.Close()
}

func (s *mediaStream) SeekFrame(frame int64) error {
	if frame < 0 {
		frame = 0
	}
	target, skip := frame, int64(0)
	if s.rs != nil {
		// Land a little early and throw the start of the output away: the
		// resampler's filter begins from silence, and that must not be heard.
		target = frame * int64(s.inFmt.Rate) / native.OutputRate
		pre := min(target, resamplePreRoll)
		target -= pre
		skip = (frame - target*native.OutputRate/int64(s.inFmt.Rate))
	}
	if _, err := s.med.SeekSample(target); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	if s.rs != nil {
		s.rs.Reset(0)
	}
	s.out = s.out[:0]
	s.skip = skip
	s.eof = false
	return nil
}

func (s *mediaStream) Read(p []float32) (int, error) {
	want := len(p) / 2
	if want == 0 {
		return 0, nil
	}
	for len(s.out) < 2*want && !s.eof {
		if err := s.fill(); err != nil {
			return 0, err
		}
		if s.skip > 0 {
			drop := int(min(s.skip, int64(len(s.out)/2)))
			s.out = s.out[:copy(s.out, s.out[2*drop:])]
			s.skip -= int64(drop)
		}
	}
	if len(s.out) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:2*want], s.out)
	s.out = s.out[:copy(s.out, s.out[n:])]
	return n / 2, nil
}

// fill decodes one chunk and appends it, converted, to s.out.
func (s *mediaStream) fill() error {
	err := s.med.ReadChunk(s.in)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding: %w", err)
	}
	last := err != nil
	n := s.in.N
	var l, r []float32
	if n > 0 {
		l, r = s.channels()
	}
	if s.rs == nil {
		for i := 0; i < n; i++ {
			s.out = append(s.out, l[i], r[i])
		}
	} else {
		s.resample(l, r)
		if last {
			s.drainResampler()
		}
	}
	if last {
		s.eof = true
	}
	return nil
}

func (s *mediaStream) resample(l, r []float32) {
	src := [][]float32{l, r}
	for len(src[0]) > 0 {
		dst := [][]float32{s.rsOut[0], s.rsOut[1]}
		produced, consumed := s.rs.Process(dst, src)
		s.appendOut(produced)
		src = [][]float32{src[0][consumed:], src[1][consumed:]}
		if produced == 0 && consumed == 0 {
			return
		}
	}
}

func (s *mediaStream) drainResampler() {
	for {
		dst := [][]float32{s.rsOut[0], s.rsOut[1]}
		produced := s.rs.Drain(dst)
		s.appendOut(produced)
		if produced == 0 {
			return
		}
	}
}

func (s *mediaStream) appendOut(n int) {
	for i := 0; i < n; i++ {
		s.out = append(s.out, s.rsOut[0][i], s.rsOut[1][i])
	}
}

// channels returns the chunk's left and right channels as float32 (a mono
// source is both, a wider one is cut to its first two channels).
func (s *mediaStream) channels() (l, r []float32) {
	second := 0
	if s.inFmt.Channels >= 2 {
		second = 1
	}
	if s.inFmt.Type == audio.Float {
		return s.in.ChanF(0), s.in.ChanF(second)
	}
	scale := float32(1) / float32(uint64(1)<<(s.inFmt.BitDepth-1))
	for i, ch := range [2]int{0, second} {
		src := s.in.ChanI(ch)
		if cap(s.conv[i]) < len(src) {
			s.conv[i] = make([]float32, len(src))
		}
		s.conv[i] = s.conv[i][:len(src)]
		for j, v := range src {
			s.conv[i][j] = float32(v) * scale
		}
	}
	return s.conv[0], s.conv[1]
}
