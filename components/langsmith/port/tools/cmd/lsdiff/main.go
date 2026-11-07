// Command lsdiff compares two mockls request logs (the JavaScript original and the Go port).
//
//	lsdiff [-max N] [-scenario scenario.json] [-rule P=>R]... [-path-prefix P]... A.jsonl B.jsonl
//	lsdiff -rules
//
// Exit status 0: identical after the documented normalisations; 1: different; 2: usage or input error; 3: no difference but
// an anomaly in a log itself (an unparsable body, a wrong length=): the logs agree on something that is broken.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"lstools/internal/lsdiff"
	"lstools/internal/reqlog"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lsdiff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var prefixes multi
	max := fs.Int("max", 1, "report up to this many differences")
	scenario := fs.String("scenario", "", "pigeq scenario file whose `normalize` list is applied as rule N7")
	hostPrompt := fs.Bool("host-prompt", false, "rule N8: the logs come from different hosts (Pi and PiG); system prompt text is not compared")
	unordered := fs.Bool("unordered-drain", false, "compare consecutive multipart/batch requests (the concurrent batches of one queue drain) as a multiset")
	var rulesFlag multi
	fs.Var(&rulesFlag, "rule", "an extra N7 rule PATTERN=>REPLACEMENT (Go regexp, applied after N1-N6; repeatable): for a value one scenario's provider invents on every run")
	listRules := fs.Bool("rules", false, "print the normalisation rules and exit")
	fs.Var(&prefixes, "path-prefix", "an extra path prefix rule N6 replaces by <lane> (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *listRules {
		for _, r := range lsdiff.Rules {
			fmt.Fprintf(stdout, "%s  %s\n", r.ID, r.Text)
		}
		return 0
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: lsdiff [-max N] [-scenario F] [-path-prefix P]... A.jsonl B.jsonl   (A: JS original, B: Go port)")
		return 2
	}
	var rules []lsdiff.Rule
	for _, r := range rulesFlag {
		i := strings.LastIndex(r, "=>")
		if i <= 0 {
			fmt.Fprintf(stderr, "lsdiff: -rule %q: want PATTERN=>REPLACEMENT\n", r)
			return 2
		}
		rules = append(rules, lsdiff.Rule{Pattern: r[:i], Replacement: r[i+2:]})
	}
	if *scenario != "" {
		data, err := os.ReadFile(*scenario)
		if err != nil {
			fmt.Fprintln(stderr, "lsdiff:", err)
			return 2
		}
		var sc struct {
			Normalize []lsdiff.Rule `json:"normalize"`
		}
		if err := json.Unmarshal(data, &sc); err != nil {
			fmt.Fprintln(stderr, "lsdiff:", *scenario+":", err)
			return 2
		}
		rules = sc.Normalize
	}
	a, err := reqlog.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "lsdiff:", err)
		return 2
	}
	b, err := reqlog.ReadFile(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "lsdiff:", err)
		return 2
	}
	res, err := lsdiff.Compare(a, b, lsdiff.Options{Rules: rules, PathPrefixes: prefixes, Max: *max, HostPrompt: *hostPrompt, UnorderedDrain: *unordered})
	if err != nil {
		fmt.Fprintln(stderr, "lsdiff:", err)
		return 2
	}
	res.Report(stdout, fs.Arg(0), fs.Arg(1))
	if !res.Same() {
		return 1
	}
	if len(res.Anomalies) > 0 {
		return 3
	}
	return 0
}
