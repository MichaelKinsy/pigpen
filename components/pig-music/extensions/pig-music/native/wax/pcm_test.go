package wax

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"io"
	"math"
	"os"
	"testing"

	"github.com/colespringer/waxflow/container"
)

// wav builds a 16-bit PCM WAV whose left channel is a slow ramp (sample i = i*step) and whose right is its negative.
func wav(rate, channels, frames int, step float64) []byte {
	var b bytes.Buffer
	data := frames * channels * 2
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	for i := 0; i < frames; i++ {
		v := int16(float64(i) * step)
		for c := 0; c < channels; c++ {
			s := v
			if c == 1 {
				s = -v
			}
			_ = binary.Write(&b, binary.LittleEndian, s)
		}
	}
	return b.Bytes()
}

func drain(t *testing.T, s native.Decoded) []float32 {
	t.Helper()
	var all []float32
	buf := make([]float32, 2*1000)
	for {
		n, err := s.Read(buf)
		all = append(all, buf[:2*n]...)
		if errors.Is(err, io.EOF) {
			return all
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if n == 0 {
			t.Fatal("Read returned 0 frames and no error")
		}
	}
}

func TestOpenDecodedStereo48kPassesThrough(t *testing.T) {
	s, err := OpenDecoded(container.BytesSource(wav(48000, 2, 48000, 0.5)), "wav")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Frames() != 48000 {
		t.Fatalf("Frames = %d", s.Frames())
	}
	pcm := drain(t, s)
	if len(pcm) != 2*48000 {
		t.Fatalf("%d samples, want %d", len(pcm), 2*48000)
	}
	// left is +ramp, right is -ramp; sample 20000 is 10000/32768.
	want := float32(10000.0 / 32768.0)
	if got := pcm[2*20000]; math.Abs(float64(got-want)) > 1e-3 {
		t.Errorf("left[20000] = %v, want %v", got, want)
	}
	if got := pcm[2*20000+1]; math.Abs(float64(got+want)) > 1e-3 {
		t.Errorf("right[20000] = %v, want %v", got, -want)
	}
}

func TestOpenDecodedMonoBecomesStereo(t *testing.T) {
	s, err := OpenDecoded(container.BytesSource(wav(48000, 1, 4800, 1)), "wav")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pcm := drain(t, s)
	if len(pcm) != 2*4800 {
		t.Fatalf("%d samples", len(pcm))
	}
	for i := 0; i < 4800; i += 97 {
		if pcm[2*i] != pcm[2*i+1] {
			t.Fatalf("frame %d: left %v right %v", i, pcm[2*i], pcm[2*i+1])
		}
	}
}

func TestOpenDecodedResamples44kTo48k(t *testing.T) {
	s, err := OpenDecoded(container.BytesSource(wav(44100, 2, 44100, 0.25)), "wav")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if want := int64(48000); s.Frames() != want {
		t.Fatalf("Frames = %d, want %d", s.Frames(), want)
	}
	pcm := drain(t, s)
	if n := len(pcm) / 2; n < 47990 || n > 48010 {
		t.Fatalf("%d frames out, want about 48000", n)
	}
	// The ramp keeps its slope: at 24000 (0.5 s) the input sample is 22050 -> 22050*0.25/32768.
	want := 22050 * 0.25 / 32768
	if got := float64(pcm[2*24000]); math.Abs(got-want) > 2e-3 {
		t.Errorf("sample at 0.5 s = %v, want %v", got, want)
	}
}

func TestDecodedSeek(t *testing.T) {
	for _, rate := range []int{48000, 44100} {
		s, err := OpenDecoded(container.BytesSource(wav(rate, 2, rate*2, 0.5)), "wav")
		if err != nil {
			t.Fatal(err)
		}
		// Read a little, then seek to 1.0 s, then to 0.25 s, then back to the start.
		buf := make([]float32, 2*100)
		_, _ = s.Read(buf)
		for _, sec := range []float64{1.0, 0.25, 0} {
			if err := s.SeekFrame(int64(sec * 48000)); err != nil {
				t.Fatalf("rate %d: seek %v: %v", rate, sec, err)
			}
			n, err := s.Read(buf)
			if n == 0 || (err != nil && !errors.Is(err, io.EOF)) {
				t.Fatalf("read after seek: %d, %v", n, err)
			}
			want := sec * float64(rate) * 0.5 / 32768
			if got := float64(buf[2*10]); math.Abs(got-want-10*0.5/32768*float64(rate)/48000) > 3e-3 {
				t.Errorf("rate %d: after seek to %.2fs sample = %v, want about %v", rate, sec, got, want)
			}
		}
		_ = s.Close()
	}
}

func TestOpenDecodedRealOpusAndAAC(t *testing.T) {
	for _, c := range []struct {
		file, hint string
		minFrames  int64
	}{{"testdata/opus-stereo.webm", "webm", 5000}, {"testdata/aac-44k.m4a", "m4a", 140000}} {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		s, err := OpenDecoded(container.BytesSource(data), c.hint)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		pcm := drain(t, s)
		if int64(len(pcm)/2) < c.minFrames {
			t.Errorf("%s: %d frames, want at least %d", c.file, len(pcm)/2, c.minFrames)
		}
		peak := 0.0
		for _, v := range pcm {
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		if peak < 0.01 || peak > 1.5 {
			t.Errorf("%s: peak %v looks wrong", c.file, peak)
		}
		if s.Frames() <= 0 {
			t.Errorf("%s: Frames = %d", c.file, s.Frames())
		}
		if err := s.SeekFrame(0); err != nil {
			t.Errorf("%s: seek: %v", c.file, err)
		}
		_ = s.Close()
	}
}

func TestOpenDecodedRejectsGarbage(t *testing.T) {
	if _, err := OpenDecoded(container.BytesSource([]byte("this is not audio at all, not even close")), "webm"); err == nil {
		t.Fatal("garbage opened")
	}
}
