package powerline_footer

import "path"

// shellModeSegment is the shell_mode segment: the shell's name, "run" or "idle", and the shell's directory, in the shellMode
// color; invisible outside bash mode. upstream: segments.ts:77.
func shellModeSegment(ctx *segmentContext) renderedSegment {
	if !ctx.Shell.Active {
		return notVisible()
	}
	name := ctx.Shell.Name
	if name == "" {
		name = "shell"
	}
	state := "idle"
	if ctx.Shell.Running {
		state = "run"
	}
	parts := []string{name, state}
	if ctx.Shell.Cwd != "" {
		if base := path.Base(ctx.Shell.Cwd); base != "/" && base != "." {
			parts = append(parts, base)
		} else if base == "." {
			parts = append(parts, ".")
		}
	}
	content := parts[0]
	for _, p := range parts[1:] {
		content += sepDot + p
	}
	return renderedSegment{segColor(ctx, "shellMode", content), true}
}
