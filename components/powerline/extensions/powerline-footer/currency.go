package powerline_footer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cost currency display. upstream: currency-rates.ts. Rates come from the cache file or a refresh; USD needs neither.

var supportedCurrencies = []string{"USD", "CNY", "EUR", "GBP", "JPY", "CAD", "AUD", "CHF", "INR", "KRW"}

var currencySymbols = map[string]string{"USD": "$", "CNY": "¥", "EUR": "€", "GBP": "£", "JPY": "¥", "CAD": "CA$", "AUD": "A$", "CHF": "CHF ", "INR": "₹", "KRW": "₩"}

const (
	rateTTLMs = 24 * 60 * 60 * 1000
	rateURL   = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/usd.min.json"
)

type cachedRates struct {
	timestamp int64
	rates     map[string]float64
}

var rates struct {
	sync.Mutex
	cached   *cachedRates
	fetching bool
}

// fetchRates downloads the rates; nil disables the refresh (the tests do).
var fetchRates = fetchLatestRates

func rateCachePath() string { return getAgentPath("powerline-footer", "currency-rates.json") }

// normalizeCostCurrency returns the upper-cased code of a supported currency, or "".
func normalizeCostCurrency(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	code := strings.ToUpper(jsTrim(s))
	for _, c := range supportedCurrencies {
		if c == code {
			return code
		}
	}
	return ""
}

func setCurrencyRatesForTest(r map[string]float64) {
	rates.Lock()
	defer rates.Unlock()
	rates.cached = &cachedRates{timestamp: clock(), rates: map[string]float64{"USD": 1}}
	for k, v := range r {
		rates.cached.rates[k] = v
	}
	rates.fetching = false
}

func resetCurrencyRatesForTest() {
	rates.Lock()
	defer rates.Unlock()
	rates.cached, rates.fetching = nil, false
}

// parseCachedRates reads the cache file: {timestamp, rates} with rates keyed by lower- or upper-case code.
func parseCachedRates(v any) *cachedRates {
	o := asObject(v)
	if o == nil {
		return nil
	}
	ts, ok := o.vals["timestamp"].(float64)
	ro := asObject(o.vals["rates"])
	if !ok || ro == nil {
		return nil
	}
	out := &cachedRates{timestamp: int64(ts), rates: map[string]float64{"USD": 1}}
	for _, c := range supportedCurrencies {
		r, ok := ro.vals[strings.ToLower(c)].(float64)
		if !ok {
			r, ok = ro.vals[c].(float64)
		}
		if ok && finite(r) && r > 0 {
			out.rates[c] = r
		}
	}
	return out
}

func readRatesFromDisk() *cachedRates {
	data, err := os.ReadFile(rateCachePath())
	if err != nil {
		return nil
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil
	}
	return parseCachedRates(v)
}

// fetchLatestRates asks the currency API for the USD rates of the supported currencies.
func fetchLatestRates() (map[string]float64, error) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rateURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("currency rate fetch failed with HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	usd := asObject(asObject(v).vals["usd"])
	if usd == nil {
		return nil, errors.New("currency rate response did not include USD rates")
	}
	out := map[string]float64{"USD": 1}
	for _, c := range supportedCurrencies {
		if r, ok := usd.vals[strings.ToLower(c)].(float64); ok && c != "USD" && finite(r) && r > 0 {
			out[c] = r
		}
	}
	return out, nil
}

// ensureRatesRefreshing loads the cache file and, when it is missing or a day old, refreshes it in the background; the next
// render that asks sees the new rates.
func ensureRatesRefreshing() {
	if rates.cached == nil {
		rates.cached = readRatesFromDisk()
	}
	if rates.cached != nil && clock()-rates.cached.timestamp < rateTTLMs {
		return
	}
	if rates.fetching || fetchRates == nil {
		return
	}
	rates.fetching = true
	go func() {
		fetched, err := fetchRates()
		rates.Lock()
		defer rates.Unlock()
		rates.fetching = false
		if err != nil {
			return
		}
		rates.cached = &cachedRates{timestamp: clock(), rates: fetched}
		if data, err := json.Marshal(map[string]any{"timestamp": rates.cached.timestamp, "rates": fetched}); err == nil {
			if os.MkdirAll(filepath.Dir(rateCachePath()), 0o755) == nil {
				_ = os.WriteFile(rateCachePath(), data, 0o644)
			}
		}
	}()
}

func getRate(currency string) (float64, bool) {
	if currency == "USD" {
		return 1, true
	}
	rates.Lock()
	defer rates.Unlock()
	ensureRatesRefreshing()
	if rates.cached == nil {
		return 0, false
	}
	r, ok := rates.cached.rates[currency]
	return r, ok && r > 0
}

// formatUsdCost converts a USD amount; a currency without a known rate reads "-- CODE".
func formatUsdCost(amountUsd float64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	rate, ok := getRate(currency)
	if !ok {
		return "-- " + currency
	}
	decimals := 2
	if currency == "JPY" || currency == "KRW" {
		decimals = 0
	}
	return currencySymbols[currency] + jsToFixed(amountUsd*rate, decimals)
}
