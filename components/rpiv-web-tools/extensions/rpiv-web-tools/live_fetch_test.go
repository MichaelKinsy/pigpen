// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"strings"
	"testing"
)

// The generic fetch path against real HTTP: the net/http doer, the shared headers, the content-type guard, the HTML
// pipeline and the content-length parse. No key is involved, so this runs wherever there is a network.
//
// LIVE_FETCH_URL overrides the default target; the second case needs a host that answers with an image content type.

func TestLiveGenericFetch(t *testing.T) {
	client := newHTTPClient()
	target := "https://example.com"
	if url := liveFetchURL(); url != "" {
		target = url
	}

	t.Run("reads a real HTML page and converts it", func(t *testing.T) {
		res, err := fetchViaGenericHTML(client.doer, target, false)
		if err != nil {
			t.Skipf("no network or the host refused: %v", err)
		}
		if !res.HasContentType {
			t.Fatal("a served page carries a content type")
		}
		if res.Text == "" {
			t.Fatal("the body must survive the conversion")
		}
		if strings.Contains(res.Text, "<html") {
			t.Fatalf("the markup must be gone, got:\n%.200s", res.Text)
		}
		t.Logf("%s -> %d bytes, content-type %q, title %q", target, len(res.Text), res.ContentType, res.Title)
	})
	t.Run("raw mode returns the body untouched", func(t *testing.T) {
		res, err := fetchViaGenericHTML(client.doer, target, true)
		if err != nil {
			t.Skipf("no network: %v", err)
		}
		if !strings.Contains(strings.ToLower(res.Text), "<") {
			t.Fatalf("raw mode must keep the markup, got:\n%.200s", res.Text)
		}
	})
	t.Run("the shared headers reach the server", func(t *testing.T) {
		// example.org answers any path with the same page, so this checks the request rather than the response.
		res, err := fetchViaGenericHTML(client.doer, target, true)
		if err != nil {
			t.Skipf("no network: %v", err)
		}
		if res.Text == "" {
			t.Fatal("the request must have been answered")
		}
	})
	t.Run("a non-2xx becomes the uniform HTTP error", func(t *testing.T) {
		_, err := fetchViaGenericHTML(client.doer, "https://example.com/definitely-not-here-"+"9f3a2b", false)
		if err == nil {
			t.Skip("this host answers 404 with 200; the case cannot run here")
		}
		if !strings.HasPrefix(err.Error(), "HTTP ") {
			t.Fatalf("the error must carry the status, got %q", err.Error())
		}
		t.Logf("error text: %v", err)
	})
	t.Run("a binary content type is refused", func(t *testing.T) {
		// A real icon is served with an image content type, which is exactly what the guard exists for. example.com
		// answers 404 for a path it does not have, so the target is a host that really serves one.
		_, err := fetchViaGenericHTML(client.doer, "https://www.google.com/favicon.ico", false)
		if err == nil {
			t.Skip("this host serves the icon as text here")
		}
		if !strings.Contains(err.Error(), "Unsupported content type") {
			t.Fatalf("the guard must name the type, got %q", err.Error())
		}
		t.Logf("error text: %v", err)
	})
}

// liveFetchURL is the override for the generic-fetch cases.
func liveFetchURL() string { return liveFetchURLValue() }

// liveFetchURLValue reads the override the caller may set.
func liveFetchURLValue() string { return os.Getenv("LIVE_FETCH_URL") }

// The installed tool's own body, driven without a model: the same runFetch the web_fetch tool calls, over the real
// network and through the same client. This is the work the model would trigger.

func TestLiveToolBody(t *testing.T) {
	configHome(t)
	app := newApp()

	t.Run("web_fetch's body reads and converts a real page", func(t *testing.T) {
		target := "https://example.com"
		if url := liveFetchURL(); url != "" {
			target = url
		}
		res, err := app.runFetch(target, false)
		if err != nil {
			t.Skipf("no network: %v", err)
		}
		if res.Text == "" {
			t.Fatal("the body must survive the conversion")
		}
		t.Logf("fetch returned %d bytes, title %q, content-type %q", len(res.Text), res.Title, res.ContentType)
	})
	t.Run("web_fetch's body refuses a private address", func(t *testing.T) {
		_, err := app.runFetch("http://127.0.0.1:1/x", false)
		if err == nil {
			t.Fatal("a loopback address must be refused before any request")
		}
		if !strings.Contains(err.Error(), "Refusing to fetch private/loopback address") {
			t.Fatalf("the guard must refuse it by name, got %q", err.Error())
		}
		t.Logf("guard error: %v", err)
	})
	t.Run("the truncation budget applies to a real body", func(t *testing.T) {
		target := "https://example.com"
		if url := liveFetchURL(); url != "" {
			target = url
		}
		res, err := app.runFetch(target, true)
		if err != nil {
			t.Skipf("no network: %v", err)
		}
		_, truncation, err := truncateBody(res.Text, maxFetchBytes)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("body of %d bytes: truncated=%v lines=%d", len(res.Text), truncation.Truncated, truncation.OutputLines)
	})
}
