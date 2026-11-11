package rea

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"runtime"
)

// payloadFS holds the payload of the target this Binary was built for: <goos>_<goarch>.tar.zst and its <goos>_<goarch>.json,
// written by components/rea/scripts/payload.mjs from the pins in rea.lock.json. payload/README.md keeps the directory present
// in a checkout without a payload, so the package compiles and its tests run; a Binary built that way reports the missing
// payload at startup and registers nothing.
//
//go:embed payload
var payloadFS embed.FS

// payloadInfo is <goos>_<goarch>.json: payload.json as packed into the archive, plus the archive's own digest and size.
type payloadInfo struct {
	Target string `json:"target"`
	Rea    struct {
		Package string `json:"package"`
		Version string `json:"version"`
		Tag     string `json:"tag,omitempty"`
		Commit  string `json:"commit,omitempty"`
		Dev     string `json:"dev,omitempty"`
	} `json:"rea"`
	Node struct {
		Version    string `json:"version"`
		Executable string `json:"executable"`
	} `json:"node"`
	Entry         string `json:"entry"`
	TreeSha256    string `json:"treeSha256"`
	Files         int    `json:"files"`
	Bytes         int64  `json:"bytes"`
	ArchiveSha256 string `json:"archiveSha256"`
	ArchiveBytes  int64  `json:"archiveBytes"`
}

// payloadSource is where a payload's description and archive come from: the embedded files in a Binary, a directory in tests.
type payloadSource struct {
	fsys fs.FS
	stem string
}

func embeddedPayload() payloadSource {
	return payloadSource{fsys: payloadFS, stem: "payload/" + runtime.GOOS + "_" + runtime.GOARCH}
}

func (s payloadSource) info() (payloadInfo, error) {
	data, err := fs.ReadFile(s.fsys, s.stem+".json")
	if err != nil {
		return payloadInfo{}, fmt.Errorf("this Binary carries no REA payload for %s/%s (%w); build the payload with components/rea/scripts/payload.mjs before pig piglet build", runtime.GOOS, runtime.GOARCH, err)
	}
	var info payloadInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return payloadInfo{}, fmt.Errorf("%s.json: %w", s.stem, err)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Target != want {
		return payloadInfo{}, fmt.Errorf("the embedded REA payload is for %s, this Binary runs on %s", info.Target, want)
	}
	if len(info.ArchiveSha256) != 64 || info.Entry == "" || info.Node.Executable == "" {
		return payloadInfo{}, fmt.Errorf("%s.json is incomplete", s.stem)
	}
	return info, nil
}

// archive opens the compressed archive. An embedded file reads from the executable's data without copying it.
func (s payloadSource) archive() (io.ReadCloser, error) {
	f, err := s.fsys.Open(s.stem + ".tar.zst")
	if err != nil {
		return nil, err
	}
	return f, nil
}
