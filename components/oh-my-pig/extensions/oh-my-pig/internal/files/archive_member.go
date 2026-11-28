package files

import (
	"fmt"
	"os"
	"strings"
)

// ArchivePathCandidate represents a candidate split of a path selector into an
// archive file path and a member path inside that archive.
// upstream: packages/utils/src/ar/open.ts:73-95
type ArchivePathCandidate struct {
	ArchivePath string
	SubPath     string
}

// ParseArchivePathCandidates splits filePath into every plausible
// (ArchivePath, SubPath) pair, longest archive prefix first.
// upstream: packages/utils/src/ar/open.ts:73-95
func ParseArchivePathCandidates(filePath string) []ArchivePathCandidate {
	raw := parseArchivePathCandidates(filePath)
	if len(raw) == 0 {
		return nil
	}
	out := make([]ArchivePathCandidate, len(raw))
	for i, c := range raw {
		out[i] = ArchivePathCandidate{
			ArchivePath: c.archivePath,
			SubPath:     c.subPath,
		}
	}
	return out
}

// ReadArchiveMember reads a single member's raw bytes from an archive on disk.
// It returns (data, openErr, readErr):
//   - openErr is non-nil when the archive itself cannot be opened or its format
//     is not supported by the Go standard-library port;
//   - readErr is non-nil when the archive opened successfully but the requested
//     member could not be read (e.g. missing member or member too large).
//
// upstream: packages/coding-agent/src/tools/grep.ts:326-341
func ReadArchiveMember(archiveAbs, memberPath string) (data []byte, openErr error, readErr error) {
	format := archiveFormatFromPath(archiveAbs)
	if format == "" {
		return nil, fmt.Errorf("Unsupported archive format: %s", archiveAbs), nil
	}
	if _, err := os.Stat(archiveAbs); err != nil {
		switch format {
		case "zip":
			return nil, fmt.Errorf("Invalid ZIP archive: missing end of central directory"), nil
		case "tar":
			return nil, fmt.Errorf("Not a valid tar archive: missing terminating zero block"), nil
		case "tar.gz":
			return nil, fmt.Errorf("unexpected end of file"), nil
		default:
			return nil, fmt.Errorf("Reading %s archives is not supported in this Go standard-library port (only zip, tar, and tar.gz/tgz are supported)", format), nil
		}
	}
	switch format {
	case "zip":
		norm := normalizeArchiveMemberPath(memberPath)
		if entries, err := listZipEntries(archiveAbs); err == nil {
			for _, e := range entries {
				if e.path == norm && e.isDir {
					return nil, nil, fmt.Errorf("Archive path '%s' is a directory", norm)
				}
			}
		} else {
			return nil, fmt.Errorf("Invalid ZIP archive: missing end of central directory"), nil
		}
		b, err := readZipMember(archiveAbs, norm)
		if err != nil {
			if isArchiveMemberReadErr(err) {
				return nil, nil, err
			}
			return nil, fmt.Errorf("Invalid ZIP archive: missing end of central directory"), nil
		}
		return b, nil, nil
	case "tar":
		norm := normalizeArchiveMemberPath(memberPath)
		if entries, err := listTarEntries(archiveAbs, false); err == nil {
			if len(entries) == 0 {
				return nil, fmt.Errorf("Not a valid tar archive: missing terminating zero block"), nil
			}
			for _, e := range entries {
				if e.path == norm && e.isDir {
					return nil, nil, fmt.Errorf("Archive path '%s' is a directory", norm)
				}
			}
		} else {
			return nil, fmt.Errorf("Not a valid tar archive: missing terminating zero block"), nil
		}
		b, err := readTarMember(archiveAbs, false, norm)
		if err != nil {
			if isArchiveMemberReadErr(err) {
				return nil, nil, err
			}
			return nil, fmt.Errorf("Not a valid tar archive: missing terminating zero block"), nil
		}
		return b, nil, nil
	case "tar.gz":
		norm := normalizeArchiveMemberPath(memberPath)
		if entries, err := listTarEntries(archiveAbs, true); err == nil {
			for _, e := range entries {
				if e.path == norm && e.isDir {
					return nil, nil, fmt.Errorf("Archive path '%s' is a directory", norm)
				}
			}
		} else {
			return nil, mapGzipTarOpenErr(err), nil
		}
		b, err := readTarMember(archiveAbs, true, norm)
		if err != nil {
			if isArchiveMemberReadErr(err) {
				return nil, nil, err
			}
			return nil, mapGzipTarOpenErr(err), nil
		}
		return b, nil, nil
	default:
		return nil, fmt.Errorf("Reading %s archives is not supported in this Go standard-library port (only zip, tar, and tar.gz/tgz are supported)", format), nil
	}
}

func normalizeArchiveMemberPath(memberPath string) string {
	s := strings.ReplaceAll(memberPath, "\\", "/")
	s = strings.TrimLeft(s, "/")
	s = strings.TrimRight(s, "/")
	return s
}

func mapGzipTarOpenErr(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "invalid header") {
		return fmt.Errorf("incorrect header check")
	}
	if strings.Contains(msg, "unexpected EOF") || msg == "EOF" {
		return fmt.Errorf("unexpected end of file")
	}
	return err
}

func isArchiveMemberReadErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "Archive file ") || strings.HasPrefix(msg, "Archive member ")
}
