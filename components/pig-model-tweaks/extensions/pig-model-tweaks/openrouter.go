package pigmodeltweaks

import (
	"strings"
)

// OpenRouter serves one model id from several upstream providers with different
// speed, quality and price. A lock pins a model to the provider the user chose:
//
//	"deepseek/deepseek-v4.1-flash" -> "deepseek"
//
// The lock is expressed two ways. It is appended to the model id that goes into
// PiG's settings.json as `base:provider`, which PiG's resolver does not
// understand, so this extension restores the base id itself at session start. At
// request time the suffix is stripped again and the lock becomes OpenRouter's
// `provider.order`, which is what actually routes the request.

const (
	openRouterProvider = "openrouter"
	providerSeparator  = ":"
)

// makeVariantID appends the locked provider to a base model id.
func makeVariantID(baseID, provider string) string {
	return baseID + providerSeparator + provider
}

// splitVariant splits a `model:provider` id. It returns false when there is no
// usable suffix, so a model id that legitimately ends in a colon survives.
func splitVariant(id string) (baseID, provider string, ok bool) {
	index := strings.LastIndex(id, providerSeparator)
	if index <= 0 || index == len(id)-1 {
		return "", "", false
	}
	return id[:index], id[index+1:], true
}

// lockFor returns the active lock for a base model id. It returns nothing while
// the feature is disabled, so consumers stop appending, stripping and routing
// at the same moment the user turns it off.
func lockFor(state *settings, baseID string) string {
	if !state.OpenRouterModelProviderPref.Enabled {
		return ""
	}
	return state.OpenRouterModelProviderPref.Locks[baseID]
}

// baseIDOf strips this extension's suffix, and only when it matches a stored
// lock, so a suffix that belongs to the model catalog is left alone.
func baseIDOf(state *settings, id string) string {
	baseID, provider, ok := splitVariant(id)
	if !ok || lockFor(state, baseID) != provider {
		return id
	}
	return baseID
}

// setLock stores or replaces a model's locked provider. An empty provider is
// refused: clearing is an explicit action, not a side effect of typing nothing.
func setLock(state *settings, baseID, provider string) {
	provider = strings.TrimSpace(provider)
	if baseID == "" || provider == "" {
		return
	}
	state.OpenRouterModelProviderPref.Locks[baseID] = provider
}

// clearLock removes a model's lock.
func clearLock(state *settings, baseID string) {
	delete(state.OpenRouterModelProviderPref.Locks, baseID)
}

// lockCount reports how many models are pinned, for status messages.
func lockCount(state *settings) int {
	return len(state.OpenRouterModelProviderPref.Locks)
}
