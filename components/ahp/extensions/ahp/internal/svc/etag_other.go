//go:build !unix

package svc

import (
	"fmt"
	"os"
	"time"
)

func etagOf(info os.FileInfo) string {
	return fmt.Sprintf("0-0-%d-%d", info.Size(), info.ModTime().UnixNano())
}

func ctimeOf(info os.FileInfo) time.Time { return info.ModTime() }
