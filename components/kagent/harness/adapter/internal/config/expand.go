package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	envPlaceholderPrefix = "__KAGENT_ENV["
	envPlaceholderSuffix = "]__"
)

// expandEnvPlaceholders resolves the controller's __KAGENT_ENV[NAME]__ string
// values from the process environment, as kagent's own ADK runtime does before
// it reads the compiled configuration. A missing variable is an error.
func expandEnvPlaceholders(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	if !strings.Contains(string(raw), envPlaceholderPrefix) {
		return raw, nil
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode config JSON: %w", err)
	}
	var expand func(any) (any, error)
	expand = func(value any) (any, error) {
		switch value := value.(type) {
		case string:
			if strings.HasPrefix(value, envPlaceholderPrefix) && strings.HasSuffix(value, envPlaceholderSuffix) {
				name := strings.TrimSuffix(strings.TrimPrefix(value, envPlaceholderPrefix), envPlaceholderSuffix)
				resolved, ok := lookup(name)
				if !ok {
					return nil, fmt.Errorf("required environment variable %s is not set", name)
				}
				return resolved, nil
			}
		case []any:
			for i := range value {
				item, err := expand(value[i])
				if err != nil {
					return nil, err
				}
				value[i] = item
			}
		case map[string]any:
			for key, item := range value {
				expanded, err := expand(item)
				if err != nil {
					return nil, err
				}
				value[key] = expanded
			}
		}
		return value, nil
	}
	expanded, err := expand(document)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("encode config JSON: %w", err)
	}
	return out, nil
}
