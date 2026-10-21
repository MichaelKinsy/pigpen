package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Port of datalab-pdf-extract.ts: convert a PDF to Markdown through the Datalab hosted API
// (presigned upload, confirm, convert, poll). The API base is DATALAB_API_BASE (default
// https://www.datalab.to/api/v1); redirects are errors, as the original fetches with
// redirect: "error".

const (
	datalabDefaultAPIBase    = "https://www.datalab.to/api/v1"
	datalabPollInterval      = 1500 * time.Millisecond
	datalabMaxResponseBytes  = 4 * 1024 * 1024
	datalabMaxErrorBodyBytes = 300
	datalabCleanupTimeout    = 5 * time.Second
	datalabDefaultTimeout    = time.Duration(defaultDatalabTimeoutMs) * time.Millisecond
)

// DatalabPDFOptions are DatalabPDFExtractOptions upstream. Zero Timeout means the 120 s default;
// empty Mode and ProcessingLocation read DATALAB_MODE and DATALAB_PROCESSING_LOCATION.
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

// IsDatalabAPIAvailable is isDatalabApiAvailable.
func IsDatalabAPIAvailable() bool {
	return hasProviderCredential("Datalab", "datalabApiKey", "DATALAB_API_KEY")
}

// DatalabAPIBase is getDatalabApiBase.
func DatalabAPIBase() string {
	base := strings.TrimRight(jsTrim(os.Getenv("DATALAB_API_BASE")), "/")
	if base == "" {
		return datalabDefaultAPIBase
	}
	return base
}

// datalabProcessingLocation is getDatalabProcessingLocation; a bad value is a "Failed to parse"
// error, which the extraction chain treats as fatal.
func datalabProcessingLocation() (string, error) {
	value := jsTrim(os.Getenv("DATALAB_PROCESSING_LOCATION"))
	if value == "" {
		return "us", nil
	}
	switch strings.ToLower(value) {
	case "eu":
		return "eu", nil
	case "us":
		return "us", nil
	}
	return "", fmt.Errorf(`Failed to parse DATALAB_PROCESSING_LOCATION: expected "eu" or "us", got "%s"`, value)
}

func datalabNormalizeString(v any) string {
	s, _ := v.(string)
	return jsTrim(s)
}

func datalabFileID(v any) string {
	if f, ok := v.(float64); ok {
		return formatNumber(f)
	}
	return datalabNormalizeString(v)
}

// ExtractPDFViaDatalab converts pdf to Markdown with page markers.
func ExtractPDFViaDatalab(ctx context.Context, pdf []byte, o DatalabPDFOptions) (result DatalabPDFResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	apiKey, err := resolveProviderCredential(ctx, "Datalab", "datalabApiKey", "DATALAB_API_KEY")
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if apiKey == "" {
		return result, errors.New("Datalab PDF conversion requires a configured Datalab API key")
	}
	mode := o.Mode
	if mode == "" {
		if mode, err = normalizeDatalabMode(os.Getenv("DATALAB_MODE")); err != nil {
			return result, err
		}
	}
	location := o.ProcessingLocation
	if location == "" {
		if location, err = datalabProcessingLocation(); err != nil {
			return result, err
		}
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = datalabDefaultTimeout
	}
	c := &datalabRun{ctx: ctx, apiKey: apiKey, deadline: time.Now().Add(timeout)}

	upload, err := c.requestUploadURL(o.Title, location)
	if err != nil {
		return result, err
	}
	uploadURL, _ := upload["upload_url"].(string)
	if uploadURL == "" {
		return result, errors.New("Datalab PDF conversion failed: missing upload_url")
	}
	fileID := datalabFileID(upload["file_id"])
	if fileID == "" {
		return result, errors.New("Datalab PDF conversion failed: missing file_id")
	}
	reference := datalabNormalizeString(upload["reference"])

	// File deletion is best-effort and never extends a caller's timeout or cancellation.
	defer func() { c.deleteFile(fileID) }()

	put, err := c.fetch(uploadURL, RequestInit{Method: "PUT", Header: http.Header{"Content-Type": {"application/pdf"}}, Body: pdf})
	if err != nil {
		return result, err
	}
	put.close()
	if !put.ok() {
		return result, fmt.Errorf("Datalab PDF upload failed: HTTP %d %s", put.status, http.StatusText(put.status))
	}

	confirmed, err := c.json(DatalabAPIBase()+"/files/"+jsEncodeURIComponent(fileID)+"/confirm", RequestInit{Method: "GET", Header: http.Header{"X-Api-Key": {apiKey}}})
	if err != nil {
		return result, err
	}
	if id := datalabFileID(confirmed["file_id"]); id != "" {
		fileID = id
	}
	if ref := datalabNormalizeString(confirmed["reference"]); ref != "" {
		reference = ref
	}
	if reference == "" {
		return result, errors.New("Datalab PDF conversion failed: missing file reference")
	}

	// The convert step omits processing_location: the API rejects multipart data that carries it
	// (HTTP 403); the region was fixed when the presigned upload was issued.
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	for _, f := range [][2]string{{"file_url", reference}, {"output_format", "markdown"}, {"mode", mode}, {"max_pages", strconv.Itoa(o.MaxPages)}, {"paginate", "true"}} {
		_ = mw.WriteField(f[0], f[1])
	}
	_ = mw.Close()
	state, err := c.json(DatalabAPIBase()+"/convert", RequestInit{Method: "POST", Header: http.Header{"X-Api-Key": {apiKey}, "Content-Type": {mw.FormDataContentType()}}, Body: form.Bytes()})
	if err != nil {
		return result, err
	}

	// The submit response is an acceptance ack with no status; only the polled result has one.
	if r, done, err := datalabState(state); done || err != nil {
		return r, err
	}
	checkURL, err := datalabCheckURL(state["request_check_url"])
	if err != nil {
		return result, err
	}
	for time.Now().Before(c.deadline) {
		if err := sleepCtx(ctx, min(datalabPollInterval, c.remaining())); err != nil {
			return result, err
		}
		if !time.Now().Before(c.deadline) {
			break
		}
		next, err := c.json(checkURL, RequestInit{Method: "GET", Header: http.Header{"X-Api-Key": {apiKey}}})
		if err != nil {
			return result, err
		}
		if r, done, err := datalabState(next); done || err != nil {
			return r, err
		}
	}
	return result, errors.New("Datalab PDF conversion timed out")
}

// datalabState reads a submit or poll response: complete returns the result, failed an error.
func datalabState(state map[string]any) (DatalabPDFResult, bool, error) {
	switch state["status"] {
	case "complete":
		r, err := datalabResult(state)
		return r, true, err
	case "failed":
		return DatalabPDFResult{}, true, fmt.Errorf("Datalab PDF conversion failed: %s", jsStringOr(state["error"], "unknown error"))
	}
	return DatalabPDFResult{}, false, nil
}

// jsStringOr is `${value ?? fallback}`.
func jsStringOr(v any, fallback string) string {
	switch x := v.(type) {
	case nil:
		return fallback
	case string:
		return x
	case float64:
		return formatNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsStringOr(e, "")
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func datalabResult(state map[string]any) (DatalabPDFResult, error) {
	markdown := datalabNormalizeString(state["markdown"])
	if markdown == "" {
		return DatalabPDFResult{}, errors.New("Datalab PDF conversion returned empty markdown")
	}
	r := DatalabPDFResult{Markdown: markdown, Pages: countPageMarkers(markdown)}
	if n, ok := state["page_count"].(float64); ok && n > 0 {
		r.Pages = int(n)
	}
	if q, ok := state["parse_quality_score"].(float64); ok {
		r.ParseQualityScore = &q
	}
	return r, nil
}

func datalabCheckURL(v any) (string, error) {
	value, _ := v.(string)
	if jsTrim(value) == "" {
		return "", errors.New("Datalab PDF conversion failed: missing request_check_url")
	}
	base, err := ParseURL(DatalabAPIBase(), nil)
	if err != nil || base.Host == "" {
		return "", errors.New("Failed to parse DATALAB_API_BASE: expected an absolute URL")
	}
	check, err := ParseURL(jsTrim(value), base)
	if err != nil {
		return "", errors.New("Datalab PDF conversion failed: invalid request_check_url")
	}
	if Origin(check) != Origin(base) {
		return "", errors.New("Datalab PDF conversion failed: request_check_url has an unexpected origin")
	}
	return check.String(), nil
}

var (
	countPageMarkerRE = regexp.MustCompile(`(?m)^<!-- Page (\d+) -->$`)
)

func countPageMarkers(markdown string) int { return len(countPageMarkerRE.FindAllString(markdown, -1)) }

func datalabFilename(title string) string {
	s := strings.ToLower(title)
	s = filenameBad.ReplaceAllString(s, "")
	s = filenameSpace.ReplaceAllString(s, "-")
	s = filenameDashRun.ReplaceAllString(s, "-")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSuffix(s, "-")
	if s = jsSlice(s, 0, 60); s == "" {
		return "document"
	}
	return s
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// datalabRun is one conversion: the caller's context and the shared deadline.
type datalabRun struct {
	ctx      context.Context
	apiKey   string
	deadline time.Time
}

func (c *datalabRun) remaining() time.Duration { return max(time.Millisecond, time.Until(c.deadline)) }

type datalabResponse struct {
	status int
	resp   *http.Response
	cancel context.CancelFunc
}

func (r *datalabResponse) ok() bool { return r.status >= 200 && r.status <= 299 }
func (r *datalabResponse) close() {
	r.resp.Body.Close()
	r.cancel()
}

// fetch is fetchDatalab: one request bounded by the caller's context and the shared deadline,
// failing on a redirect and redacting the API key from errors. The caller closes the response.
func (c *datalabRun) fetch(rawURL string, init RequestInit) (*datalabResponse, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("Failed to parse URL")
	}
	ctx, cancel := withTimeout(c.ctx, c.remaining())
	resp, err := doAPI(ctx, u, init)
	if err == nil {
		switch resp.StatusCode {
		case 301, 302, 303, 307, 308:
			if resp.Header.Get("Location") != "" {
				resp.Body.Close()
				err = errors.New("fetch failed: unexpected redirect")
			}
		}
	}
	if err != nil {
		cancel()
		return nil, redactErr(timeoutFix(c.ctx, ctx, err), c.apiKey)
	}
	return &datalabResponse{status: resp.StatusCode, resp: resp, cancel: cancel}, nil
}

// json is readJsonResponse over fetch.
func (c *datalabRun) json(rawURL string, init RequestInit) (map[string]any, error) {
	r, err := c.fetch(rawURL, init)
	if err != nil {
		return nil, err
	}
	defer r.close()
	text, err := datalabReadText(r.resp)
	if err != nil {
		return nil, redactErr(err, c.apiKey)
	}
	if !r.ok() {
		detail := jsSlice(jsTrim(text), 0, datalabMaxErrorBodyBytes)
		msg := fmt.Sprintf("Datalab PDF conversion failed: HTTP %d %s", r.status, http.StatusText(r.status))
		if detail != "" {
			msg += ": " + detail
		}
		return nil, errors.New(RedactCredential(msg, c.apiKey))
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, &redactedError{"Datalab PDF conversion returned invalid JSON", err}
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil, errors.New("Datalab PDF conversion returned invalid JSON object")
	}
	return obj, nil
}

func datalabReadText(resp *http.Response) (string, error) {
	if n, err := strconv.ParseFloat(resp.Header.Get("Content-Length"), 64); err == nil && n > datalabMaxResponseBytes {
		return "", errors.New("Datalab PDF conversion response too large")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, datalabMaxResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > datalabMaxResponseBytes {
		return "", errors.New("Datalab PDF conversion response too large")
	}
	return string(data), nil
}

func (c *datalabRun) requestUploadURL(title, location string) (map[string]any, error) {
	body, _ := json.Marshal(map[string]string{"filename": datalabFilename(title) + ".pdf", "content_type": "application/pdf", "processing_location": location})
	return c.json(DatalabAPIBase()+"/files/upload", RequestInit{Method: "POST", Header: http.Header{"X-Api-Key": {c.apiKey}, "Content-Type": {"application/json"}}, Body: body})
}

// deleteFile removes the uploaded file without waiting for it (deleteDatalabFile upstream).
func (c *datalabRun) deleteFile(fileID string) {
	base := context.WithoutCancel(c.ctx)
	target := DatalabAPIBase() + "/files/" + jsEncodeURIComponent(fileID)
	go func() {
		ctx, cancel := context.WithTimeout(base, datalabCleanupTimeout)
		defer cancel()
		u, err := url.Parse(target)
		if err != nil {
			return
		}
		if resp, err := doAPI(ctx, u, RequestInit{Method: "DELETE", Header: http.Header{"X-Api-Key": {c.apiKey}}}); err == nil {
			resp.Body.Close()
		}
	}()
}
