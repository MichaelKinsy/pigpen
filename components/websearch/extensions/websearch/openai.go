package websearch

import (
	"context"
	"errors"
	"time"
)

// Stub of openai-search.ts: the twins in openai_*_test.go fail until the port lands.

var openAISearchTimeout = 60 * time.Second

var errModelProviderNotPorted = errors.New("not implemented")

func SearchWithOpenAI(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errModelProviderNotPorted
}

func SearchWithCurrentModelOpenAI(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errModelProviderNotPorted
}

func IsOpenAISearchAvailable(ExtensionContext) (bool, error) { return false, errModelProviderNotPorted }
