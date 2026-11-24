package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// executeWrite implements omp's `write` tool for local files.
// upstream: packages/coding-agent/src/tools/write.ts
func executeWrite(ctx sdk.Context, store *hashline.Store, cwd, rawPath string, content *string) (sdk.ToolResult, error) {
	unwrapped := unwrapHashlineHeaderPath(rawPath)
	if content == nil {
		return sdk.ToolResult{}, fmt.Errorf("content is required for %s.", unwrapped)
	}
	text := *content

	absPath := resolveToCwd(unwrapped, cwd)
	displayPath := formatPathRelativeToCwd(absPath, cwd)

	var resultText string
	var details map[string]any
	writeFn := func() error {
		var oldText string
		if info, err := os.Stat(absPath); err == nil {
			if info.IsDir() {
				return fmt.Errorf("Cannot write '%s': it is a directory.", displayPath)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Cannot write '%s': not a regular file or directory.", displayPath)
			}
			if prevBytes, readErr := os.ReadFile(absPath); readErr == nil {
				oldText = string(prevBytes)
			}
		}

		parent := filepath.Dir(absPath)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(absPath, []byte(text), 0o644); err != nil {
			return err
		}

		_, stripped := hashline.StripBOM(text)
		normalized := hashline.NormalizeToLF(stripped)
		tag := store.RecordSnapshot(absPath, normalized)
		header := hashline.FormatHeader(displayPath, tag)
		writeLine := fmt.Sprintf("Successfully wrote %d bytes to %s", len(text), displayPath)
		resultText = header + "\n" + writeLine

		patch := generateUnifiedPatch(ctx, displayPath, oldText, text)
		details = map[string]any{
			"patch":        patch,
			"diff":         patch,
			"resolvedPath": absPath,
		}
		return nil
	}

	if err := withMutationQueue(ctx, absPath, writeFn); err != nil {
		return sdk.ToolResult{}, err
	}
	return sdk.ToolResult{Content: resultText, Details: details}, nil
}

// withMutationQueue serializes writes through sdk.Context.WithFileMutationQueue when
// connected to a host, falling back to direct execution in standalone unit tests.
func withMutationQueue(ctx sdk.Context, absPath string, fn func() error) error {
	if os.Getenv("PIG_EXT_SOCKET") == "" {
		return fn()
	}
	return ctx.WithFileMutationQueue(absPath, fn)
}

// executeEdit implements omp's hashline `edit` tool.
// upstream: packages/coding-agent/src/edit/index.ts, crates/pi-edit/src/session.rs
func executeEdit(ctx sdk.Context, store *hashline.Store, cwd, input string) (sdk.ToolResult, error) {
	sections, err := hashline.ParseInput(input)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	clipboard := store.StartBatchClipboard()
	var results []string
	var perFile []map[string]any
	var patches []string

	for _, sec := range sections {
		absPath := resolveToCwd(sec.Path, cwd)
		displayPath := formatPathRelativeToCwd(absPath, cwd)

		var secText string
		var secPatch string
		var secEntry map[string]any
		err := withMutationQueue(ctx, absPath, func() error {
			// Whole-file delete (REM) without line edits.
			if sec.FileOp.Kind == hashline.FileOpDelete && len(sec.Ops) == 0 {
				if err := verifyTagIfProvided(store, absPath, displayPath, sec); err != nil {
					return err
				}
				var oldText string
				if prevBytes, readErr := os.ReadFile(absPath); readErr == nil {
					oldText = string(prevBytes)
				}
				if err := os.Remove(absPath); err != nil {
					return err
				}
				secText = fmt.Sprintf("Deleted %s", displayPath)
				secPatch = generateUnifiedPatch(ctx, displayPath, oldText, "")
				secEntry = map[string]any{
					"path":    displayPath,
					"diff":    secPatch,
					"op":      "delete",
					"oldText": oldText,
				}
				return nil
			}
			rawBytes, err := os.ReadFile(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("File not found: %s", displayPath)
				}
				return err
			}

			rawText := string(rawBytes)
			bom, stripped := hashline.StripBOM(rawText)
			ending := hashline.DetectLineEnding(stripped)
			normalized := hashline.NormalizeToLF(stripped)
			actualHash := hashline.FileHash(normalized)
			// Validate snapshot tag, attempting in-session snapshot recovery before rejecting.
			if !sec.HasHash {
				return fmt.Errorf("Edit rejected for %s: missing required #TAG in [%s#TAG] header. Re-read the file with `read` to copy a current [path#tag] header.", displayPath, displayPath)
			}

			var applied hashline.ApplyResult
			if sec.FileHash != actualHash {
				recovered, ok, recErr := hashline.TryRecover(store, absPath, displayPath, normalized, sec.FileHash, sec.Ops, clipboard)
				if recErr != nil {
					return recErr
				}
				if !ok {
					recognized := store.HasHashForPath(absPath, sec.FileHash)
					var origins []string
					if !recognized {
						origins = store.TagOriginPaths(sec.FileHash)
					}
					msg := hashline.FormatMismatchMessage(hashline.MismatchDetails{
						Path:             displayPath,
						ExpectedFileHash: sec.FileHash,
						ActualFileHash:   actualHash,
						FileLines:        hashline.SplitAddressableFileLines(normalized),
						AnchorLines:      hashline.CollectAnchorLines(sec.Ops),
						HashRecognized:   recognized,
						TagOriginPaths:   origins,
					})
					return fmt.Errorf("%s", msg)
				}
				applied = recovered
			} else {
				if err := hashline.CheckSeenLines(store, absPath, displayPath, sec.FileHash, normalized, sec.Ops); err != nil {
					return err
				}
				var applyErr error
				applied, applyErr = hashline.ApplyOps(normalized, displayPath, sec.Ops, clipboard)
				if applyErr != nil {
					return applyErr
				}
			}

			if !applied.Changed && sec.FileOp.Kind == hashline.FileOpNone {
				return fmt.Errorf("No changes made to %s.", displayPath)
			}

			restored := bom + hashline.RestoreLineEndings(applied.Content, ending)

			// Handle MV (rename after edits).
			if sec.FileOp.Kind == hashline.FileOpMove {
				destAbs := resolveToCwd(sec.FileOp.Dest, cwd)
				destDisplay := formatPathRelativeToCwd(destAbs, cwd)
				if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(destAbs, []byte(restored), 0o644); err != nil {
					return err
				}
				if destAbs != absPath {
					if err := os.Remove(absPath); err != nil {
						return err
					}
				}
				newTag := store.RecordSnapshotWithSeenLines(destAbs, applied.Content, hashline.SeenLinesFromBody(applied.Preview))
				secText = formatEditSectionOutput(hashline.FormatHeader(destDisplay, newTag), fmt.Sprintf("Moved to %s", destDisplay), applied)
				secPatch = generateUnifiedPatch(ctx, destDisplay, rawText, restored)
				secEntry = map[string]any{
					"path":       destDisplay,
					"sourcePath": displayPath,
					"move":       destDisplay,
					"diff":       secPatch,
					"op":         "update",
					"oldText":    rawText,
					"newText":    restored,
				}
				if applied.FirstChangedLine > 0 {
					secEntry["firstChangedLine"] = applied.FirstChangedLine
				}
				return nil
			}

			// Normal update.
			if err := os.WriteFile(absPath, []byte(restored), 0o644); err != nil {
				return err
			}
			newTag := store.RecordSnapshotWithSeenLines(absPath, applied.Content, hashline.SeenLinesFromBody(applied.Preview))
			secText = formatEditSectionOutput(hashline.FormatHeader(displayPath, newTag), "", applied)
			secPatch = generateUnifiedPatch(ctx, displayPath, rawText, restored)
			secEntry = map[string]any{
				"path":    displayPath,
				"diff":    secPatch,
				"op":      "update",
				"oldText": rawText,
				"newText": restored,
			}
			if applied.FirstChangedLine > 0 {
				secEntry["firstChangedLine"] = applied.FirstChangedLine
			}
			return nil
		})
		if err != nil {
			return sdk.ToolResult{}, err
		}
		results = append(results, secText)
		if secPatch != "" {
			patches = append(patches, secPatch)
		}
		if secEntry != nil {
			perFile = append(perFile, secEntry)
		}
	}

	store.CommitClipboard(clipboard)
	combinedPatch := strings.Join(patches, "")
	details := map[string]any{
		"patch": combinedPatch,
		"diff":  combinedPatch,
	}
	if len(perFile) == 1 {
		entry := perFile[0]
		details["path"] = entry["path"]
		details["op"] = entry["op"]
		if fcl, ok := entry["firstChangedLine"]; ok {
			details["firstChangedLine"] = fcl
		}
		if move, ok := entry["move"]; ok {
			details["move"] = move
		}
		if src, ok := entry["sourcePath"]; ok {
			details["sourcePath"] = src
		}
		if ot, ok := entry["oldText"]; ok {
			details["oldText"] = ot
		}
		if nt, ok := entry["newText"]; ok {
			details["newText"] = nt
		}
	} else if len(perFile) > 1 {
		details["perFileResults"] = perFile
	}
	return sdk.ToolResult{Content: strings.Join(results, "\n\n"), Details: details}, nil
}

// formatEditSectionOutput formats one edited section matching crates/pi-edit/src/session.rs:491.
func formatEditSectionOutput(header, movedLine string, applied hashline.ApplyResult) string {
	var lines []string
	if header != "" {
		lines = append(lines, header)
	}
	for _, res := range applied.BlockResolutions {
		lines = append(lines, hashline.FormatBlockResolution(res))
	}
	if movedLine != "" {
		lines = append(lines, movedLine)
	}
	if applied.Preview != "" {
		lines = append(lines, applied.Preview)
	}
	body := strings.Join(lines, "\n")
	if len(applied.Warnings) > 0 {
		body += "\n\nWarnings:\n" + strings.Join(applied.Warnings, "\n")
	}
	return body
}

func verifyTagIfProvided(store *hashline.Store, absPath, displayPath string, sec hashline.Section) error {
	if !sec.HasHash {
		return nil
	}
	rawBytes, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	_, stripped := hashline.StripBOM(string(rawBytes))
	normalized := hashline.NormalizeToLF(stripped)
	actualHash := hashline.FileHash(normalized)
	if sec.FileHash != actualHash {
		recognized := store.HasHashForPath(absPath, sec.FileHash)
		var origins []string
		if !recognized {
			origins = store.TagOriginPaths(sec.FileHash)
		}
		msg := hashline.FormatMismatchMessage(hashline.MismatchDetails{
			Path:             displayPath,
			ExpectedFileHash: sec.FileHash,
			ActualFileHash:   actualHash,
			FileLines:        hashline.SplitAddressableFileLines(normalized),
			HashRecognized:   recognized,
			TagOriginPaths:   origins,
		})
		return fmt.Errorf("%s", msg)
	}
	return nil
}
