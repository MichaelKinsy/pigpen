package pi

import (
	"context"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// Inline completions for a message being composed (port of src/pi/completions.ts).
//
// The protocol makes CompletionItem.attachment required, which decides what this feature is: an
// attachment picker. "@"-mentions fit; Pi's slash commands do not, because they attach nothing.

// MentionTrigger is the character that opens the file picker.
const MentionTrigger = "@"

const (
	maxCompletionItems = 50
	// maxScannedEntries bounds the directory walk of a huge working directory.
	maxScannedEntries = 10_000
)

// skippedDirectories are never descended into. The list is deliberately narrow: build output such
// as dist/ and build/ is often exactly what a user wants to mention.
var skippedDirectories = map[string]bool{".git": true, "node_modules": true}

// score ranks a relative path against a lower-cased query: an exact file name beats a prefix beats
// a substring of the name beats a substring of the path; 0 is no match.
func score(relativePath, query string) int {
	if query == "" {
		return 1
	}
	path := strings.ToLower(relativePath)
	name := path[strings.LastIndexByte(path, '/')+1:]
	switch {
	case name == query:
		return 4
	case strings.HasPrefix(name, query):
		return 3
	case strings.Contains(name, query):
		return 2
	case strings.Contains(path, query):
		return 1
	}
	return 0
}

// Mention is the "@" token the cursor sits in; offsets are UTF-16 code units, the unit clients
// count text in.
type Mention struct {
	Start, End int
	Query      string
}

func isSpaceUnit(u uint16) bool {
	return u < 0xd800 && isJSSpaceRune(rune(u)) || u >= 0xe000 && isJSSpaceRune(rune(u))
}

func isJSSpaceRune(r rune) bool { return mapperIsSpace(r) }

// FindMention finds the mention the cursor is in, if any. An "@" only opens one at the start of
// the text or after whitespace (otherwise every email address and decorator opens the picker), and
// the cursor must not have moved past the mention's end.
func FindMention(text string, offset int) (Mention, bool) {
	units := utf16.Encode([]rune(text))
	clamped := offset
	if clamped < 0 {
		clamped = 0
	}
	if clamped > len(units) {
		clamped = len(units)
	}
	for index := clamped; index > 0; index-- {
		char := units[index-1]
		if isSpaceUnit(char) {
			return Mention{}, false
		}
		if char == '@' {
			if index >= 2 && !isSpaceUnit(units[index-2]) {
				return Mention{}, false
			}
			return Mention{Start: index - 1, End: clamped, Query: string(utf16.Decode(units[index:clamped]))}, true
		}
	}
	return Mention{}, false
}

// CompletionServiceOptions configure a [CompletionService].
type CompletionServiceOptions struct {
	// WorkingDirectoryFor is the directory a chat's mentions resolve against ("" when unknown).
	WorkingDirectoryFor func(chat string) string
	// MaxItems caps the result (default 50).
	MaxItems int
}

// CompletionService implements host.CompletionHandler.
type CompletionService struct{ opts CompletionServiceOptions }

// NewCompletionService creates the service.
func NewCompletionService(opts CompletionServiceOptions) *CompletionService {
	return &CompletionService{opts: opts}
}

type candidate struct {
	absolute, relative string
	rank               int
}

// localeLess approximates String.localeCompare for path tie-breaking: case-insensitive first, then
// case-sensitive (ICU collation differs from this on punctuation; see PORT.md).
func localeLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// Complete implements host.CompletionHandler.
func (s *CompletionService) Complete(_ context.Context, p ahptypes.CompletionsParams) (ahptypes.CompletionsResult, error) {
	empty := ahptypes.CompletionsResult{Items: []ahptypes.CompletionItem{}}
	if p.Kind != ahptypes.CompletionItemKindUserMessage {
		return empty, nil
	}
	workingDirectory := s.opts.WorkingDirectoryFor(p.Channel)
	if workingDirectory == "" {
		return empty, nil
	}
	mention, ok := FindMention(p.Text, int(p.Offset))
	if !ok {
		return empty, nil
	}
	query := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(mention.Query, `\`, "/"), "./"))

	var candidates []candidate
	scanned := 0
	errScanLimit := fs.SkipAll
	err := filepath.WalkDir(workingDirectory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == workingDirectory {
				return err
			}
			return nil // an unreadable entry is skipped, not fatal
		}
		if path == workingDirectory {
			return nil
		}
		name := d.Name()
		// Like a glob, dot-entries are neither matched nor descended into.
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && skippedDirectories[name] {
			return fs.SkipDir
		}
		scanned++
		if scanned > maxScannedEntries {
			return errScanLimit
		}
		if d.IsDir() {
			return nil
		}
		// Files and symbolic links are offered; sockets, devices and the like are not.
		if !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(workingDirectory, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rank := score(rel, query); rank > 0 {
			candidates = append(candidates, candidate{absolute: path, relative: rel, rank: rank})
		}
		return nil
	})
	if err != nil {
		return empty, nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank > candidates[j].rank
		}
		return localeLess(candidates[i].relative, candidates[j].relative)
	})
	max := s.opts.MaxItems
	if max <= 0 {
		max = maxCompletionItems
	}
	if len(candidates) > max {
		candidates = candidates[:max]
	}
	start, end := int64(mention.Start), int64(mention.End)
	items := make([]ahptypes.CompletionItem, 0, len(candidates))
	for _, c := range candidates {
		kind := "document"
		items = append(items, ahptypes.CompletionItem{
			InsertText: MentionTrigger + c.relative, RangeStart: &start, RangeEnd: &end,
			Attachment: ahptypes.MessageAttachment{Value: &ahptypes.MessageResourceAttachment{
				Type: ahptypes.MessageAttachmentKindResource, Label: filepathBase(c.relative), DisplayKind: &kind, Uri: fileURL(c.absolute),
			}},
		})
	}
	return ahptypes.CompletionsResult{Items: items}, nil
}

func filepathBase(rel string) string { return rel[strings.LastIndexByte(rel, '/')+1:] }

// fileURL is url.pathToFileURL(path).toString().
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func mapperIsSpace(r rune) bool { return mapper.IsJSSpace(r) }
