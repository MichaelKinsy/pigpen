package pi_subagents

import (
	"strings"
	"testing"
)

const permissionsFile = "permissions"

func TestNativeChildPermissions(t *testing.T) {
	tw(t, permissionsFile, "defaults every unconfigured tool and bash to pass-through", func(t *testing.T) {
		equal(t, permissionDecision(nil, "write"), "allow", "write")
		equal(t, permissionDecision(permissionRules{"write": "deny"}, "unknown_tool"), "allow", "unknown")
		equal(t, permissionDecision(permissionRules{"write": "deny"}, "bash"), "allow", "bash")
		if resolvePermissionRules(nil, nil) != nil {
			t.Error("want undefined")
		}
	})
	tw(t, permissionsFile, "merges explicit global and agent rules while removing explicit allow rules", func(t *testing.T) {
		got := resolvePermissionRules(&permissionConfig{Rules: permissionRules{"write": "ask", "edit": "deny"}}, permissionRules{"write": "allow", "read": "deny"})
		equal(t, got, permissionRules{"edit": "deny", "read": "deny"}, "merged")
	})
	tw(t, permissionsFile, "rejects bash and coordination-tool rules", func(t *testing.T) {
		_, err := validatePermissionRules(js(t, `{"bash":"ask"}`), "permissions")
		if err == nil {
			t.Fatal("bash accepted")
		}
		polMatches(t, err.Error(), `pi-guard`)
		_, err = validatePermissionRules(js(t, `{"contact_supervisor":"deny"}`), "permissions")
		if err == nil {
			t.Fatal("contact_supervisor accepted")
		}
		polMatches(t, err.Error(), `reserved for child coordination`)
		_, err = validatePermissionConfig(js(t, `{"rules":{"write":"sometimes"}}`), "config.permissions")
		if err == nil {
			t.Fatal("sometimes accepted")
		}
		polMatches(t, err.Error(), `allow, ask, or deny`)
	})
	tw(t, permissionsFile, "redacts bounded argument previews", func(t *testing.T) {
		preview := permissionArgsPreview(js(t, marshalJSON(map[string]any{"token": "secret-value", "content": "Bearer abcdefghijklmnop " + strings.Repeat("x", 3000)}, "")))
		polNotMatches(t, preview, `secret-value|abcdefghijklmnop`)
		if len(preview) > 2048 {
			t.Errorf("preview is %d bytes", len(preview))
		}
		items := make([]any, 10)
		for i := range items {
			items[i] = strings.Repeat("😀", 300)
		}
		multibyte := permissionArgsPreview(map[string]any{"content": items})
		if len(multibyte) > 2048 {
			t.Errorf("multibyte preview is %d bytes", len(multibyte))
		}
		polNotMatches(t, multibyte, "\uFFFD")
		polMatches(t, multibyte, `…$`)
	})
}
