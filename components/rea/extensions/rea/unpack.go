package rea

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// executableBits is whether the file system records the executable bit that verify compares.
var executableBits = runtime.GOOS != "windows"

// sentinelName is the file an unpacked payload directory holds last: the archive's sha256. A directory without it, or with
// another digest, is not used.
const sentinelName = ".rea-payload"

// stagingPrefix names the temporary directories an unpack writes into before it renames one into place.
const stagingPrefix = ".unpack-"

// dirName is the content-addressed directory of a payload under the cache root.
func dirName(info payloadInfo) string { return info.ArchiveSha256[:16] }

// layout is where an unpacked payload keeps what the registration names.
type layout struct {
	dir, node, entry string
}

func layoutOf(root string, info payloadInfo) layout {
	dir := filepath.Join(root, dirName(info))
	return layout{dir: dir, node: filepath.Join(dir, filepath.FromSlash(info.Node.Executable)), entry: filepath.Join(dir, filepath.FromSlash(info.Entry))}
}

// complete reports whether dir holds the unpacked payload whose archive digest is sum.
func complete(dir, sum string) bool {
	data, err := os.ReadFile(filepath.Join(dir, sentinelName))
	return err == nil && strings.TrimSpace(string(data)) == sum
}

// ensureUnpacked returns the payload's layout under root, unpacking the archive first when no complete copy is there. It
// unpacks into a staging directory, writes the sentinel last and renames the directory into place, so a reader sees a whole
// payload or none. When another process renamed its copy into place first, that copy is used and this one is removed.
func ensureUnpacked(root string, src payloadSource, info payloadInfo) (layout, bool, error) {
	l := layoutOf(root, info)
	if complete(l.dir, info.ArchiveSha256) {
		return l, false, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return layout{}, false, fmt.Errorf("REA cache %s: %w", root, err)
	}
	staging, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		return layout{}, false, fmt.Errorf("REA cache %s: %w", root, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := extract(src, info, staging); err != nil {
		return layout{}, false, err
	}
	if err := os.WriteFile(filepath.Join(staging, sentinelName), []byte(info.ArchiveSha256+"\n"), 0o644); err != nil {
		return layout{}, false, err
	}
	if err := os.Rename(staging, l.dir); err != nil {
		if complete(l.dir, info.ArchiveSha256) {
			return l, false, nil
		}
		// Only a whole payload is ever renamed into place, so a directory here without the sentinel was left by something else.
		if rmErr := os.RemoveAll(l.dir); rmErr != nil {
			return layout{}, false, fmt.Errorf("replace incomplete %s: %w", l.dir, rmErr)
		}
		if err := os.Rename(staging, l.dir); err != nil {
			return layout{}, false, fmt.Errorf("install %s: %w", l.dir, err)
		}
	}
	return l, true, nil
}

// tarEntries reads the embedded archive and calls visit for every entry with the entry's content (nil for a directory). It
// also checks the archive's sha256 against the payload description once the archive is read to its end.
func tarEntries(src payloadSource, info payloadInfo, visit func(name string, header *tar.Header, content io.Reader) error) error {
	compressed, err := src.archive()
	if err != nil {
		return fmt.Errorf("open REA payload: %w", err)
	}
	defer compressed.Close()
	digest := sha256.New()
	decoder, err := zstd.NewReader(io.TeeReader(compressed, digest), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read REA payload: %w", err)
		}
		name, err := entryName(header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = visit(name, header, nil)
		case tar.TypeReg:
			err = visit(name, header, reader)
		default:
			err = fmt.Errorf("REA payload entry %s: type %q is not packed", header.Name, header.Typeflag)
		}
		if err != nil {
			return err
		}
	}
	// Drain what the tar reader left (the end-of-archive blocks and zstd's frame end) so the digest covers the whole file.
	if _, err := io.Copy(io.Discard, decoder); err != nil {
		return fmt.Errorf("read REA payload: %w", err)
	}
	if _, err := io.Copy(digest, compressed); err != nil {
		return fmt.Errorf("read REA payload: %w", err)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != info.ArchiveSha256 {
		return fmt.Errorf("the embedded REA payload's sha256 is %s, its description says %s", got, info.ArchiveSha256)
	}
	return nil
}

// entryName is a tar name as a local slash path, or an error for anything that could leave the target directory.
func entryName(raw string) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if name == "" || !filepath.IsLocal(filepath.FromSlash(name)) || strings.Contains(name, `\`) || name == sentinelName {
		return "", fmt.Errorf("REA payload entry %q is not a local path", raw)
	}
	return name, nil
}

func fileMode(header *tar.Header) os.FileMode {
	if header.Mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// extract writes the archive's directories and regular files under dir.
func extract(src payloadSource, info payloadInfo, dir string) error {
	buffer := make([]byte, 1<<20)
	return tarEntries(src, info, func(name string, header *tar.Header, content io.Reader) error {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if content == nil {
			return os.MkdirAll(path, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode(header))
		if err != nil {
			return err
		}
		if _, err := io.CopyBuffer(f, content, buffer); err != nil {
			_ = f.Close()
			return fmt.Errorf("unpack %s: %w", name, err)
		}
		return f.Close()
	})
}

// verify checks an unpacked payload against the embedded archive: every file's bytes and executable bit, no missing or extra
// files, the archive's digest, and the tree digest payload.mjs recorded. It returns the number of files checked.
func verify(src payloadSource, info payloadInfo, dir string) (int, error) {
	if !complete(dir, info.ArchiveSha256) {
		return 0, fmt.Errorf("%s is not an unpacked copy of this payload (no %s naming %s)", dir, sentinelName, info.ArchiveSha256[:16])
	}
	type treeLine struct{ name, line string }
	var tree []treeLine
	seen := map[string]bool{}
	var problems []string
	checked := 0
	err := tarEntries(src, info, func(name string, header *tar.Header, content io.Reader) error {
		seen[name] = true
		path := filepath.Join(dir, filepath.FromSlash(name))
		stat, err := os.Stat(path)
		if err != nil {
			problems = append(problems, name+": missing")
			if content != nil {
				_, _ = io.Copy(io.Discard, content)
			}
			return nil
		}
		if content == nil {
			if !stat.IsDir() {
				problems = append(problems, name+": not a directory")
			}
			return nil
		}
		want := sha256.New()
		if _, err := io.Copy(want, content); err != nil {
			return err
		}
		wantSum := hex.EncodeToString(want.Sum(nil))
		if name != "payload.json" {
			tree = append(tree, treeLine{name, strconv.FormatInt(int64(fileMode(header)), 8) + " " + wantSum + " " + name + "\n"})
		}
		got, err := fileSha256(path)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			return nil
		}
		if got != wantSum {
			problems = append(problems, name+": content differs")
		}
		if executableBits && (stat.Mode()&0o111 != 0) != (fileMode(header)&0o111 != 0) {
			problems = append(problems, name+": executable bit differs")
		}
		checked++
		return nil
	})
	if err != nil {
		return 0, err
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == sentinelName {
			return nil
		}
		if !seen[rel] {
			problems = append(problems, rel+": not in the payload")
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	slices.SortFunc(tree, func(a, b treeLine) int { return strings.Compare(a.name, b.name) })
	digest := sha256.New()
	for _, t := range tree {
		digest.Write([]byte(t.line))
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != info.TreeSha256 {
		problems = append(problems, fmt.Sprintf("tree digest %s, payload.mjs recorded %s", got, info.TreeSha256))
	}
	if len(problems) > 0 {
		shown := problems[:min(len(problems), 10)]
		more := ""
		if len(problems) > len(shown) {
			more = fmt.Sprintf(" (and %d more)", len(problems)-len(shown))
		}
		return checked, fmt.Errorf("%s differs from the embedded payload: %s%s", dir, strings.Join(shown, "; "), more)
	}
	return checked, nil
}

func fileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// staleStaging is how old a staging directory must be before gc removes it: an unpack takes seconds, so an hour-old one was
// left by a process that ended.
const staleStaging = time.Hour

// gc removes every payload directory under root except keep, and staging directories older than staleStaging. It returns
// the removed names.
func gc(root, keep string, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == keep {
			continue
		}
		if strings.HasPrefix(name, stagingPrefix) {
			info, err := entry.Info()
			if err != nil || now.Sub(info.ModTime()) < staleStaging {
				continue
			}
		} else if !isPayloadDirName(name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, errors.Join(errs...)
}

func isPayloadDirName(name string) bool {
	if len(name) != 16 || strings.ToLower(name) != name {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}
