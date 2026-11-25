//go:build !unix && !windows

package settings

import "time"

func acquireAdvisoryLock(_ string, _ time.Duration) (func(), error) {
	return func() {}, nil
}
