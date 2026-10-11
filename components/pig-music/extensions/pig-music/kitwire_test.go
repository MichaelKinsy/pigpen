package pig_music_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The kit views pig-music sends, as the fake host reads them off the wire (the SDK's own encoding). renderView is the test's
// stand-in for PiG's renderer, for the node kinds pig-music sends: a container's children one after another, a lines node's
// rows as they are, a truncated text padded to the width, a text with its padding, an hstack's children side by side at their
// basis. PiG's own renderer is checked against the screens' rows by kitparity (kitparity/run.sh).

type wireView struct {
	Root   wireNode          `json:"root"`
	Theme  map[string]string `json:"theme"`
	Focus  string            `json:"focus"`
	Images []struct {
		Ref      string `json:"ref"`
		MimeType string `json:"mimeType"`
		Data     []byte `json:"data"`
	} `json:"images"`
}

type wireNode struct {
	Kind     string     `json:"kind"`
	Children []wireNode `json:"children"`
	Stack    *struct {
		Basis  *int `json:"basis"`
		Grow   *int `json:"grow"`
		Shrink *int `json:"shrink"`
	} `json:"stack"`
	Text     string   `json:"text"`
	PaddingX *int     `json:"paddingX"`
	PaddingY *int     `json:"paddingY"`
	Content  []string `json:"content"`
	Image    *struct {
		Ref string `json:"ref"`
	} `json:"image"`
	Progress *struct {
		Value float64 `json:"value"`
		Max   float64 `json:"max"`
	} `json:"progress"`
	List *struct {
		Items []struct {
			Label   string   `json:"label"`
			Detail  string   `json:"detail"`
			Columns []string `json:"columns"`
		} `json:"items"`
		SelectedIndex int `json:"selectedIndex"`
	} `json:"list"`
}

func decodeView(t testing.TB, raw json.RawMessage) wireView {
	t.Helper()
	var v wireView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode view %s: %v", raw, err)
	}
	return v
}

// mustRenderView is the rows of a view frame at width, as the test renderer draws them.
func mustRenderView(t testing.TB, raw json.RawMessage, width int) []string {
	t.Helper()
	v := decodeView(t, raw)
	rows, err := renderNode(v.Root, width)
	if err != nil {
		t.Errorf("render view: %v", err)
	}
	return rows
}

// mustWidgetLines is the string list a widget view draws as: the text of its text nodes (each a Text(line, 1, 0), as Pi lays
// out a string-list widget).
func mustWidgetLines(t testing.TB, raw json.RawMessage) []string {
	t.Helper()
	var out []string
	var walk func(n wireNode)
	walk = func(n wireNode) {
		if n.Kind == "text" {
			out = append(out, n.Text)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(decodeView(t, raw).Root)
	return out
}

func intOr(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func renderNode(n wireNode, width int) ([]string, error) {
	switch n.Kind {
	case "container":
		var out []string
		for _, c := range n.Children {
			rows, err := renderNode(c, width)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		return out, nil
	case "lines":
		return n.Content, nil
	case "truncated-text":
		if intOr(n.PaddingX, 0) != 0 || intOr(n.PaddingY, 0) != 0 {
			return nil, fmt.Errorf("truncated-text with padding")
		}
		w := ansi.StringWidth(n.Text)
		if w > width {
			return nil, fmt.Errorf("truncated-text of %d cells at %d: %q", w, width, n.Text)
		}
		return []string{n.Text + strings.Repeat(" ", width-w)}, nil
	case "text":
		px := intOr(n.PaddingX, 1)
		w := ansi.StringWidth(n.Text) + 2*px
		if w > width || intOr(n.PaddingY, 1) != 0 {
			return nil, fmt.Errorf("text the test renderer does not lay out: %q", n.Text)
		}
		return []string{strings.Repeat(" ", px) + n.Text + strings.Repeat(" ", width-w+px)}, nil
	case "hstack":
		var cols [][]string
		height := 0
		for _, c := range n.Children {
			if c.Stack == nil || c.Stack.Basis == nil {
				return nil, fmt.Errorf("hstack child without a basis")
			}
			rows, err := renderNode(c, *c.Stack.Basis)
			if err != nil {
				return nil, err
			}
			cols = append(cols, rows)
			height = max(height, len(rows))
		}
		out := make([]string, height)
		for i := range out {
			var b strings.Builder
			for _, c := range cols {
				if i < len(c) {
					// As PiG's hstack composites a node into its row: a reset (SGR and hyperlink) on each side.
					b.WriteString(segmentReset + c[i] + segmentReset)
				}
			}
			out[i] = b.String()
		}
		return out, nil
	}
	return nil, fmt.Errorf("kind %q", n.Kind)
}

// walkNodes calls f for n and every node below it.
func walkNodes(n wireNode, f func(wireNode)) {
	f(n)
	for _, c := range n.Children {
		walkNodes(c, f)
	}
}

// segmentReset is the reset PiG's hstack puts on each side of a node's part of a row (tui/widthx.SegmentReset).
const segmentReset = "\x1b[0m\x1b]8;;\x07"
