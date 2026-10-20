package pi

// PackageName and OriginalSpec name this port and the original it ports, for the refusals a user
// reads when a feature of pi-ahp is not available here.
const (
	PackageName  = "@pi-in-go/pigpen-ahp"
	OriginalSpec = "git:github.com/Qusic/pi-ahp@4065e98309b03c1d2d916300856c3c21a9c10674"
)

// Unsupported is the one wording of a refusal of something pi-ahp does and this port does not
// (yet): it names the feature and the original.
func Unsupported(feature string) string {
	return PackageName + " (Go port) does not support " + feature + " yet. Install the original extension, " +
		OriginalSpec + ", which runs unchanged in PiG."
}
