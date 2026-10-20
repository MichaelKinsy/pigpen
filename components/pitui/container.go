package tui

// Container stacks child components vertically. Port of pi-tui's Container
// (without mouse layout).
type Container struct {
	Children []Component
}

// NewContainer returns an empty Container.
func NewContainer() *Container { return &Container{} }

// AddChild appends a child.
func (c *Container) AddChild(component Component) { c.Children = append(c.Children, component) }

// RemoveChild removes the first occurrence of component, if present.
func (c *Container) RemoveChild(component Component) {
	for i, ch := range c.Children {
		if ch == component {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() { c.Children = nil }

// Invalidate invalidates every child.
func (c *Container) Invalidate() {
	for _, ch := range c.Children {
		ch.Invalidate()
	}
}

// Render concatenates the children's lines.
func (c *Container) Render(width int) []string {
	lines := []string{}
	for _, ch := range c.Children {
		lines = append(lines, ch.Render(width)...)
	}
	return lines
}
