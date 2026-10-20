//go:build !linux && !darwin && !windows

package warden

import "io/fs"

// No birth time is read here; scratchPlatform is false for these systems, so nothing depends on it.
func birthtimeMs(string, fs.FileInfo) float64 { return 0 }

func fileDevIno(fs.FileInfo) (uint64, uint64) { return 0, 0 }
