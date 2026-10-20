package pi_permission_system

import "strings"

// LANE-LOCAL COPY for the forwarding lane: src/access-intent/path-surfaces.ts surfaceFamilyOf, owned by the paths lane
// (pathsurfaces.go SurfaceFamilyOf, same name and body). Delete this file at integration.

// SurfaceFamilyOf is the family a surface belongs to: itself unless it carries a capability suffix over a directional family.
func SurfaceFamilyOf(surface string) string {
	for _, d := range []string{"read", "write"} {
		family, ok := strings.CutSuffix(surface, "_"+d)
		if ok && (family == "path" || family == "external_directory") {
			return family
		}
	}
	return surface
}

// SurfaceFamilyMembers is path-surfaces.ts surfaceFamilyMembers: the read and write members of a directional family, ok false for
// any other surface (paths lane: pathsurfaces.go, same name and signature).
func SurfaceFamilyMembers(surface string) (members [2]string, ok bool) {
	if surface != "path" && surface != "external_directory" {
		return members, false
	}
	return [2]string{surface + "_read", surface + "_write"}, true
}
