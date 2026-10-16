package mpvfake

import (
	"encoding/json"
	"strconv"
)

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

func commandText(cmd []json.RawMessage) string {
	data, _ := json.Marshal(cmd)
	return string(data)
}

// namedOrder is where mpv puts each named argument of the commands the fake knows in named form ({"name": "loadfile",
// "url": ...}): the positional order of mpv 0.38, whose loadfile has an index before its options.
var namedOrder = map[string][]string{"loadfile": {"url", "flags", "index", "options"}}

// commandArgs is a command in positional form, whether it was sent as an array or with named arguments (nil for neither).
func commandArgs(raw json.RawMessage) []json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var named map[string]json.RawMessage
	if json.Unmarshal(raw, &named) != nil || named["name"] == nil {
		return nil
	}
	list = []json.RawMessage{named["name"]}
	var name string
	_ = json.Unmarshal(named["name"], &name)
	for _, arg := range namedOrder[name] {
		v := named[arg]
		if v == nil {
			v = json.RawMessage(`""`)
		}
		list = append(list, v)
	}
	return list
}
