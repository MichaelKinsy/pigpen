package changeset_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/changeset-uri.test.ts.
func TestChangesetURI(t *testing.T) {
	twin.Run(t, "changeset-uri", "builds stable static catalogue channels", func(t *testing.T) {
		got := testkit.Normalize(t, changeset.Catalogue("session-1", true))
		want := testkit.Normalize(t, []map[string]any{
			{
				"label": "Latest Commit", "description": "Changes introduced by the current HEAD commit",
				"changeKind": "branch", "uriTemplate": "ahp-changeset:/session-1/latest-commit",
			},
			{
				"label": "Uncommitted Changes", "description": "Current staged, unstaged, and untracked changes",
				"changeKind": "uncommitted", "uriTemplate": "ahp-changeset:/session-1/uncommitted",
			},
		})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("catalogue = %v\nwant %v", got, want)
		}
		parsed, ok := changeset.ParseURI(changeset.URI("session-1", changeset.Uncommitted))
		if !ok || parsed != (changeset.ParsedURI{SessionID: "session-1", Kind: changeset.Uncommitted}) {
			t.Fatalf("parse = %+v, %v", parsed, ok)
		}
	})
	twin.Run(t, "changeset-uri", "rejects channels the host did not advertise", func(t *testing.T) {
		for _, uri := range []string{
			"ahp-changeset:/session-1/turn",
			"ahp-changeset:/session-1/uncommitted/extra",
			"ahp-changeset:/session%2Fother/uncommitted",
			"ahp-changeset:/session-1/uncommitted?ref=HEAD",
		} {
			if parsed, ok := changeset.ParseURI(uri); ok {
				t.Errorf("%s parsed as %+v", uri, parsed)
			}
		}
	})
}

// Beyond upstream's two cases: the omission of Latest Commit, escaping round trips and the
// malformed shapes ParseURI must not accept.
func TestChangesetURIEdges(t *testing.T) {
	if got := changeset.Catalogue("s", false); len(got) != 1 || got[0].Label != "Uncommitted Changes" {
		t.Fatalf("catalogue without a commit = %+v", got)
	}
	for _, id := range []string{"plain", "with space", "ünï😀", "a%b", "quote'(x)*!~"} {
		for _, kind := range changeset.Kinds {
			uri := changeset.URI(id, kind)
			parsed, ok := changeset.ParseURI(uri)
			if !ok || parsed.SessionID != id || parsed.Kind != kind {
				t.Errorf("%q: %s parsed as %+v, %v", id, uri, parsed, ok)
			}
		}
	}
	if got := changeset.URI("a b/c", changeset.LatestCommit); got != "ahp-changeset:/a%20b%2Fc/latest-commit" {
		t.Errorf("URI escapes = %s", got)
	}
	for _, uri := range []string{
		"", "ahp-changeset:", "ahp-changeset:/", "ahp-changeset://x/uncommitted", "ahp-changeset:/uncommitted",
		"ahp-changeset:/x/uncommitted#frag", "ahp-changeset:/%/uncommitted", "ahp-changeset:/%ZZ/uncommitted",
		"ahp-changeset:/%ED%A0%80/uncommitted", "ahp-changeset:/a%41/uncommitted", "ahp-changeset:/a/../x/uncommitted",
		"ahp-session:/x/uncommitted", "ahp-changeset:/x/Uncommitted", "ahp-changeset:/%C3%BCn%2F/uncommitted",
	} {
		if parsed, ok := changeset.ParseURI(uri); ok {
			t.Errorf("%q parsed as %+v", uri, parsed)
		}
	}
}
