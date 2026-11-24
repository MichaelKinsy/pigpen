package search

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Lexical-only port of omp's `find` (jfind) tool.
// upstream: packages/coding-agent/src/tools/jfind/{index,keywords,lexical}.ts
// The LLM judge cascade (filename ranking, sketch routing, passage verification)
// is not ported because PiG extensions do not have omp's System One judge role.

const (
	findMaxHits    = 20
	findMaxRanges  = 3
	findTimeout    = 20 * time.Second
	findSnippetMax = 120
)

var findStopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "or": {}, "and": {}, "to": {}, "is": {}, "are": {},
	"be": {}, "when": {}, "where": {}, "how": {}, "that": {}, "this": {}, "of": {},
	"in": {}, "on": {}, "at": {}, "for": {}, "with": {}, "by": {}, "its": {}, "it": {},
	"as": {}, "from": {}, "into": {}, "like": {}, "gets": {}, "get": {}, "up": {},
	"which": {}, "what": {}, "does": {}, "do": {}, "code": {}, "file": {}, "files": {},
	"over": {}, "all": {}, "user": {}, "using": {}, "then": {}, "than": {}, "there": {},
	"their": {}, "they": {}, "them": {}, "you": {}, "your": {}, "we": {}, "our": {},
	"has": {}, "have": {}, "had": {}, "was": {}, "were": {}, "been": {}, "being": {},
	"will": {}, "would": {}, "should": {}, "can": {}, "could": {}, "not": {}, "but": {},
	"if": {}, "so": {}, "such": {}, "via": {}, "per": {}, "any": {}, "some": {},
	"each": {}, "every": {}, "also": {}, "just": {}, "only": {}, "more": {}, "most": {},
	"other": {}, "out": {}, "off": {}, "about": {}, "after": {}, "before": {},
	"between": {}, "through": {}, "during": {}, "without": {}, "within": {}, "one": {},
	"two": {}, "new": {}, "used": {}, "use": {}, "make": {}, "makes": {}, "made": {},
	"run": {}, "runs": {}, "way": {}, "thing": {}, "things": {}, "something": {},
	"actually": {}, "really": {}, "still": {}, "yet": {},
}

func stemKeyword(tok string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if strings.HasSuffix(tok, suf) {
			base := tok[:len(tok)-len(suf)]
			if len(base) >= 4 {
				return base
			}
		}
	}
	return tok
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// keywordsFromQuery extracts quoted phrases whole and alphanumeric tokens minus stopwords.
// upstream: packages/coding-agent/src/tools/jfind/keywords.ts:135-169
func keywordsFromQuery(query string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(k string) {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}

	var rest strings.Builder
	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c != '"' && c != '\'' {
			rest.WriteRune(c)
			continue
		}
		var phrase strings.Builder
		closed := false
		for i+1 < len(runes) {
			i++
			if runes[i] == c {
				closed = true
				break
			}
			phrase.WriteRune(runes[i])
		}
		p := strings.ToLower(strings.TrimSpace(phrase.String()))
		if closed && len(p) >= 3 {
			add(p)
			rest.WriteRune(' ')
			continue
		}
		rest.WriteString(p)
		rest.WriteRune(' ')
	}

	tokens := strings.FieldsFunc(rest.String(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	for _, tok := range tokens {
		lower := strings.ToLower(tok)
		if len(lower) < 3 || isAllDigits(lower) {
			continue
		}
		if _, stop := findStopwords[lower]; stop {
			continue
		}
		add(stemKeyword(lower))
	}
	return out
}

func deriveFindKeywords(query string, extra []string) []string {
	out := keywordsFromQuery(query)
	seen := make(map[string]struct{}, len(out))
	for _, k := range out {
		seen[k] = struct{}{}
	}
	for _, raw := range extra {
		k := strings.ToLower(strings.TrimSpace(raw))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

func executeFind(ctx sdk.Context, params map[string]any) (any, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-bgCtx.Done():
		}
	}()
	return runFind(bgCtx, ctx.Cwd(), params)
}

func runFind(bgCtx context.Context, cwd string, params map[string]any) (any, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("`query` must be a non-empty description")
	}

	var extraKeywords []string
	if rawKws, ok := params["grep_keywords"].([]any); ok {
		for _, v := range rawKws {
			if s, ok := v.(string); ok {
				extraKeywords = append(extraKeywords, s)
			}
		}
	}

	pathInput, _ := params["path"].(string)
	scopeRoot := cwd
	if strings.TrimSpace(pathInput) != "" {
		if isRootOnlyPath(pathInput) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		scopeRoot = resolvePath(strings.TrimSpace(pathInput), cwd)
	}
	info, err := os.Stat(scopeRoot)
	if err != nil {
		return nil, fmt.Errorf("Path not found: %s", pathInput)
	}

	keywords := deriveFindKeywords(query, extraKeywords)
	if len(keywords) == 0 {
		return sdk.ToolResult{
			Content: fmt.Sprintf("no hits for %q (lexical mode)", query),
			Details: map[string]any{"hits": []any{}, "keywords": keywords},
		}, nil
	}

	var files []walkEntry
	if !info.IsDir() {
		files = []walkEntry{{
			absPath: scopeRoot,
			relPath: formatRelToCwd(scopeRoot, cwd, false),
			size:    info.Size(),
		}}
	} else {
		entries, err := walkDir(bgCtx, scopeRoot, walkOptions{
			hidden:    false,
			gitignore: true,
			recursive: true,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.isDir {
				files = append(files, e)
			}
		}
	}

	// Build case-insensitive alternation regex for fast line filtering.
	escaped := make([]string, len(keywords))
	for i, k := range keywords {
		escaped[i] = regexp.QuoteMeta(k)
	}
	re, err := regexp.Compile("(?i)" + strings.Join(escaped, "|"))
	if err != nil {
		return nil, err
	}

	type lineHit struct {
		lineNum int
		snippet string
		hits    int
	}
	type fileLexical struct {
		rel      string
		counts   []int
		topLines []lineHit
		lineCnt  int
	}

	var matched []*fileLexical
	for _, f := range files {
		data, err := os.ReadFile(f.absPath)
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > grepMaxFileBytes {
			data = data[:grepMaxFileBytes]
		}
		probeLen := min(len(data), grepBinaryProbeBytes)
		if bytes.IndexByte(data[:probeLen], 0) != -1 {
			continue
		}
		counts := make([]int, len(keywords))
		anyHit := false
		var topLines []lineHit
		lines := strings.Split(string(data), "\n")
		for idx, rawLine := range lines {
			line := strings.TrimRight(rawLine, "\r")
			if !re.MatchString(line) {
				continue
			}
			lower := strings.ToLower(line)
			lineHits := 0
			for k, kw := range keywords {
				c := strings.Count(lower, kw)
				if c > 0 {
					counts[k] += c
					lineHits += c
					anyHit = true
				}
			}
			if lineHits > 0 && len(topLines) < findMaxRanges*4 {
				snip := strings.TrimSpace(line)
				if len(snip) > findSnippetMax {
					snip = snip[:findSnippetMax] + "..."
				}
				topLines = append(topLines, lineHit{lineNum: idx + 1, snippet: snip, hits: lineHits})
			}
		}
		if anyHit {
			sort.Slice(topLines, func(i, j int) bool {
				if topLines[i].hits != topLines[j].hits {
					return topLines[i].hits > topLines[j].hits
				}
				return topLines[i].lineNum < topLines[j].lineNum
			})
			if len(topLines) > findMaxRanges {
				topLines = topLines[:findMaxRanges]
			}
			matched = append(matched, &fileLexical{
				rel:      formatRelToCwd(f.absPath, cwd, false),
				counts:   counts,
				topLines: topLines,
				lineCnt:  len(lines),
			})
		}
	}

	if len(matched) == 0 {
		return sdk.ToolResult{
			Content: fmt.Sprintf("no hits for %q (lexical mode)", query),
			Details: map[string]any{"hits": []any{}, "keywords": keywords},
		}, nil
	}

	// Compute IDF weights clamped to [0.5, 6].
	// upstream: packages/coding-agent/src/tools/jfind/lexical.ts:81-90
	weights := make([]float64, len(keywords))
	totalScanned := len(files)
	for k := range keywords {
		df := 0
		for _, m := range matched {
			if m.counts[k] > 0 {
				df++
			}
		}
		w := math.Log(float64(totalScanned+1) / float64(df+1))
		weights[k] = math.Min(6, math.Max(0.5, w))
	}

	type scoredHit struct {
		file  *fileLexical
		raw   float64
		score float64 // normalized to (0, 1) via sigmoid-like scaling
	}
	scored := make([]scoredHit, len(matched))
	maxRaw := 0.0
	for i, m := range matched {
		lowerRel := strings.ToLower(m.rel)
		s := 0.0
		for k, kw := range keywords {
			inPath := 0.0
			if strings.Contains(lowerRel, kw) {
				inPath = 1.0
			}
			s += weights[k] * (2*inPath + math.Log1p(float64(m.counts[k])))
		}
		scored[i] = scoredHit{file: m, raw: s}
		if s > maxRaw {
			maxRaw = s
		}
	}
	for i := range scored {
		if maxRaw > 0 {
			scored[i].score = scored[i].raw / (maxRaw + 1.0)
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].raw != scored[j].raw {
			return scored[i].raw > scored[j].raw
		}
		return scored[i].file.rel < scored[j].file.rel
	})
	if len(scored) > findMaxHits {
		scored = scored[:findMaxHits]
	}

	var out []string
	out = append(out, fmt.Sprintf("%d hit(s) for %q (lexical mode; semantic judge not ported), strongest first", len(scored), query))
	out = append(out, "")
	for _, h := range scored {
		out = append(out, fmt.Sprintf("%s  %.2f  %d lines scanned", h.file.rel, h.score, h.file.lineCnt))
		for _, lr := range h.file.topLines {
			out = append(out, fmt.Sprintf("  %s:%d  %.2f  %s", h.file.rel, lr.lineNum, h.score, lr.snippet))
		}
	}

	return sdk.ToolResult{
		Content: strings.Join(out, "\n"),
		Details: map[string]any{
			"query":    query,
			"keywords": keywords,
			"hitCount": len(scored),
		},
	}, nil
}
