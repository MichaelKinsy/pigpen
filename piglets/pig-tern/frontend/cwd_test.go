package frontend

import (
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// The footer's "~"-abbreviated directory reaches Tern as an absolute file
// URL in OSC 7, once per change and outside any TSP message, and a tool's
// path target links to its file resolved against that directory.
func TestCwdReportAndToolPathLinks(t *testing.T) {
	h := readyEnv(t, map[string]string{"HOME": "/home/u"})
	h.session.hostname = "box"
	h.out.take()

	footer := fe.Footer{Cwd: "~/src/pig 100%", Model: "m"}
	h.apply(footerOp(fe.Insert, footer))
	want := "\x1b]7;file://box/home/u/src/pig%20100%25\x07"
	out := h.out.take()
	if strings.Count(out, "\x1b]7;") != 1 || !strings.Contains(out, want) {
		t.Fatalf("first footer wrote %q, want one %q", out, want)
	}
	for _, part := range strings.Split(out, "\x1b_tsp;")[1:] {
		if body, _, _ := strings.Cut(part, "\x1b\\"); strings.Contains(body, "\x1b]7;") {
			t.Fatalf("OSC 7 inside a TSP message: %q", part)
		}
	}

	footer.Model = "n"
	h.apply(footerOp(fe.Update, footer))
	if out := h.out.take(); strings.Contains(out, "\x1b]7;") {
		t.Fatalf("an unchanged directory was reported again: %q", out)
	}

	footer.Cwd = "/srv/app"
	h.apply(footerOp(fe.Update, footer))
	if out := h.out.take(); !strings.Contains(out, "\x1b]7;file://box/srv/app\x07") {
		t.Fatalf("changed directory wrote %q", out)
	}

	for _, tc := range []struct{ path, href string }{
		{"src/a.go", "/srv/app/src/a.go"},
		{"/etc/hosts", "/etc/hosts"},
		{"~/notes 5%.md", "/home/u/notes 5%25.md"},
	} {
		node := h.session.mainNode("n.t", fe.ToolCard{Name: "read", Status: fe.ToolDone, Arguments: map[string]any{"path": tc.path}})
		if node.P["href"] != tc.href {
			t.Fatalf("read %q href = %#v, want %q", tc.path, node.P["href"], tc.href)
		}
	}
	bash := h.session.mainNode("n.b", fe.ToolCard{Name: "bash", Status: fe.ToolDone, Arguments: map[string]any{"command": "ls"}})
	if _, ok := bash.P["href"]; ok {
		t.Fatalf("a command target got a link: %#v", bash.P)
	}
}

// Without a known absolute directory, a relative path gets no link and
// nothing is reported.
func TestCwdUnknownWritesNothing(t *testing.T) {
	h := ready(t)
	h.out.take()
	h.apply(footerOp(fe.Insert, fe.Footer{Cwd: "~/src", Model: "m"}))
	if out := h.out.take(); strings.Contains(out, "\x1b]7;") {
		t.Fatalf("reported without HOME: %q", out)
	}
	node := h.session.mainNode("n.t", fe.ToolCard{Name: "read", Status: fe.ToolDone, Arguments: map[string]any{"path": "a.go"}})
	if _, ok := node.P["href"]; ok {
		t.Fatalf("relative path linked without a directory: %#v", node.P)
	}
}
