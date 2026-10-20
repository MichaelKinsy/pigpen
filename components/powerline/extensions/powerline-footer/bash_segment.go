package powerline_footer

// shellModeSegment is the shell_mode segment: the shell's name, "run" or "idle", and the shell's directory, in the shellMode
// color; invisible outside bash mode. upstream: segments.ts:77.
func shellModeSegment(ctx *segmentContext) renderedSegment { return *new(renderedSegment) }
