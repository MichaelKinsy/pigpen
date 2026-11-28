package hashline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Snapshot-tag mismatch diagnostics.
// upstream: crates/edit/src/modes/hashline/mismatch.rs

var lineRefRe = regexp.MustCompile(`^\s*[>+\-*]*\s*(\d+)(?::.*)?\s*$`)

// MismatchDetails holds context needed to explain a stale or unrecognized snapshot tag.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:17
type MismatchDetails struct {
	Path             string
	ExpectedFileHash string
	ActualFileHash   string
	FileLines        []string
	AnchorLines      []uint32
	HashRecognized   bool
	TagOriginPaths   []string
}

// FormatFullAnchorRequirement formats the required shape of a tagged line anchor.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:32
func FormatFullAnchorRequirement(raw string, hasRaw bool) string {
	received := ""
	if hasRaw {
		received = " Received " + JSONQuote(raw) + "."
	}
	return fmt.Sprintf(
		"a bare line number from read/search output plus the section header content-hash tag (for example [src/foo.ts#%s] and line \"160\")%s",
		HLFileHashExamples[0], received,
	)
}

// ParseTag parses a decorated bare line-number reference.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:42
func ParseTag(reference string) (uint32, *EditError) {
	m := lineRefRe.FindStringSubmatch(reference)
	if m == nil {
		return 0, ParseError(fmt.Sprintf("Invalid line reference. Expected %s.", FormatFullAnchorRequirement(reference, true)))
	}
	v, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return 0, ParseError(fmt.Sprintf("Invalid line reference. Expected %s.", FormatFullAnchorRequirement(reference, true)))
	}
	if v < 1 {
		return 0, ParseError(fmt.Sprintf("Line number must be >= 1, got %d in \"%s\".", v, reference))
	}
	return uint32(v), nil
}

// ValidateLineRef validates that a line reference exists in fileLines.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:64
func ValidateLineRef(line uint32, fileLines []string) *EditError {
	if line < 1 || int(line) > len(fileLines) {
		return MatchError(fmt.Sprintf("Line %d does not exist (file has %d lines)", line, len(fileLines)))
	}
	return nil
}

// FormatMismatchMessage formats the complete model-facing snapshot mismatch diagnostic.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:75
func FormatMismatchMessage(details MismatchDetails) string {
	pathClause := ""
	if details.Path != "" {
		pathClause = " for " + details.Path
	}
	context := FormatAnchoredContext(details.AnchorLines, details.FileLines)
	var lines []string
	if details.HashRecognized {
		lines = []string{
			fmt.Sprintf("Edit rejected%s: file changed between read and edit.", pathClause),
			fmt.Sprintf(
				"Section is bound to #%s, but the current file hashes to #%s. If a prior edit in this session modified this file, copy the [path#newhash] header from that edit's response; otherwise re-read the file with `read` to refresh the tag before retrying.",
				details.ExpectedFileHash, details.ActualFileHash,
			),
		}
	} else {
		if details.ExpectedFileHash == "" {
			lines = append(lines, fmt.Sprintf(
				"Edit rejected%s: the header needs this file's current tag, #%s.",
				pathClause, details.ActualFileHash,
			))
		} else {
			lines = append(lines, fmt.Sprintf(
				"Edit rejected%s: hash #%s is not from this session.",
				pathClause, details.ExpectedFileHash,
			))
		}
		for _, origin := range details.TagOriginPaths {
			lines = append(lines, fmt.Sprintf(
				"Hash #%s was issued in this session for %s.",
				details.ExpectedFileHash, origin,
			))
		}
		offerResend := len(context) > 0 && len(details.TagOriginPaths) == 0
		lines = append(lines, currentTagAdvice(details.ActualFileHash, offerResend))
	}
	if len(context) > 0 {
		lines = append(lines, "")
		lines = append(lines, context...)
	}
	return strings.Join(lines, "\n")
}

// currentTagAdvice matches crates/edit/src/modes/hashline/mismatch.rs:132.
func currentTagAdvice(actualFileHash string, offerResend bool) string {
	if offerResend {
		return fmt.Sprintf(
			"The current file hashes to #%s. If the rows below are the ones you mean, resend the edit under #%s; otherwise re-read the file with `read` and copy its [path#tag] header verbatim.",
			actualFileHash, actualFileHash,
		)
	}
	return fmt.Sprintf(
		"The current file hashes to #%s. Re-read the file with `read` and copy its [path#tag] header verbatim.",
		actualFileHash,
	)
}

// FormatHeaderOnlyMessage formats the diagnostic for input whose only content is a header.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:152
func FormatHeaderOnlyMessage(path, actualFileHash string) string {
	return fmt.Sprintf(
		"Edit rejected for %s: no ops follow the header; put them under this file's current tag, #%s.",
		path, actualFileHash,
	)
}

// TagPathMissingError rejects a missing header path whose name and tag match one read file.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:163
func TagPathMissingError(authored, issuedFor, tag string) *EditError {
	return RejectedError(
		RejectionFileNotFound,
		fmt.Sprintf(
			"File not found: %s. Hash #%s was issued in this session for %s; if that is the file you mean, re-issue the edit under [%s#%s]. Nothing was applied.",
			authored, tag, issuedFor, issuedFor, tag,
		),
	)
}

// TagFromOtherFileError rejects a file matching the tag that never held it in this session.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:178
func TagFromOtherFileError(authored, resolved, tag string, origins []string) *EditError {
	return RejectedError(
		RejectionOtherFileTag,
		fmt.Sprintf(
			"Edit rejected for %s: the header resolves to %s, but hash #%s was issued in this session for %s (identical content shares a tag). Nothing was applied. To edit the file you read, put its full path in the header; to edit %s, read it first.",
			authored, resolved, tag, strings.Join(origins, ", "), resolved,
		),
	)
}

// MismatchError constructs a typed tag rejection from MismatchDetails.
// upstream: crates/edit/src/modes/hashline/mismatch.rs:197
func MismatchError(details MismatchDetails) *EditError {
	var reason Rejection
	switch {
	case details.HashRecognized:
		reason = RejectionStaleTag
	case details.ExpectedFileHash == "":
		reason = RejectionMissingTag
	case len(details.TagOriginPaths) == 0:
		reason = RejectionUnknownTag
	default:
		reason = RejectionOtherFileTag
	}
	return RejectedError(reason, FormatMismatchMessage(details))
}
