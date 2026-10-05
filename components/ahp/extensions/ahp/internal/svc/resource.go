package svc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// ResourceOptions configures a ResourceService.
type ResourceOptions struct {
	// Paths maps and confines URIs. A nil policy means unrestricted, which the extension only uses
	// when the user explicitly asked for it.
	Paths *PathPolicy
	// ReadVirtual answers reads of non-file resources (nil result means "not mine").
	ReadVirtual func(ahptypes.ResourceReadParams) (*ahptypes.ResourceReadResult, error)
}

// ResourceService is the filesystem behind the resource* commands (port of resource-service.ts).
// It implements host.ResourceHandler.
type ResourceService struct {
	paths       *PathPolicy
	readVirtual func(ahptypes.ResourceReadParams) (*ahptypes.ResourceReadResult, error)

	mu    sync.Mutex
	locks map[string]*pathLock
}

type pathLock struct {
	mu   sync.Mutex
	refs int
}

// NewResourceService creates the service.
func NewResourceService(opts ResourceOptions) *ResourceService {
	paths := opts.Paths
	if paths == nil {
		paths = &PathPolicy{}
	}
	return &ResourceService{paths: paths, readVirtual: opts.ReadVirtual, locks: map[string]*pathLock{}}
}

func pathErr(code int, msg string) *wire.Error { return wire.Coded(code, msg) }

// translate maps a filesystem error onto the protocol error a client can branch on.
func translate(err error, uri string) error {
	var we *wire.Error
	if errors.As(err, &we) {
		return err
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return wire.NotFound(uri)
	case errors.Is(err, fs.ErrPermission):
		return pathErr(wire.CodePermissionDenied, "Permission denied: "+uri)
	case errors.Is(err, fs.ErrExist):
		return pathErr(wire.CodeAlreadyExists, "Already exists: "+uri)
	case errors.Is(err, syscall.ENOTEMPTY):
		return pathErr(wire.CodeConflict, "Directory not empty: "+uri)
	case errors.Is(err, syscall.EISDIR):
		return wire.InvalidParams("Is a directory: " + uri)
	case errors.Is(err, syscall.ENOTDIR):
		return wire.InvalidParams("Not a directory: " + uri)
	}
	return pathErr(wire.CodePermissionDenied, err.Error())
}

func isUTF8Text(data []byte) bool { return utf8.Valid(data) && !bytes.Contains(data, []byte{0}) }

func strPtr(s string) *string { return &s }

// Read implements host.ResourceHandler.
func (s *ResourceService) Read(_ context.Context, p ahptypes.ResourceReadParams) (ahptypes.ResourceReadResult, error) {
	if s.readVirtual != nil {
		virtual, err := s.readVirtual(p)
		if err != nil {
			return ahptypes.ResourceReadResult{}, err
		}
		if virtual != nil {
			return *virtual, nil
		}
	}
	path, err := s.paths.PathFor(p.Uri)
	if err != nil {
		return ahptypes.ResourceReadResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ahptypes.ResourceReadResult{}, translate(err, p.Uri)
	}
	// Never claim UTF-8 when decoding would replace invalid bytes. Unknown extensions still
	// remain editable when their contents are valid text.
	text := isUTF8Text(data)
	result := ahptypes.ResourceReadResult{Encoding: ahptypes.ContentEncodingBase64}
	if text && (p.Encoding == nil || *p.Encoding != ahptypes.ContentEncodingBase64) {
		result.Encoding = ahptypes.ContentEncodingUtf8
		result.Data = string(data)
	} else {
		result.Data = base64.StdEncoding.EncodeToString(data)
	}
	if ct := contentTypeOf(path, text); ct != "" {
		result.ContentType = strPtr(ct)
	}
	return result, nil
}

func (s *ResourceService) lockPath(path string) func() {
	s.mu.Lock()
	l := s.locks[path]
	if l == nil {
		l = &pathLock{}
		s.locks[path] = l
	}
	l.refs++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(s.locks, path)
		}
		s.mu.Unlock()
	}
}

// Write implements host.ResourceHandler.
func (s *ResourceService) Write(_ context.Context, p ahptypes.ResourceWriteParams) error {
	path, err := s.paths.PathFor(p.Uri)
	if err != nil {
		return err
	}
	var data []byte
	switch p.Encoding {
	case ahptypes.ContentEncodingBase64:
		data, err = base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			// Node's Buffer.from(…, "base64") is lenient; accept unpadded input too.
			if data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(p.Data, "=")); err != nil {
				return wire.InvalidParams("Invalid base64 data: " + p.Uri)
			}
		}
	case ahptypes.ContentEncodingUtf8:
		data = []byte(p.Data)
	default:
		return wire.InvalidParams("Unsupported content encoding: " + string(p.Encoding))
	}
	mode := ahptypes.ResourceWriteModeTruncate
	if p.Mode != nil {
		mode = *p.Mode
	}
	if mode != ahptypes.ResourceWriteModeTruncate && mode != ahptypes.ResourceWriteModeAppend && mode != ahptypes.ResourceWriteModeInsert {
		return wire.InvalidParams("Unsupported write mode: " + string(mode))
	}
	var position int64
	if p.Position != nil {
		position = *p.Position
	}
	if position < 0 {
		return wire.InvalidParams("Write position must be a non-negative integer")
	}
	unlock := s.lockPath(path)
	defer unlock()
	if err := writeLocked(path, p, data, mode, position); err != nil {
		return translate(err, p.Uri)
	}
	return nil
}

func writeLocked(path string, p ahptypes.ResourceWriteParams, data []byte, mode ahptypes.ResourceWriteMode, position int64) error {
	createOnly := p.CreateOnly != nil && *p.CreateOnly
	if createOnly && p.IfMatch == nil {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return err
		}
		return closeAfter(f, data)
	}
	if p.IfMatch != nil {
		current := ""
		exists := false
		if info, err := os.Stat(path); err == nil {
			current, exists = etagOf(info), true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if createOnly && exists {
			return pathErr(wire.CodeAlreadyExists, "Already exists: "+p.Uri)
		}
		if !exists || *p.IfMatch != current {
			return pathErr(wire.CodeConflict, "ifMatch precondition failed: "+p.Uri)
		}
	}
	if position == 0 && mode != ahptypes.ResourceWriteModeInsert {
		flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if mode == ahptypes.ResourceWriteModeAppend {
			flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(path, flag, 0o666)
		if err != nil {
			return err
		}
		return closeAfter(f, data)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	length := int64(len(existing))
	var offset int64
	if mode == ahptypes.ResourceWriteModeAppend {
		offset = length - position
		if offset < 0 {
			offset = 0
		}
	} else {
		offset = position
		if offset > length {
			offset = length
		}
	}
	updated := append([]byte{}, existing[:offset]...)
	updated = append(updated, data...)
	if mode != ahptypes.ResourceWriteModeTruncate {
		updated = append(updated, existing[offset:]...)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	return closeAfter(f, updated)
}

func closeAfter(f *os.File, data []byte) error {
	_, err := f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// List implements host.ResourceHandler: directories first, then names in locale order.
func (s *ResourceService) List(_ context.Context, uri string) (ahptypes.ResourceListResult, error) {
	path, err := s.paths.PathFor(uri)
	if err != nil {
		return ahptypes.ResourceListResult{}, err
	}
	found, err := os.ReadDir(path)
	if err != nil {
		return ahptypes.ResourceListResult{}, translate(err, uri)
	}
	entries := make([]ahptypes.DirectoryEntry, 0, len(found))
	for _, entry := range found {
		kind := "file"
		if s.isDirectoryEntry(path, entry) {
			kind = "directory"
		}
		entries = append(entries, ahptypes.DirectoryEntry{Name: entry.Name(), Type: kind})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Type == b.Type {
			return localeLess(a.Name, b.Name)
		}
		return a.Type == "directory"
	})
	return ahptypes.ResourceListResult{Entries: entries}, nil
}

func localeLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

func (s *ResourceService) isDirectoryEntry(parent string, entry fs.DirEntry) bool {
	if entry.Type()&fs.ModeSymlink == 0 {
		return entry.IsDir()
	}
	path := filepath.Join(parent, entry.Name())
	// DirectoryEntry has no symlink kind; an unresolved or forbidden link is safest as a
	// non-navigable file entry.
	if _, err := s.paths.PathFor(wire.PathToFileURI(path)); err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isoMillis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Resolve implements host.ResourceHandler.
func (s *ResourceService) Resolve(_ context.Context, p ahptypes.ResourceResolveParams) (ahptypes.ResourceResolveResult, error) {
	path, err := s.paths.PathFor(p.Uri)
	if err != nil {
		return ahptypes.ResourceResolveResult{}, err
	}
	follow := p.FollowSymlinks == nil || *p.FollowSymlinks
	var info os.FileInfo
	realPath := path
	if follow {
		info, err = os.Stat(path)
		if err == nil {
			realPath, err = filepath.EvalSymlinks(path)
		}
	} else {
		info, err = os.Lstat(path)
	}
	if err != nil {
		return ahptypes.ResourceResolveResult{}, translate(err, p.Uri)
	}
	kind := ahptypes.ResourceTypeFile
	switch {
	case info.IsDir():
		kind = ahptypes.ResourceTypeDirectory
	case info.Mode()&fs.ModeSymlink != 0:
		kind = ahptypes.ResourceTypeSymlink
	}
	size := info.Size()
	result := ahptypes.ResourceResolveResult{
		Uri:   wire.PathToFileURI(realPath),
		Type:  kind,
		Size:  &size,
		Mtime: strPtr(isoMillis(info.ModTime())),
		Ctime: strPtr(isoMillis(ctimeOf(info))),
		Etag:  strPtr(etagOf(info)),
	}
	if kind == ahptypes.ResourceTypeFile {
		if ct := contentTypeOf(path, false); ct != "" {
			result.ContentType = strPtr(ct)
		}
	}
	return result, nil
}

// Mkdir implements host.ResourceHandler (mkdir -p).
func (s *ResourceService) Mkdir(_ context.Context, p ahptypes.ResourceMkdirParams) error {
	path, err := s.paths.PathFor(p.Uri)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o777); err != nil {
		return translate(err, p.Uri)
	}
	return nil
}

// Delete implements host.ResourceHandler. A directory is only removed when recursion is asked
// for; the refusal is PermissionDenied so a client can tell it from "already exists".
func (s *ResourceService) Delete(_ context.Context, p ahptypes.ResourceDeleteParams) error {
	path, err := s.paths.PathFor(p.Uri)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return translate(err, p.Uri)
	}
	recursive := p.Recursive != nil && *p.Recursive
	if info.IsDir() {
		if !recursive {
			return pathErr(wire.CodePermissionDenied, "Path is a directory: rm returned EISDIR (rm '"+path+"')")
		}
		if err := os.RemoveAll(path); err != nil {
			return translate(err, p.Uri)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		return translate(err, p.Uri)
	}
	return nil
}

func (s *ResourceService) guardDestination(path, uri string, failIfExists *bool) error {
	if failIfExists == nil || !*failIfExists {
		return nil
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return translate(err, uri)
	}
	return pathErr(wire.CodeAlreadyExists, "Already exists: "+uri)
}

// Move implements host.ResourceHandler.
func (s *ResourceService) Move(_ context.Context, p ahptypes.ResourceMoveParams) error {
	source, err := s.paths.PathFor(p.Source)
	if err != nil {
		return err
	}
	destination, err := s.paths.PathFor(p.Destination)
	if err != nil {
		return err
	}
	if err := s.guardDestination(destination, p.Destination, p.FailIfExists); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return translate(err, p.Source)
	}
	return nil
}

// Copy implements host.ResourceHandler; symlinks inside a copied directory stay links.
func (s *ResourceService) Copy(_ context.Context, p ahptypes.ResourceCopyParams) error {
	source, err := s.paths.PathFor(p.Source)
	if err != nil {
		return err
	}
	destination, err := s.paths.PathFor(p.Destination)
	if err != nil {
		return err
	}
	if err := s.guardDestination(destination, p.Destination, p.FailIfExists); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return translate(err, p.Source)
	}
	if info.IsDir() {
		err = copyTree(source, destination)
	} else {
		err = copyFile(source, destination, info.Mode().Perm())
	}
	if err != nil {
		return translate(err, p.Source)
	}
	return nil
}

func copyFile(source, destination string, perm fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}
