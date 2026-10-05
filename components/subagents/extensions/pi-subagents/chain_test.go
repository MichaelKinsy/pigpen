package pi_subagents

import (
	"regexp"
	"testing"
)

const chainFile = "chain-serializer"

func chainText(body string) string {
	return "---\nname: review-chain\ndescription: Review chain\n---\n\n" + body
}

func mustChain(t *testing.T, content string) *ChainConfig {
	t.Helper()
	c, err := ParseChain(content, SourceProject, "/tmp/review-chain.md")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func matches(t *testing.T, s, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Errorf("%q does not match /%s/", s, re)
	}
}

func TestChainSerializer(t *testing.T) {
	tw(t, chainFile, "round-trips step outputMode", func(t *testing.T) {
		parsed := mustChain(t, "---\nname: review-chain\ndescription: Review chain\n---\n\n## reviewer\noutput: report.md\noutputMode: file-only\n\nReview the diff\n")
		eq(t, parsed.Steps[0].OutputMode, "file-only", "outputMode")
		matches(t, SerializeChain(parsed), `outputMode: file-only`)
	})
	tw(t, chainFile, "round-trips phase, label, as, and path-based outputSchema", func(t *testing.T) {
		parsed := mustChain(t, chainText("## reviewer\nphase: Review\nlabel: correctness pass\nas: correctnessFindings\noutputSchema: ./schemas/finding.schema.json\n\nReview the diff\n"))
		s := parsed.Steps[0]
		eq(t, []string{s.Phase, s.Label, s.As, s.OutputSchema}, []string{"Review", "correctness pass", "correctnessFindings", "./schemas/finding.schema.json"}, "step")
		out := SerializeChain(parsed)
		matches(t, out, `phase: Review`)
		matches(t, out, `label: correctness pass`)
		matches(t, out, `as: correctnessFindings`)
		matches(t, out, `outputSchema: \./schemas/finding\.schema\.json`)
	})
	tw(t, chainFile, "round-trips markdown chain toolBudget", func(t *testing.T) {
		parsed := mustChain(t, chainText("## reviewer\ntoolBudget: {\"soft\":3,\"hard\":5,\"block\":[\"read\",\"grep\"]}\n\nReview the diff\n"))
		eq(t, marshalJSON(parsed.Steps[0].ToolBudget, ""), `{"soft":3,"hard":5,"block":["read","grep"]}`, "toolBudget")
		matches(t, SerializeChain(parsed), `toolBudget: \{"soft":3,"hard":5,"block":\["read","grep"\]\}`)
	})
	tw(t, chainFile, "rejects invalid markdown chain toolBudget", func(t *testing.T) {
		_, err := ParseChain(chainText("## reviewer\ntoolBudget: {\"soft\":6,\"hard\":5}\n\nReview the diff\n"), SourceProject, "/tmp/review-chain.md")
		hasMatch(t, err, "toolBudget for step 'reviewer'.soft must be <= toolBudget for step 'reviewer'.hard")
	})
	tw(t, chainFile, "rejects inline outputSchema values in markdown chains", func(t *testing.T) {
		_, err := ParseChain(chainText("## reviewer\noutputSchema: {\"type\":\"object\"}\n\nReview the diff\n"), SourceProject, "/tmp/review-chain.md")
		hasMatch(t, err, "Inline outputSchema values are not supported")
	})
}

func TestChainPackages(t *testing.T) {
	tw(t, "agent-frontmatter", "parses packaged chains directly from serializer helpers", func(t *testing.T) {
		parsed, err := ParseChain("---\nname: review-flow\npackage: code-analysis\ndescription: Review flow\n---\n\n## code-analysis.scout\n\nInspect\n", SourceProject, "/tmp/review.chain.md")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, parsed.Name, "code-analysis.review-flow", "name")
		eq(t, parsed.LocalName, "review-flow", "localName")
		eq(t, parsed.PackageName, "code-analysis", "package")
		matches(t, SerializeChain(parsed), `(?m)^name: review-flow$`)
	})
}
