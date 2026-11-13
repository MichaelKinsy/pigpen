package config

import (
	"fmt"
	"sort"
	"strings"
)

// Problem names one rejected compiled-configuration field and why.
type Problem struct {
	Field  string
	Reason string
}

// UnsupportedError lists every compiled field this harness cannot honour.
// Startup fails with the whole list so one deployment round trip fixes them all.
type UnsupportedError struct{ Problems []Problem }

func (e *UnsupportedError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, problem.Field+": "+problem.Reason)
	}
	return "unsupported configuration for the PiG harness: " + strings.Join(parts, "; ")
}

// Fields returns the rejected field names in sorted order.
func (e *UnsupportedError) Fields() []string {
	fields := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		fields = append(fields, problem.Field)
	}
	sort.Strings(fields)
	return fields
}

// collector accumulates rejections and warnings while a configuration compiles.
type collector struct {
	problems []Problem
	warnings []string
}

func (c *collector) reject(field, format string, args ...any) {
	c.problems = append(c.problems, Problem{Field: field, Reason: fmt.Sprintf(format, args...)})
}

func (c *collector) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

func (c *collector) err() error {
	if len(c.problems) == 0 {
		return nil
	}
	return &UnsupportedError{Problems: c.problems}
}
