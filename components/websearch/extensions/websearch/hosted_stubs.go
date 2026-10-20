package websearch

import (
	"context"
	"errors"
)

// Compilable stubs of the hosted providers; the next commit replaces them with the ports.

var errHostedStub = errors.New("not implemented")

func IsTinyFishAvailable() bool       { return false }
func IsSearch1APIAvailable() bool     { return false }
func IsSearchinfinityAvailable() bool { return false }
func IsQueritAvailable() bool         { return false }
func IsParallelAvailable() bool       { return false }
func IsBrightDataAvailable() bool     { return false }

func IsBrightDataUnlockerAvailable() bool { return false }

func SearchWithTinyFish(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithSearch1API(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithSearchinfinity(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithQuerit(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithParallel(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithParallelMCP(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithOllama(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}
func SearchWithBrightData(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errHostedStub
}

func ExtractWithTinyFish(context.Context, string, ExtractOptions) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithSearch1API(context.Context, string, ExtractOptions) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithQuerit(context.Context, string, ExtractOptions) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithParallel(context.Context, string, ExtractOptions) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithFirecrawl(context.Context, string, ExtractOptions, *SsrfConfig) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithCrawl4ai(context.Context, string, ExtractOptions, *SsrfConfig) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithOllama(context.Context, string, ExtractOptions, SsrfConfig) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithKagi(context.Context, string, ExtractOptions, SsrfConfig) (*ExtractedContent, error) {
	return nil, errHostedStub
}
func ExtractWithBrightDataUnlocker(context.Context, string, ExtractOptions, SsrfConfig) (*ExtractedContent, error) {
	return nil, errHostedStub
}
