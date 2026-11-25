package modes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ompImportOptions controls how an omp v3 JSONL session is translated to PiG v3 JSONL.
type ompImportOptions struct {
	// Full keeps every entry from the root instead of slicing to the active tail
	// starting at the latest compaction's firstKeptEntryId.
	Full bool
}

// ompImportResult summarizes a translated omp session file.
type ompImportResult struct {
	SourcePath     string
	OutputPath     string
	SessionID      string
	Title          string
	KeptEntries    int
	ConvertedCount int
	DroppedCount   int
	TrimmedPrefix  int
	OutputBytes    int64
}

// handleImportOmpCommand implements `/import-omp [path|latest|--full]`.
// Translates an omp session JSONL into PiG v3 JSONL and switches the live session to it.
func (m *manager) handleImportOmpCommand(ctx sdk.Context, args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	var opts ompImportOptions
	var target string
	for _, f := range fields {
		switch f {
		case "--full":
			opts.Full = true
		case "--help", "-h":
			notifyInfo(ctx, "Usage: /import-omp [latest|<path-to-session.jsonl>] [--full]\n"+
				"Translates an omp session JSONL into PiG v3 format and switches to it.\n"+
				"Defaults to the newest omp session for the current working directory (active tail from last compaction; pass --full for full history).")
			return nil
		default:
			if target == "" {
				target = f
			}
		}
	}

	cwd := strings.TrimSpace(ctx.Cwd())
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	srcPath, err := resolveOmpSessionPath(target, cwd)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("import-omp: %v", err))
		return nil
	}

	// Write translated file into the active PiG session directory (or temp dir if none).
	outDir := ""
	if cur, err := ctx.GetSessionFile(); err == nil && cur != nil && strings.TrimSpace(*cur) != "" {
		outDir = filepath.Dir(strings.TrimSpace(*cur))
	} else if dir, err := ctx.SessionManager().GetSessionDir(); err == nil && strings.TrimSpace(dir) != "" {
		outDir = strings.TrimSpace(dir)
	}
	if outDir == "" {
		outDir = os.TempDir()
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		notifyError(ctx, fmt.Sprintf("import-omp: create session dir: %v", err))
		return nil
	}

	base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	outPath := filepath.Join(outDir, fmt.Sprintf("%s-omp-import-%d.jsonl", base, time.Now().Unix()))

	res, err := translateOmpSessionFile(srcPath, outPath, opts)
	if err != nil {
		_ = os.Remove(outPath)
		notifyError(ctx, fmt.Sprintf("import-omp: translate failed: %v", err))
		return nil
	}

	cancelled, err := ctx.SwitchSession(outPath, nil)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("import-omp: translated to %s (%d entries, %.1f KiB) but SwitchSession failed: %v",
			outPath, res.KeptEntries, float64(res.OutputBytes)/1024.0, err))
		return nil
	}
	if cancelled.Cancelled {
		notifyInfo(ctx, fmt.Sprintf("import-omp: switch cancelled (translated file kept at %s).", outPath))
		return nil
	}

	titlePart := ""
	if res.Title != "" {
		titlePart = fmt.Sprintf(" %q", res.Title)
	}
	notifyInfo(ctx, fmt.Sprintf("Imported omp session%s (%d entries, %d converted, %d dropped, %.1f KiB).",
		titlePart, res.KeptEntries, res.ConvertedCount, res.DroppedCount, float64(res.OutputBytes)/1024.0))
	return nil
}

// resolveOmpSessionPath finds the requested omp session file.
// Empty or "latest" picks the newest .jsonl in ~/.omp/agent/sessions matching cwd.
func resolveOmpSessionPath(arg, cwd string) (string, error) {
	if arg != "" && arg != "latest" {
		p := arg
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		if !filepath.IsAbs(p) && cwd != "" {
			p = filepath.Join(cwd, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return newestJSONLInDir(p)
		}
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home dir: %w", err)
	}
	sessionsRoot := filepath.Join(home, ".omp", "agent", "sessions")
	candidates := candidateOmpSessionDirs(sessionsRoot, cwd, home)
	for _, dir := range candidates {
		if path, err := newestJSONLInDir(dir); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no omp session found for %s under %s", cwd, sessionsRoot)
}

func candidateOmpSessionDirs(sessionsRoot, cwd, home string) []string {
	var dirs []string
	add := func(name string) {
		if name == "" {
			return
		}
		full := filepath.Join(sessionsRoot, name)
		for _, existing := range dirs {
			if existing == full {
				return
			}
		}
		dirs = append(dirs, full)
	}
	if cwd != "" {
		absCwd, err := filepath.Abs(cwd)
		if err == nil {
			if rel, err := filepath.Rel(home, absCwd); err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				enc := "-" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(rel)
				add(enc)
			}
			// Legacy --abs-path-- format
			trimmed := strings.TrimLeft(absCwd, "/\\")
			legacy := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(trimmed) + "--"
			add(legacy)
		}
	}
	return dirs
}

func newestJSONLInDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type fileMod struct {
		path string
		mod  time.Time
	}
	var files []fileMod
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileMod{
			path: filepath.Join(dir, e.Name()),
			mod:  info.ModTime(),
		})
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no .jsonl files in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod.After(files[j].mod)
	})
	return files[0].path, nil
}

type translatedEntry struct {
	ID               string
	ParentID         *string
	Type             string
	FirstKeptEntryID string
	Raw              []byte
}

// translateOmpSessionFile converts an omp session JSONL file into a PiG v3 JSONL file.
func translateOmpSessionFile(srcPath, dstPath string, opts ompImportOptions) (ompImportResult, error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return ompImportResult{}, err
	}
	defer in.Close()

	var headerBytes []byte
	var res ompImportResult
	res.SourcePath = srcPath
	res.OutputPath = dstPath

	remap := make(map[string]*string)
	resolveParent := func(pid *string) *string {
		if pid == nil {
			return nil
		}
		cur := pid
		for steps := 0; cur != nil && steps < 10000; steps++ {
			next, ok := remap[*cur]
			if !ok {
				break
			}
			cur = next
		}
		return cur
	}

	var translated []translatedEntry
	var lastTitle, lastModel, lastThinking *translatedEntry
	lastCompIdx := -1

	reader := bufio.NewReaderSize(in, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return ompImportResult{}, err
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var obj map[string]any
			if jerr := json.Unmarshal(trimmed, &obj); jerr != nil {
				res.DroppedCount++
			} else {
				typ, _ := obj["type"].(string)
				switch typ {
				case "title":
					if t, _ := obj["title"].(string); t != "" {
						res.Title = t
					}
					res.DroppedCount++

				case "session":
					if headerBytes != nil {
						res.DroppedCount++
					} else {
						hdr := map[string]any{
							"type":      "session",
							"version":   3,
							"id":        obj["id"],
							"timestamp": obj["timestamp"],
							"cwd":       obj["cwd"],
						}
						if ps, ok := obj["parentSession"]; ok && ps != nil && ps != "" {
							hdr["parentSession"] = ps
						}
						if sid, _ := obj["id"].(string); sid != "" {
							res.SessionID = sid
						}
						if t, _ := obj["title"].(string); t != "" && res.Title == "" {
							res.Title = t
						}
						hb, merr := json.Marshal(hdr)
						if merr != nil {
							return ompImportResult{}, merr
						}
						headerBytes = hb
					}

				case "model_usage", "ttsr_injection":
					eid, _ := obj["id"].(string)
					pid := resolveParent(optStringPtr(obj["parentId"]))
					if eid != "" {
						remap[eid] = pid
					}
					res.DroppedCount++

				default:
					eid, _ := obj["id"].(string)
					pid := resolveParent(optStringPtr(obj["parentId"]))
					if pid == nil {
						obj["parentId"] = nil
					} else {
						obj["parentId"] = *pid
					}

					var outObj map[string]any
					outType := typ

					switch typ {
					case "title_change":
						titleStr, _ := obj["title"].(string)
						if titleStr != "" {
							res.Title = titleStr
						}
						outType = "session_info"
						outObj = map[string]any{
							"type":      "session_info",
							"id":        eid,
							"parentId":  obj["parentId"],
							"timestamp": obj["timestamp"],
							"name":      titleStr,
						}
						res.ConvertedCount++

					case "model_change":
						if modelStr, ok := obj["model"].(string); ok && obj["modelId"] == nil {
							prov, mid := splitProviderModel(modelStr)
							outObj = map[string]any{
								"type":      "model_change",
								"id":        eid,
								"parentId":  obj["parentId"],
								"timestamp": obj["timestamp"],
								"provider":  prov,
								"modelId":   mid,
							}
							res.ConvertedCount++
						} else {
							outObj = obj
						}

					case "message":
						if msg, ok := obj["message"].(map[string]any); ok {
							role, _ := msg["role"].(string)
							if role == "custom" || role == "developer" {
								ct, _ := msg["customType"].(string)
								if ct == "" {
									ct = "omp:" + role
								}
								disp, _ := msg["display"].(bool)
								content := msg["content"]
								if content == nil {
									content = ""
								}
								outType = "custom_message"
								outObj = map[string]any{
									"type":       "custom_message",
									"id":         eid,
									"parentId":   obj["parentId"],
									"timestamp":  obj["timestamp"],
									"customType": ct,
									"content":    content,
									"display":    disp,
								}
								res.ConvertedCount++
							} else {
								outObj = obj
							}
						} else {
							outObj = obj
						}

					case "compaction":
						if fk, ok := obj["firstKeptEntryId"].(string); ok && fk != "" {
							if resolved := resolveParent(&fk); resolved != nil {
								obj["firstKeptEntryId"] = *resolved
							}
						}
						outObj = obj

					default:
						outObj = obj
					}

					raw, merr := json.Marshal(outObj)
					if merr != nil {
						return ompImportResult{}, merr
					}
					fk, _ := outObj["firstKeptEntryId"].(string)
					te := translatedEntry{
						ID:               eid,
						ParentID:         pid,
						Type:             outType,
						FirstKeptEntryID: fk,
						Raw:              raw,
					}
					translated = append(translated, te)
					idx := len(translated) - 1
					switch outType {
					case "session_info":
						cp := translated[idx]
						lastTitle = &cp
					case "model_change":
						cp := translated[idx]
						lastModel = &cp
					case "thinking_level_change":
						cp := translated[idx]
						lastThinking = &cp
					case "compaction":
						lastCompIdx = idx
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	if headerBytes == nil {
		return ompImportResult{}, errors.New("missing session header record")
	}

	// Select entries to emit. By default slice from the latest compaction's
	// firstKeptEntryId so giant multi-hundred-MiB sessions stay under PiG's
	// 128 MiB extension IPC frame limit while preserving 100% of active LLM context.
	startIdx := 0
	if !opts.Full && lastCompIdx >= 0 {
		fk := translated[lastCompIdx].FirstKeptEntryID
		fkIdx := lastCompIdx
		if fk != "" {
			for i := 0; i <= lastCompIdx; i++ {
				if translated[i].ID == fk {
					fkIdx = i
					break
				}
			}
		}
		startIdx = fkIdx
		res.TrimmedPrefix = startIdx
	}

	var toWrite [][]byte
	toWrite = append(toWrite, headerBytes)

	var prevID *string
	if startIdx > 0 {
		keptIDs := make(map[string]struct{}, len(translated)-startIdx)
		for _, e := range translated[startIdx:] {
			if e.ID != "" {
				keptIDs[e.ID] = struct{}{}
			}
		}
		for _, meta := range []*translatedEntry{lastTitle, lastModel, lastThinking} {
			if meta == nil {
				continue
			}
			if _, already := keptIDs[meta.ID]; already {
				continue
			}
			rewritten, err := setParentIDRaw(meta.Raw, prevID)
			if err != nil {
				return ompImportResult{}, err
			}
			toWrite = append(toWrite, rewritten)
			idCopy := meta.ID
			prevID = &idCopy
		}
	}

	for i, e := range translated[startIdx:] {
		raw := e.Raw
		if i == 0 && startIdx > 0 {
			rewritten, err := setParentIDRaw(raw, prevID)
			if err != nil {
				return ompImportResult{}, err
			}
			raw = rewritten
		}
		toWrite = append(toWrite, raw)
	}

	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ompImportResult{}, err
	}
	bw := bufio.NewWriterSize(out, 256*1024)
	var written int64
	for _, line := range toWrite {
		n, werr := bw.Write(line)
		written += int64(n)
		if werr != nil {
			_ = out.Close()
			return ompImportResult{}, werr
		}
		if werr := bw.WriteByte('\n'); werr != nil {
			_ = out.Close()
			return ompImportResult{}, werr
		}
		written++
	}
	if err := bw.Flush(); err != nil {
		_ = out.Close()
		return ompImportResult{}, err
	}
	if err := out.Close(); err != nil {
		return ompImportResult{}, err
	}

	res.KeptEntries = len(toWrite) - 1 // exclude header
	res.OutputBytes = written
	return res, nil
}

func optStringPtr(v any) *string {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

func splitProviderModel(model string) (string, string) {
	if idx := strings.IndexByte(model, '/'); idx > 0 {
		return model[:idx], model[idx+1:]
	}
	return "", model
}

func setParentIDRaw(raw []byte, parentID *string) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if parentID == nil {
		obj["parentId"] = nil
	} else {
		obj["parentId"] = *parentID
	}
	return json.Marshal(obj)
}
