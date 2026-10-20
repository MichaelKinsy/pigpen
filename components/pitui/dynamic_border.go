package tui

import "strings"

// DynamicBorder renders a horizontal rule that adjusts to the viewport width.
// Port of pi-coding-agent's DynamicBorder; the colour function is required
// (the original defaulted to the global theme's "border" colour).
type DynamicBorder struct {
	Color func(string) string
}

// NewDynamicBorder returns a DynamicBorder with the given colour function.
func NewDynamicBorder(color func(string) string) *DynamicBorder {
	return &DynamicBorder{Color: color}
}

// Invalidate is a no-op.
func (d *DynamicBorder) Invalidate() {}

// Render returns one line of "─" of max(1, width) cells.
func (d *DynamicBorder) Render(width int) []string {
	return []string{d.Color(strings.Repeat("─", max(1, width)))}
}
