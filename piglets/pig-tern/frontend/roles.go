package frontend

// With PIG_TERN_ROLES=omp (or Options{Omp: true} through FrontendWith), the
// member draws the transcript and dock with omp's roles and node shapes
// (omp_composer.go, omp_working.go, omp_transcript.go, omp_status.go), which
// Tern's built-in styles and chat styles (Reader, Spine, Console) target.
// The surface role and hello app keep PiG's names so Tern does not mistake the
// pane for omp. Off by default.
const ompRolesEnv = "PIG_TERN_ROLES"

// ompRolesOn reports whether the omp role mode is requested by env.
func ompRolesOn(getenv func(string) string) bool {
	return getenv(ompRolesEnv) == "omp"
}

// footerRoles are omp's status segment roles for the footer's segments
// that have one; extension statuses and the experimental badge have none.
var footerRoles = map[string]string{
	"cwd":     "omp.status.path",
	"branch":  "omp.status.git",
	"session": "omp.status.session_name",
	"usage":   "omp.status.cost",
	"context": "omp.status.context_pct",
	"model":   "omp.status.model",
}
