package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsOmpVersionAndIsOlderOmpVersion(t *testing.T) {
	valid := []string{"0.1.0", "19.1.1", "18.6.1-rc.1", "1.2.3-alpha.0.beta"}
	for _, v := range valid {
		if !IsOmpVersion(v) {
			t.Errorf("IsOmpVersion(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "1.2", "dev", "build rc2", "18.6.1+build", "18.6.1-foo\nIGNORE", "1.2.3 ", strings.Repeat("1", 65)}
	for _, v := range invalid {
		if IsOmpVersion(v) {
			t.Errorf("IsOmpVersion(%q) = true, want false", v)
		}
	}

	// upstream: browser-relay-probe.test.ts:314-323
	if IsOlderOmpVersion("999.0.0", "18.8.7+build") {
		t.Error("expected reported version not older than unrankable version")
	}
	if IsOlderOmpVersion("999.0.0", "18.8.7-canary-foo") {
		t.Error("expected 999.0.0 not older than 18.8.7-canary-foo")
	}
	if !IsOlderOmpVersion("", "18.8.7+build") {
		t.Error("expected unreported version to be older than non-empty version")
	}
	if IsOlderOmpVersion("", "") {
		t.Error("expected empty not older than empty")
	}
	if !IsOlderOmpVersion("18.6.1", "19.1.1") {
		t.Error("expected 18.6.1 < 19.1.1")
	}
	if !IsOlderOmpVersion("19.1.1-alpha.1", "19.1.1") {
		t.Error("expected prerelease < release")
	}
	if IsOlderOmpVersion("19.1.1", "19.1.1") {
		t.Error("expected equal versions not older")
	}
	if !IsOlderOmpVersion("19.1.1-alpha.2", "19.1.1-alpha.10") {
		t.Error("expected numeric prerelease identifier 2 < 10")
	}
}

func TestExtensionStamps(t *testing.T) {
	script := StampRelayExtensionScript("console.log('hi');\n", "0.1.0", "abc123")
	stamp := ReadRelayExtensionStamp(script)
	if stamp == nil || stamp.OmpVersion != "0.1.0" || stamp.OmpBuild == nil || *stamp.OmpBuild != "abc123" {
		t.Fatalf("unexpected stamp: %+v", stamp)
	}

	legacy := "const OMP_INSTALL_VERSION = \"18.6.1\";\nconsole.log('hi');\n"
	stamp2 := ReadRelayExtensionStamp(legacy)
	if stamp2 == nil || stamp2.OmpVersion != "18.6.1" || stamp2.OmpBuild != nil {
		t.Fatalf("unexpected legacy stamp: %+v", stamp2)
	}

	if ReadRelayExtensionStamp("console.log('unstamped');") != nil {
		t.Fatal("expected nil for unstamped script")
	}

	manifest, err := StampRelayExtensionManifest(`{"name":"oh-my-pig","version":"1.0"}`, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(manifest), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["version_name"] != "0.1.0" {
		t.Fatalf("expected version_name 0.1.0, got %v", parsed["version_name"])
	}
}

func TestFromNativeHostMarshal(t *testing.T) {
	cases := []struct {
		msg  FromNativeHost
		want string
	}{
		{FromNativeHost{T: "linked"}, `{"t":"linked"}`},
		{FromNativeHost{T: "unlinked", Reason: "bye"}, `{"t":"unlinked","reason":"bye"}`},
		{FromNativeHost{T: "exit", Code: nil, Stderr: "nope"}, `{"t":"exit","code":null,"stderr":"nope"}`},
		{FromNativeHost{T: "relay", Line: "abc"}, `{"t":"relay","line":"abc"}`},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.msg)
		if err != nil {
			t.Fatalf("marshal %+v: %v", tc.msg, err)
		}
		if string(b) != tc.want {
			t.Fatalf("got %s, want %s", string(b), tc.want)
		}
	}
}

func TestLineFramesChunkingUTF8(t *testing.T) {
	// Small line fits in one relay frame.
	small := LineFrames("relay", `{"t":"reload"}`, 1)
	if len(small) != 1 || small[0].T != "relay" {
		t.Fatalf("expected 1 relay frame, got %+v", small)
	}

	// Oversized multi-byte UTF-8 line must split into valid UTF-8 chunks under NativeFrameLimit.
	big := strings.Repeat("é", 600_000) // 1.2 MiB UTF-8
	frames := LineFrames("relay", big, 7)
	if len(frames) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(frames))
	}
	var reassembled strings.Builder
	for i, f := range frames {
		if f.T != "chunk" || !f.Relay || f.ID != 7 {
			t.Fatalf("frame %d unexpected metadata: %+v", i, f)
		}
		if f.Index == nil || *f.Index != i {
			t.Fatalf("frame %d bad index: %+v", i, f.Index)
		}
		wantLast := i == len(frames)-1
		if f.Last == nil || *f.Last != wantLast {
			t.Fatalf("frame %d bad last: %+v", i, f.Last)
		}
		encoded, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > NativeFrameLimit {
			t.Fatalf("frame %d encoded len %d exceeds NativeFrameLimit %d", i, len(encoded), NativeFrameLimit)
		}
		reassembled.WriteString(f.Data)
	}
	if reassembled.String() != big {
		t.Fatalf("reassembled len %d != original %d", reassembled.Len(), len(big))
	}
}
