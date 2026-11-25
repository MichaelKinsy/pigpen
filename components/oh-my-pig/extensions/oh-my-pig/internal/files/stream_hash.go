package files

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Streaming xxHash32 (seed 0) matching crates/edit/src/store.rs:87-103 and internal/hashline/xxh32.go.
// Kept local to internal/files so we can hash files up to HardSnapshotCapBytes (64 MiB) in 64 KiB
// chunks without loading them into memory or modifying internal/hashline.
const (
	xxhPrime32_1 uint32 = 0x9E3779B1
	xxhPrime32_2 uint32 = 0x85EBCA77
	xxhPrime32_3 uint32 = 0xC2B2AE3D
	xxhPrime32_4 uint32 = 0x27D4EB2F
	xxhPrime32_5 uint32 = 0x165667B1
)

type streamXXH32 struct {
	totalLen uint32
	v1       uint32
	v2       uint32
	v3       uint32
	v4       uint32
	mem      [16]byte
	memSize  int
	large    bool
}

func newStreamXXH32(seed uint32) streamXXH32 {
	return streamXXH32{
		v1: seed + xxhPrime32_1 + xxhPrime32_2,
		v2: seed + xxhPrime32_2,
		v3: seed,
		v4: seed - xxhPrime32_1,
	}
}

func rotl32(x uint32, r uint) uint32 {
	return (x << r) | (x >> (32 - r))
}

func xxh32Round(acc, input uint32) uint32 {
	acc += input * xxhPrime32_2
	acc = rotl32(acc, 13)
	acc *= xxhPrime32_1
	return acc
}

func (h *streamXXH32) update(b []byte) {
	if len(b) == 0 {
		return
	}
	h.totalLen += uint32(len(b))
	if h.memSize+len(b) < 16 {
		copy(h.mem[h.memSize:], b)
		h.memSize += len(b)
		return
	}
	h.large = true
	p := 0
	if h.memSize > 0 {
		need := 16 - h.memSize
		copy(h.mem[h.memSize:], b[:need])
		h.v1 = xxh32Round(h.v1, binary.LittleEndian.Uint32(h.mem[0:4]))
		h.v2 = xxh32Round(h.v2, binary.LittleEndian.Uint32(h.mem[4:8]))
		h.v3 = xxh32Round(h.v3, binary.LittleEndian.Uint32(h.mem[8:12]))
		h.v4 = xxh32Round(h.v4, binary.LittleEndian.Uint32(h.mem[12:16]))
		p = need
		h.memSize = 0
	}
	for p+16 <= len(b) {
		h.v1 = xxh32Round(h.v1, binary.LittleEndian.Uint32(b[p:p+4]))
		h.v2 = xxh32Round(h.v2, binary.LittleEndian.Uint32(b[p+4:p+8]))
		h.v3 = xxh32Round(h.v3, binary.LittleEndian.Uint32(b[p+8:p+12]))
		h.v4 = xxh32Round(h.v4, binary.LittleEndian.Uint32(b[p+12:p+16]))
		p += 16
	}
	if p < len(b) {
		h.memSize = copy(h.mem[:], b[p:])
	}
}

func (h *streamXXH32) digest() uint32 {
	var acc uint32
	if h.large {
		acc = rotl32(h.v1, 1) + rotl32(h.v2, 7) + rotl32(h.v3, 12) + rotl32(h.v4, 18)
	} else {
		acc = h.v3 + xxhPrime32_5
	}
	acc += h.totalLen

	p := 0
	for p+4 <= h.memSize {
		acc += binary.LittleEndian.Uint32(h.mem[p:p+4]) * xxhPrime32_3
		acc = rotl32(acc, 17) * xxhPrime32_4
		p += 4
	}
	for p < h.memSize {
		acc += uint32(h.mem[p]) * xxhPrime32_5
		acc = rotl32(acc, 11) * xxhPrime32_1
		p++
	}

	acc ^= acc >> 15
	acc *= xxhPrime32_2
	acc ^= acc >> 13
	acc *= xxhPrime32_3
	acc ^= acc >> 16
	return acc
}

// streamFileHash computes the 4-hex hashline content tag for absPath by streaming in 64 KiB chunks.
// It strips a leading UTF-8 BOM and drops trailing ' ', '\t', and '\r' before each '\n' (and at EOF),
// producing the exact same digest as hashline.FileHash(hashline.NormalizeToLF(bomStripped)).
// Returns ("", nil) when the file exceeds HardSnapshotCapBytes (64 MiB).
// upstream: crates/edit/src/store.rs:87-103,377-390
func streamFileHash(absPath string, fileSize int64) (string, error) {
	if fileSize > HardSnapshotCapBytes {
		return "", nil
	}
	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := newStreamXXH32(0)
	buf := make([]byte, 64*1024)
	// pending holds trailing ' ', '\t', '\r' bytes that are emitted only if a non-trailing byte
	// follows on the same line (before '\n' or EOF).
	var pending []byte
	checkedBOM := false
	var bomBuf [3]byte
	bomLen := 0

	feed := func(chunk []byte) {
		start := 0
		for i := range chunk {
			c := chunk[i]
			switch c {
			case ' ', '\t', '\r':
				if i > start {
					if len(pending) > 0 {
						h.update(pending)
						pending = pending[:0]
					}
					h.update(chunk[start:i])
				}
				pending = append(pending, c)
				start = i + 1
			case '\n':
				if i > start {
					if len(pending) > 0 {
						h.update(pending)
						pending = pending[:0]
					}
					h.update(chunk[start:i])
				}
				// Drop trailing spaces/tabs/CRs at line end, then hash the '\n'.
				pending = pending[:0]
				h.update([]byte{'\n'})
				start = i + 1
			default:
				if len(pending) > 0 {
					h.update(pending)
					pending = pending[:0]
				}
			}
		}
		if start < len(chunk) {
			if len(pending) > 0 {
				h.update(pending)
				pending = pending[:0]
			}
			h.update(chunk[start:])
		}
	}

	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if !checkedBOM {
				need := 3 - bomLen
				take := min(need, len(chunk))
				copy(bomBuf[bomLen:], chunk[:take])
				bomLen += take
				chunk = chunk[take:]
				if bomLen == 3 {
					checkedBOM = true
					if !(bomBuf[0] == 0xef && bomBuf[1] == 0xbb && bomBuf[2] == 0xbf) {
						feed(bomBuf[:3])
					}
				}
			}
			if len(chunk) > 0 {
				feed(chunk)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if !checkedBOM && bomLen > 0 {
		feed(bomBuf[:bomLen])
	}
	// Trailing spaces/tabs/CRs in pending at EOF are dropped, matching store.rs:94.
	return fmt.Sprintf("%04X", h.digest()&0xffff), nil
}

const (
	tailScanChunkSize = 64 * 1024
	tailSeekMaxLines  = 64 * 1024
)

type largeFileTailPlan struct {
	totalLines int
	// seekLine (0-indexed) and seekByte hold the exact byte offset of startLine when retained.
	hasSeek  bool
	seekLine int
	seekByte int64
}

// resolveLargeFileTail scans absPath in 64 KiB chunks to count total lines and retain the byte
// offsets of the last `keep` line starts, so reading `:-N` seeks directly to the tail window
// without buffering the file.
// upstream: packages/coding-agent/src/tools/read.ts:777-830
func resolveLargeFileTail(absPath string, tailCount int, wantLeadingCtx bool) (largeFileTailPlan, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return largeFileTailPlan{}, err
	}
	defer f.Close()

	wanted := tailCount + RangeLeadingContextLines + 1
	keep := 0
	if wanted <= tailSeekMaxLines {
		keep = wanted
	}
	var starts []int64
	if keep > 0 {
		starts = make([]int64, keep)
	}

	chunk := make([]byte, tailScanChunkSize)
	newlines := 0
	var pos int64
	lastByte := -1
	for {
		n, rerr := f.Read(chunk)
		if n > 0 {
			view := chunk[:n]
			search := 0
			for search < len(view) {
				at := bytes.IndexByte(view[search:], '\n')
				if at < 0 {
					break
				}
				newlines++
				if keep > 0 {
					starts[newlines%keep] = pos + int64(search+at+1)
				}
				search += at + 1
			}
			pos += int64(n)
			lastByte = int(view[n-1])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return largeFileTailPlan{}, rerr
		}
	}

	totalLines := newlines
	if pos == 0 || lastByte != '\n' {
		totalLines++
	}
	startLine := max(0, totalLines-tailCount)
	if wantLeadingCtx && startLine > 0 {
		startLine = max(0, startLine-RangeLeadingContextLines)
	}
	plan := largeFileTailPlan{totalLines: totalLines}
	if keep > 0 && startLine > 0 && startLine <= newlines && newlines-startLine < keep {
		plan.hasSeek = true
		plan.seekLine = startLine
		plan.seekByte = starts[startLine%keep]
	}
	return plan, nil
}
