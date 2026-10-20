package bashparse

// ParseUnresolvedAt reports whether tree-sitter failed to resolve the syntax at node: the node's
// own error, or its immediate predecessor's (parse-health.ts parseUnresolvedAt). Error recovery
// strands a discarded operator ahead of the redirect it belonged to, so the redirect-shaped
// question widens to the previous sibling; the statement-shaped one ([ParseUnresolvedWithin])
// must not.
//
// This and ParseUnresolvedWithin are the one place HasError and PreviousSibling are read.
func ParseUnresolvedAt(node *Node) bool {
	return node.hasError || (node.prev != nil && node.prev.hasError)
}

// ParseUnresolvedWithin reports whether tree-sitter failed to resolve the syntax anywhere within
// node (parse-health.ts parseUnresolvedWithin): a statement holding an unresolved region is one
// whose recovered shape is invented rather than observed.
func ParseUnresolvedWithin(node *Node) bool {
	return node.hasError
}
