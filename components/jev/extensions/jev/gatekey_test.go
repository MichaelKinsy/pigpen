package jev

import "testing"

func TestGateKeyIsBoundedAndDistinguishesCalls(t *testing.T) {
	big := benchInput(200_000)
	k := gateKey("write", big, "/work", "do it")
	if len(k) > 128 {
		t.Fatalf("the memo key is %d bytes for a 200 KB call; it must be a digest, not the arguments", len(k))
	}
	if k != gateKey("write", benchInput(200_000), "/work", "do it") {
		t.Fatal("the same call must produce the same key")
	}
	for name, other := range map[string]string{
		"tool": gateKey("edit", big, "/work", "do it"),
		"cwd":  gateKey("write", big, "/other", "do it"),
		"user": gateKey("write", big, "/work", "do something else"),
		"args": gateKey("write", benchInput(200_042), "/work", "do it"),
	} {
		if other == k {
			t.Errorf("a different %s must produce a different key", name)
		}
	}
}
