// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "fmt"

// The factory's live provider: the identity half comes from the metadata table, and Search runs the ported arm for
// that provider. upstream: providers/factory.ts createSearchProvider, whose result is one of the ten classes.
//
// The two self-hosted providers are built elsewhere, because they need a base URL the factory does not resolve;
// this one covers the eight keyed providers, which need nothing but a key.

// newSearchProvider builds the provider for a name, with the client its arm runs through. The unknown-name branch is
// the factory's own uniform error, distinct from the orchestrator's because only the orchestrator knows the valid
// list. upstream: providers/factory.ts createSearchProvider.
func newSearchProvider(name string, creds providerCredentials, client httpDoer) (searchProvider, error) {
	meta, ok := providerMetaByName(name)
	if !ok {
		return nil, &unknownFactoryProviderError{Name: name}
	}
	return &liveProvider{meta: meta, creds: creds, client: client}, nil
}

// liveProvider is one provider instance: its metadata and its credentials, with the arm behind Search. upstream: the
// provider classes (BraveProvider, TavilyProvider, ...), one per vendor.
type liveProvider struct {
	meta   providerMeta
	creds  providerCredentials
	client httpDoer
}

func (p *liveProvider) Name() string   { return p.meta.Name }
func (p *liveProvider) Label() string  { return p.meta.Label }
func (p *liveProvider) EnvVar() string { return p.meta.EnvVar }

// Search runs the arm of this provider. Each branch keeps its own guard, request shape, status wrapper and
// normalisation, which is where the vendor differences live. upstream: each provider's search().
func (p *liveProvider) Search(query string, maxResults int) (searchResponse, error) {
	switch p.meta.Name {
	case "brave":
		return searchBrave(p.client, p.creds.APIKey, query, maxResults)
	case "tavily":
		return searchTavily(p.client, p.creds.APIKey, query, maxResults)
	case "serper":
		return searchSerper(p.client, p.creds.APIKey, query, maxResults)
	case "exa":
		return searchExa(p.client, p.creds.APIKey, query, maxResults)
	case "youcom":
		return searchYouCom(p.client, p.creds.APIKey, query, maxResults)
	case "jina":
		return searchJina(p.client, p.creds.APIKey, query, maxResults)
	case "firecrawl":
		return searchFirecrawl(p.client, p.creds.APIKey, query, maxResults)
	case "perplexity":
		return searchPerplexity(p.client, p.creds.APIKey, query, maxResults)
	}
	// A provider the factory knows but whose arm is not one of the eight keyed ones is built with its own base URL,
	// so reaching here means the table and the arms have drifted apart.
	return searchResponse{}, fmt.Errorf("%s: no search arm is wired for this provider", p.meta.Name)
}

// unknownFactoryProviderError is the factory's own failure for a name it does not know. It differs from
// unknownProviderError on purpose: the factory never has the valid list, the orchestrator does. upstream:
// providers/factory.ts createSearchProvider's default branch.
type unknownFactoryProviderError struct {
	Name string
}

func (e *unknownFactoryProviderError) Error() string {
	return `Unknown search provider: "` + e.Name + `"`
}
