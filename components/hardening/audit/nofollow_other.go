//go:build !unix

package audit

// noFollow is 0 where the platform has no O_NOFOLLOW; the opened descriptor is still checked to be a regular file.
const noFollow = 0
