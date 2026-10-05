package native

// OutputRate is the one sample rate the engine plays at: Opus is 48 kHz natively
// and AAC (44.1 or 48 kHz) is resampled to it.
const OutputRate = 48000

// Decoded is one track's audio as 48 kHz interleaved stereo float32.
type Decoded interface {
	// Read fills p with up to len(p)/2 frames and returns the frames written.
	// It returns io.EOF, with no frames, at the end of the track.
	Read(p []float32) (int, error)
	// SeekFrame moves to frame (at OutputRate) from the start of the track.
	SeekFrame(frame int64) error
	// Frames is the track's length in frames, or -1 when the container does not say.
	Frames() int64
	Close() error
}
