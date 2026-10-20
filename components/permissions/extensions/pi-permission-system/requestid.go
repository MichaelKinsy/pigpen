package pi_permission_system

import (
	"crypto/rand"
	"fmt"
)

// Port of src/permission-request-id.ts.

// CreatePermissionRequestId mints the identifier for one permission request: `perm-` and a random UUID (v4), distinct from the
// host's toolCallId, which a single tool call shares across the several requests its gates raise.
func CreatePermissionRequestId() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("perm-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
