package websearch

import "errors"

// BrowserCookieSelection is the browserCookies section of web-search.json.
type BrowserCookieSelection struct {
	Browser string
	Profile string
}

var errGeminiWebConfigStub = errors.New("the Gemini Web configuration is not implemented")

// BrowserCookieSelectionFromConfig is getBrowserCookieSelectionFromConfig.
func BrowserCookieSelectionFromConfig() (BrowserCookieSelection, error) {
	return BrowserCookieSelection{}, errGeminiWebConfigStub
}

// IsBrowserCookieAccessAllowed is isBrowserCookieAccessAllowed.
func IsBrowserCookieAccessAllowed() (bool, error) { return false, errGeminiWebConfigStub }

func resetGeminiWebConfig() {}
