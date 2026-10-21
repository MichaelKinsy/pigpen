package doctor

import "time"

// dirLockFresh is how recent a lock directory's modification must be for the
// lock to count as held (lock directories carry no owner we could probe).
const dirLockFresh = time.Minute
