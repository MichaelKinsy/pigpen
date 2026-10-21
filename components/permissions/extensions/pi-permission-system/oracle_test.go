package pi_permission_system

import (
	"strings"
	"testing"
)

func TestDetectPermissiveBashFallbackTwins(t *testing.T) {
	const f = "config/detect-permissive-bash-fallback"
	tw(t, f, "warns when top-level '*' is allow and bash is absent", func(t *testing.T) {
		issue := detectPermissiveBashFallback(jo(t, `{"*":"allow"}`))
		eq(t, issue != "", true, "defined")
		eq(t, strings.Contains(issue, "bash"), true, "names bash")
		eq(t, strings.Contains(issue, "allow"), true, "names allow")
	})
	tw(t, f, "warns when top-level '*' is allow and bash map has no '*' key", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":{"git *":"ask"}}`)) != "", true, "defined")
	})
	tw(t, f, "does not warn when bash is a bare string surface", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":"ask"}`)), "", "undefined")
	})
	tw(t, f, "does not warn when bash map has an explicit '*' key", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":{"*":"ask","git *":"allow"}}`)), "", "undefined")
	})
	tw(t, f, "does not warn when top-level '*' is not allow", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"ask"}`)), "", "undefined")
	})
	tw(t, f, "does not warn when top-level '*' is absent", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"bash":{"git *":"ask"}}`)), "", "undefined")
	})
	tw(t, f, "does not warn when permission is undefined", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(nil), "", "undefined")
	})
}
