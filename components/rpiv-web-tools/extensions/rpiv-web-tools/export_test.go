package rpiv_web_tools

import "net/http"

// SetTransport replaces the HTTP transport the providers use and returns the function that restores it.
func SetTransport(rt http.RoundTripper) func() {
	prev := httpClient.Transport
	httpClient.Transport = rt
	return func() { httpClient.Transport = prev }
}
