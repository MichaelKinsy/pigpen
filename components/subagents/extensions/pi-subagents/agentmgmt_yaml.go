package pi_subagents

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAML values and the JavaScript-shaped values the original handles (the `yaml` package's parse and stringify).

// parseYAML parses one YAML document into *jsObject, []any, float64, bool, string or nil. An empty document is nil.
func parseYAML(src string) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		return nil, nil
	}
	return yamlNodeToJS(&doc, 0)
}

func yamlNodeToJS(n *yaml.Node, depth int) (any, error) {
	if depth > 200 {
		return nil, fmt.Errorf("YAML nesting is too deep")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlNodeToJS(n.Content[0], depth+1)
	case yaml.AliasNode:
		return yamlNodeToJS(n.Alias, depth+1)
	case yaml.MappingNode:
		o := newObject()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, err := yamlNodeToJS(n.Content[i], depth+1)
			if err != nil {
				return nil, err
			}
			v, err := yamlNodeToJS(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			o.set(yamlKeyString(k), v)
		}
		return o, nil
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := yamlNodeToJS(c, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			b, err := strconv.ParseBool(strings.ToLower(n.Value))
			if err != nil {
				return n.Value, nil
			}
			return b, nil
		case "!!int", "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return n.Value, nil
			}
			return f, nil
		}
		return n.Value, nil
	}
	return nil, nil
}

func yamlKeyString(k any) string {
	switch x := k.(type) {
	case string:
		return x
	case float64:
		return jsNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	}
	return fmt.Sprint(k)
}

// stringifyYAML is the `yaml` package's stringify for plain data, two-space indent, trailing newline.
func stringifyYAML(v any) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(jsToYAMLNode(v))
	_ = enc.Close()
	return buf.String()
}

func jsToYAMLNode(v any) *yaml.Node {
	switch x := v.(type) {
	case *jsObject:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range x.order() {
			if _, u := x.vals[k].(undef); u {
				continue
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, jsToYAMLNode(x.vals[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range x {
			n.Content = append(n.Content, jsToYAMLNode(e))
		}
		return n
	case []string:
		return jsToYAMLNode(anyStrings(x))
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: jsNumber(x)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}

// jsToNumber is JavaScript's Number(string) for the forms a frontmatter number takes.
func jsToNumber(s string) float64 {
	t := jsTrim(s)
	if t == "" {
		return 0
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "0o") || strings.HasPrefix(lower, "0b") {
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[lower[1]]
		n, err := strconv.ParseInt(lower[2:], base, 64)
		if err != nil {
			return math.NaN()
		}
		return float64(n)
	}
	for _, r := range t {
		if !(r >= '0' && r <= '9') && r != '.' && r != '-' && r != '+' && r != 'e' && r != 'E' {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

func isIntegerNum(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) }
