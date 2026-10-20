package pi_permission_system

import "strings"

// Port of src/access-intent/path-surfaces.ts.

// ReadOnlyPathBearingTools are the file tools that only read the filesystem (READ_ONLY_PATH_BEARING_TOOLS).
var ReadOnlyPathBearingTools = map[string]bool{"read": true, "find": true, "grep": true, "ls": true}

// PathBearingToolNames are the PATH_BEARING_TOOLS, in the original's order.
var PathBearingToolNames = []string{"read", "write", "edit", "find", "grep", "ls"}

// PathBearingTools is PATH_BEARING_TOOLS as a set.
var PathBearingTools = map[string]bool{"read": true, "write": true, "edit": true, "find": true, "grep": true, "ls": true}

// pathDirectionalFamilies are the surface families that carry the read/write capability axis (ADR 0013 section 3).
var pathDirectionalFamilies = []string{"path", "external_directory"}

// CapabilityDirection is one end of the read/write axis.
type CapabilityDirection string

var capabilityDirections = []CapabilityDirection{"read", "write"}

func isDirectionalFamily(s string) bool {
	for _, f := range pathDirectionalFamilies {
		if f == s {
			return true
		}
	}
	return false
}

// PathSurfaces are the surfaces whose patterns are matched against filesystem paths and fold case and separators on Windows.
var PathSurfaces = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range PathBearingToolNames {
		m[t] = true
	}
	for _, f := range pathDirectionalFamilies {
		m[f] = true
		for _, d := range capabilityDirections {
			m[f+"_"+string(d)] = true
		}
	}
	return m
}()

// SurfaceFamilyOf is the family a surface belongs to: itself unless it carries a capability suffix over a directional family.
func SurfaceFamilyOf(surface string) string {
	for _, d := range capabilityDirections {
		family, ok := strings.CutSuffix(surface, "_"+string(d))
		if ok && isDirectionalFamily(family) {
			return family
		}
	}
	return surface
}

// CapabilityDirectionOf is the direction a surface proves, ok false when it proves neither.
func CapabilityDirectionOf(surface string) (CapabilityDirection, bool) {
	if SurfaceFamilyOf(surface) == surface {
		return "", false
	}
	for _, d := range capabilityDirections {
		if strings.HasSuffix(surface, "_"+string(d)) {
			return d, true
		}
	}
	return "", false
}

// SurfaceFamilyMembers is a family name's two directional members (read, then write), ok false when surface is not a family name.
func SurfaceFamilyMembers(surface string) (members [2]string, ok bool) {
	if !isDirectionalFamily(surface) {
		return members, false
	}
	return [2]string{surface + "_read", surface + "_write"}, true
}

// CapabilitySurfaceForEffect is the narrowest surface in family that an attributed effect names; unproven names the bare family.
func CapabilitySurfaceForEffect(family string, effect AttributedEffect) string {
	switch effect {
	case EffectRead:
		return family + "_read"
	case EffectWrite:
		return family + "_write"
	}
	return family
}

// CapabilitySurfaceForTool is the narrowest surface in family that toolName's identity proves.
func CapabilitySurfaceForTool(family, toolName string) string {
	return CapabilitySurfaceForEffect(family, effectProvenByTool(toolName))
}

func effectProvenByTool(toolName string) AttributedEffect {
	if ReadOnlyPathBearingTools[toolName] {
		return EffectRead
	}
	if toolName == "write" {
		return EffectWrite
	}
	return EffectUnproven
}
