package websearch

import (
	"strings"
	"unicode/utf8"
)

// Temporary stand-ins, replaced as the remaining ports land.

type dfdExtracted struct {
	content         string
	contentHTML     string
	contentSelector string
	variables       map[string]string
}

type dfdExtractor interface {
	canExtract() bool
	extract() dfdExtracted
	name() string
}

func dfdFindExtractor(d *dfdDefuddle, url string, schema []any) dfdExtractor { return nil }

func (d *dfdDefuddle) mergeExtractorResult(pipeline dfdResult, e dfdExtracted, ext dfdExtractor) dfdResult {
	return pipeline
}

func (d *dfdDefuddle) buildExtractorResponse(e dfdExtracted, m dfdMetadata, ext dfdExtractor) dfdResult {
	return dfdResult{}
}

func dfdStandardizeExtractorOutput(el *ldNode) {}

func dfdFilterExtractorClasses(v string) string { return "" }

func lastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

var _ = strings.ToLower
