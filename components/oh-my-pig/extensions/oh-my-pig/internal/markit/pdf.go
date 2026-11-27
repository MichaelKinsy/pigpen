package markit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	pdfExtensions = []string{".pdf"}
	pdfMIMETypes  = []string{"application/pdf", "application/x-pdf"}
)

const (
	// DefaultPDFTimeout bounds a single `pdftotext` subprocess invocation.
	DefaultPDFTimeout = 30 * time.Second
	// MaxPDFStderrBytes caps captured stderr from `pdftotext`.
	MaxPDFStderrBytes = 8 * 1024
)

// ErrPdftotextNotFound is returned when PDF conversion is requested but the
// external `pdftotext` CLI is not installed on PATH.
var ErrPdftotextNotFound = errors.New("pdftotext is not installed on PATH; install poppler-utils (or poppler on macOS) to extract text from PDF files")

// Overridable in tests for deterministic CLI refusal / timeout / output-cap tests.
var (
	pdfLookPath       = exec.LookPath
	pdfTimeout        = DefaultPDFTimeout
	pdfMaxStdoutBytes = MaxInMemorySize
)

// boundedWriter captures up to limit bytes and sets overflow=true (cancelling
// cancel if provided) when more bytes are written.
type boundedWriter struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.buf.Len()
	if rem > 0 {
		take := min(len(p), rem)
		w.buf.Write(p[:take])
	}
	if len(p) > rem {
		w.overflow = true
		if w.cancel != nil {
			w.cancel()
		}
	}
	// Report full write to avoid SIGPIPE masking overflow vs exit status;
	// cancel terminates the subprocess promptly when configured.
	return len(p), nil
}

// PdfConverter converts PDF files to text using the `pdftotext` CLI when available,
// and returns an explicit error when `pdftotext` is not installed.
// upstream: packages/coding-agent/src/markit/converters/pdf/index.ts:1-39
// Note: upstream uses the Rust `pdf-inspector` native crate; this Go stdlib port
// delegates to `pdftotext` with bounded runtime and output caps, and never fabricates output.
type PdfConverter struct{}

func (p *PdfConverter) Name() string { return "pdf" }

func (p *PdfConverter) Accepts(info StreamInfo) bool {
	return matchesExtensionOrMIME(info, pdfExtensions, pdfMIMETypes)
}

func (p *PdfConverter) Convert(ctx context.Context, input []byte, info StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	bin, err := pdfLookPath("pdftotext")
	if err != nil {
		return Result{}, ErrPdftotextNotFound
	}

	runCtx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()

	// Prefer passing LocalPath directly when available so we avoid piping up to
	// 256 MiB over stdin (and stay compatible with both poppler and Xpdf pdftotext).
	var cmd *exec.Cmd
	if info.LocalPath != "" {
		cmd = exec.CommandContext(runCtx, bin, "-enc", "UTF-8", info.LocalPath, "-")
	} else if len(input) > 0 {
		cmd = exec.CommandContext(runCtx, bin, "-enc", "UTF-8", "-", "-")
		cmd.Stdin = bytes.NewReader(input)
	} else {
		return Result{}, fmt.Errorf("empty PDF input")
	}

	stdout := &boundedWriter{limit: pdfMaxStdoutBytes, cancel: cancel}
	stderr := &boundedWriter{limit: MaxPDFStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if stdout.overflow {
		return Result{}, fmt.Errorf("pdftotext output exceeds %d bytes limit", pdfMaxStdoutBytes)
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("pdftotext timed out after %s", pdfTimeout)
		}
		errMsg := strings.TrimSpace(stderr.buf.String())
		if errMsg != "" {
			return Result{}, fmt.Errorf("pdftotext failed: %s", errMsg)
		}
		return Result{}, fmt.Errorf("pdftotext failed: %w", runErr)
	}

	// Normalize form-feed page breaks to blank lines and trim surrounding whitespace.
	text := strings.ReplaceAll(stdout.buf.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\f", "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, fmt.Errorf("PDF contains no extractable text (scanned or image-only PDF)")
	}
	return Result{Markdown: text}, nil
}
