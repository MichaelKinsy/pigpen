package ohmypig

import "testing"

// A fused extension that panics while registering takes the Binary down at start.
func TestExtensionRegistersWithoutPanic(t *testing.T) {
	if Extension() == nil {
		t.Fatal("Extension() returned nil")
	}
}
