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
