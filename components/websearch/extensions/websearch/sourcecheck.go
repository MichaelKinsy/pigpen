package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Port of source-check.ts: structured source evidence and machine-readable research artifacts.
// The assessment never claims semantic support: it reports what was retrieved, with exact
// character spans, and asks for manual review.

// Span is a character range in the fetched content (UTF-16 units, like the original).
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ResearchSource is one ranked source of an artifact.
type ResearchSource struct {
	Rank           int     `json:"rank"`
	URL            string  `json:"url"`
	Title          string  `json:"title"`
	Snippet        *string `json:"snippet,omitempty"`
	FetchTimestamp *int64  `json:"fetch_timestamp,omitempty"`
	ContentHash    string  `json:"content_hash,omitempty"`
	Quality        string  `json:"quality"`
	Fetched        *bool   `json:"fetched,omitempty"`
	FetchError     string  `json:"fetch_error,omitempty"`
}

// ResearchPassage is a cited passage.
type ResearchPassage struct {
	PassageID      string `json:"passage_id"`
	SourceURL      string `json:"source_url"`
	SourceRank     int    `json:"source_rank"`
	Text           string `json:"text"`
	ExtractionSpan *Span  `json:"extraction_span,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
}

// ClaimAssessment is the (deliberately non-semantic) assessment of a claim.
type ClaimAssessment struct {
	Claim                 string   `json:"claim"`
	Status                string   `json:"status"`
	SupportingPassages    []string `json:"supporting_passages"`
	ContradictingPassages []string `json:"contradicting_passages"`
	Rationale             string   `json:"rationale"`
	Confidence            float64  `json:"confidence"`
}

// ArtifactFilters record the request filters.
type ArtifactFilters struct {
	Recency       string   `json:"recency,omitempty"`
	DomainInclude []string `json:"domain_include,omitempty"`
	DomainExclude []string `json:"domain_exclude,omitempty"`
}

// ArtifactError is a failed query of the artifact.
type ArtifactError struct {
	Query string `json:"query"`
	Error string `json:"error"`
}

// ResearchArtifact is what source_check stores and returns.
type ResearchArtifact struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Timestamp   int64             `json:"timestamp"`
	Query       string            `json:"query"`
	Sources     []ResearchSource  `json:"sources"`
	Passages    []ResearchPassage `json:"passages"`
	Claims      []ClaimAssessment `json:"claims,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	Summary     *string           `json:"summary,omitempty"`
	ContentHash string            `json:"content_hash,omitempty"`
	Filters     ArtifactFilters   `json:"filters"`
	Errors      []ArtifactError   `json:"errors,omitempty"`
}

var (
	officialDocsHosts = regexp.MustCompile(`(?i)^(developers\.|docs\.|learn\.|reference\.)|\.github\.io$`)
	officialDocsPaths = regexp.MustCompile(`(?i)/(docs?|reference)(/|\b)`)
	vendorDocsPaths   = regexp.MustCompile(`(?i)/(documentation|docs?)/`)
	repoIssuePaths    = regexp.MustCompile(`(?i)/(issues|pull|pulls)/`)
	blogHosts         = regexp.MustCompile(`(?i)(medium\.com|substack\.com|dev\.to|hashnode\.)`)
	blogPaths         = regexp.MustCompile(`(?i)/blogs?/`)
	forumHosts        = regexp.MustCompile(`(?i)(stackoverflow\.com|serverfault\.com|superuser\.com|discourse\.|community\.)`)
	forumPaths        = regexp.MustCompile(`(?i)/(forum|forums|threads)/`)
	newsHosts         = regexp.MustCompile(`(?i)(reuters\.com|bloomberg\.com|techcrunch\.com|theverge\.com|arstechnica\.com|wired\.com|cnet\.com|zdnet\.com)`)
	newsPaths         = regexp.MustCompile(`(?i)/news(/|$)`)
)

// ClassifySource labels a URL by where it lives.
func ClassifySource(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "unknown"
	}
	host, path := strings.ToLower(u.Hostname()), u.Path
	if path == "" {
		path = "/"
	}
	switch {
	case repoIssuePaths.MatchString(path):
		return "repo_issue"
	case officialDocsHosts.MatchString(host) || officialDocsPaths.MatchString(path):
		return "official_docs"
	case vendorDocsPaths.MatchString(path):
		return "vendor_docs"
	case newsHosts.MatchString(host) || newsPaths.MatchString(path):
		return "news"
	case forumHosts.MatchString(host) || forumPaths.MatchString(path):
		return "forum"
	case blogHosts.MatchString(host) || blogPaths.MatchString(path):
		return "blog"
	}
	return "unknown"
}

// HashContent is "sha256:" plus the hex digest of the UTF-8 text.
func HashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

type textSpan struct {
	text       string
	start, end int
}

func tokenizeHint(value string) []string {
	var out []string
	seen := map[string]bool{}
	for _, term := range regexp.MustCompile(`[^a-z0-9]+`).Split(strings.ToLower(value), -1) {
		if len(term) > 3 && !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

func isPunct(c uint16) bool { return c == '.' || c == '!' || c == '?' }

func isSpaceU16(c uint16) bool { return isJSSpace(rune(c)) }

// sentenceSpans finds the matches of /[^.!?]+(?:[.!?]+(?=\s|$)|$)/g, including its behaviour of
// dropping a sentence whose terminating punctuation is not followed by whitespace ("3.5 is").
func sentenceSpans(u []uint16) [][2]int {
	var out [][2]int
	n := len(u)
	pos := 0
	for pos < n {
		if isPunct(u[pos]) {
			pos++
			continue
		}
		j := pos
		for j < n && !isPunct(u[j]) {
			j++
		}
		k := j
		for k < n && isPunct(u[k]) {
			k++
		}
		if k > j && (k == n || isSpaceU16(u[k])) {
			out = append(out, [2]int{pos, k})
			pos = k
			continue
		}
		if j == n {
			out = append(out, [2]int{pos, n})
			pos = n
			continue
		}
		pos = j
	}
	return out
}

func extractRelevantSpans(content, hint string) []textSpan {
	u := utf16.Encode([]rune(content))
	var sentences []textSpan
	for _, m := range sentenceSpans(u) {
		raw := string(utf16.Decode(u[m[0]:m[1]]))
		text := jsTrim(raw)
		if jsLen(text) > 0 && jsLen(text) <= 400 {
			lead := jsIndexOf(raw, text, 0)
			start := m[0] + lead
			sentences = append(sentences, textSpan{text, start, start + jsLen(text)})
		}
	}
	terms := tokenizeHint(hint)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		s     textSpan
		index int
		score int
	}
	var items []scored
	for i, s := range sentences {
		lower := strings.ToLower(s.text)
		score := 0
		for _, t := range terms {
			if strings.Contains(lower, t) {
				score++
			}
		}
		if score > 0 {
			items = append(items, scored{s, i, score})
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].score != items[b].score {
			return items[a].score > items[b].score
		}
		return items[a].index < items[b].index
	})
	if len(items) > 3 {
		items = items[:3]
	}
	out := make([]textSpan, len(items))
	for i, it := range items {
		out[i] = it.s
	}
	return out
}

func passageID(rank, index int) string { return "p-" + strconv.Itoa(rank) + "-" + strconv.Itoa(index) }

// BuildPassages cites each source's snippet and, when the page was fetched, up to three of its
// sentences that share terms with the snippet (or the claim) with their exact spans.
func BuildPassages(sources []ResearchSource, fetched []ExtractedContent, hint string) []ResearchPassage {
	passages := []ResearchPassage{}
	byURL := map[string]*ExtractedContent{}
	for i := range fetched {
		byURL[fetched[i].URL] = &fetched[i]
	}
	for _, source := range sources {
		if source.Snippet != nil && *source.Snippet != "" {
			passages = append(passages, ResearchPassage{PassageID: passageID(source.Rank, 0), SourceURL: source.URL, SourceRank: source.Rank, Text: *source.Snippet, ContentHash: HashContent(*source.Snippet)})
		}
		page := byURL[source.URL]
		if page != nil && page.Error == nil && page.Content != "" {
			passageHint := hint
			if source.Snippet != nil && jsTrim(*source.Snippet) != "" {
				passageHint = jsTrim(*source.Snippet)
			}
			for index, span := range extractRelevantSpans(page.Content, passageHint) {
				passages = append(passages, ResearchPassage{PassageID: passageID(source.Rank, index+1), SourceURL: source.URL, SourceRank: source.Rank, Text: span.text,
					ExtractionSpan: &Span{span.start, span.end}, ContentHash: HashContent(span.text)})
			}
		}
	}
	return passages
}

// AssessClaim states whether any passage was retrieved; it never asserts support or contradiction.
func AssessClaim(claim string, passages []ResearchPassage) ClaimAssessment {
	if len(passages) == 0 {
		return ClaimAssessment{Claim: claim, Status: "missing-evidence", SupportingPassages: []string{}, ContradictingPassages: []string{}, Rationale: "No passages were retrieved for the claim.", Confidence: 0.2}
	}
	return ClaimAssessment{Claim: claim, Status: "unclear", SupportingPassages: []string{}, ContradictingPassages: []string{},
		Rationale:  "Passages were retrieved, but automated semantic support or contradiction assessment is unavailable; review the cited passages manually.",
		Confidence: 0.3}
}

// RankedSearchResult is a search result with its rank.
type RankedSearchResult struct {
	SearchResult
	Rank int
}

// BuildArtifactInput is the input of BuildResearchArtifact.
type BuildArtifactInput struct {
	Query        string
	Provider     string
	Summary      *string
	Results      []RankedSearchResult
	Fetched      []ExtractedContent
	Recency      string
	DomainFilter []string
}

// BuildResearchArtifact assembles the artifact: unique sources in rank order with quality labels,
// the fetch outcome per source, passages, hashes and the request filters.
func BuildResearchArtifact(in BuildArtifactInput) ResearchArtifact {
	byURL := map[string]*ExtractedContent{}
	for i := range in.Fetched {
		byURL[in.Fetched[i].URL] = &in.Fetched[i]
	}
	sources := []ResearchSource{}
	seen := map[string]bool{}
	for index, r := range in.Results {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		page := byURL[r.URL]
		fetched := page != nil && page.Error == nil
		rank := r.Rank
		if rank == 0 {
			rank = index + 1
		}
		snippet := r.Snippet
		s := ResearchSource{Rank: rank, URL: r.URL, Title: r.Title, Snippet: &snippet, Quality: ClassifySource(r.URL), Fetched: &fetched}
		if page != nil {
			ts := nowMs()
			s.FetchTimestamp = &ts
			if page.Error == nil {
				s.ContentHash = HashContent(page.Content)
			} else {
				s.FetchError = *page.Error
			}
		}
		sources = append(sources, s)
	}
	passages := BuildPassages(sources, in.Fetched, in.Query)
	include, exclude := []string{}, []string{}
	for _, d := range in.DomainFilter {
		if strings.HasPrefix(d, "-") {
			exclude = append(exclude, d[1:])
		} else {
			include = append(include, d)
		}
	}
	a := ResearchArtifact{ID: GenerateID(), Type: "research", Timestamp: nowMs(), Query: in.Query, Sources: sources, Passages: passages,
		Provider: in.Provider, Summary: in.Summary, Filters: ArtifactFilters{Recency: in.Recency, DomainInclude: include, DomainExclude: exclude}}
	if len(passages) > 0 {
		texts := make([]string, len(passages))
		for i, p := range passages {
			texts[i] = p.Text
		}
		a.ContentHash = HashContent(strings.Join(texts, "\n"))
	}
	return a
}

// WithClaimAssessment attaches one assessment per claim.
func WithClaimAssessment(a ResearchArtifact, claims []string) ResearchArtifact {
	a.Claims = make([]ClaimAssessment, len(claims))
	for i, c := range claims {
		a.Claims[i] = AssessClaim(c, a.Passages)
	}
	return a
}

// StoreResearchArtifact retains an artifact for get_search_content.
func StoreResearchArtifact(a *ResearchArtifact) error {
	if a.ID == "" {
		return errors.New("Research artifact id must not be empty")
	}
	StoreResult(a.ID, &StoredSearchData{ID: a.ID, Type: "research", Timestamp: a.Timestamp, Artifact: a})
	return nil
}

// GetResearchArtifact returns a retained artifact.
func GetResearchArtifact(id string) *ResearchArtifact {
	data := GetResult(id)
	if data == nil || data.Type != "research" || data.Artifact == nil {
		return nil
	}
	switch v := data.Artifact.(type) {
	case *ResearchArtifact:
		return v
	default:
		// restored from a session entry: decode the generic object
		raw, err := jsonMarshal(v)
		if err != nil {
			return nil
		}
		var a ResearchArtifact
		if jsonUnmarshal(raw, &a) != nil {
			return nil
		}
		return &a
	}
}
