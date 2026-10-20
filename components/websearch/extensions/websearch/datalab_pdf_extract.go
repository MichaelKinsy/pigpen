package websearch

import (
	"context"
	"time"
)

// Stubs of the Datalab PDF conversion: the red state of the twins.

// DatalabPDFOptions are DatalabPDFExtractOptions upstream.
type DatalabPDFOptions struct {
	MaxPages           int
	Title              string
	Timeout            time.Duration
	Mode               string
	ProcessingLocation string
}

// DatalabPDFResult is DatalabPDFExtractResult upstream.
type DatalabPDFResult struct {
	Markdown          string
	Pages             int
	ParseQualityScore *float64
}

// ExtractPDFViaDatalab converts a PDF to Markdown with the Datalab API.
func ExtractPDFViaDatalab(context.Context, []byte, DatalabPDFOptions) (DatalabPDFResult, error) {
	return DatalabPDFResult{}, unsupported("PDF text extraction (Datalab provider)")
}

// DatalabAPIBase is getDatalabApiBase.
func DatalabAPIBase() string { return "" }

func datalabFilename(string) string { return "" }
