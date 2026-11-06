package frontend

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// Tern's blob cache (https://docs.stencil.so/tern/protocol/transport.md,
// "Through Tern's blob cache"): a program on Tern's machine writes an
// image to $TERN_BLOB_DIR/<id> instead of sending its bytes through the
// pty, asks the blobs query which ids Tern holds, and sends only those
// missing with b as usual. The frame naming the image may go out before
// the reply. Images then cost the pty nothing, however large, and one that
// another pane sent is not written again.

// blobReplyWait bounds the wait for the blobs reply; without one in time
// the images go through the pty.
const blobReplyWait = 5 * time.Second

// flushBlobs hands the queued images to Tern before the frame that names
// them: through the blob cache when Tern offers it, else through the pty.
// Tern keeps blobs by their hash, so each goes out once per session.
func (s *session) flushBlobs() error {
	var cached []*fe.ViewImage
	for len(s.blobQueue) > 0 {
		img := s.blobQueue[0]
		if s.cacheBlob(img) {
			cached = append(cached, img)
		} else if _, err := s.tsp.Blob(img.Data, img.MimeType); err != nil {
			return err
		}
		if s.blobsSent == nil {
			s.blobsSent = map[string]bool{}
		}
		s.blobsSent[img.Ref] = true
		s.blobQueue[0] = nil
		s.blobQueue = s.blobQueue[1:]
	}
	if len(cached) > 0 {
		go s.confirmBlobs(s.tsp, cached)
	}
	return nil
}

// cacheBlob writes an image into Tern's blob cache, as a temporary file
// renamed into place so Tern never reads part of it, and reports whether
// the image is there. It is not when Tern offers no cache, the image must
// go inline, or the write fails.
func (s *session) cacheBlob(img *fe.ViewImage) bool {
	if s.blobDir == "" || !s.blobCache || s.blobInline[img.Ref] || len(img.Data) > tern.MaxBlob {
		return false
	}
	path := filepath.Join(s.blobDir, img.Ref)
	if _, err := os.Stat(path); err == nil {
		return true
	}
	tmp := path + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, img.Data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	return true
}

// confirmBlobs asks Tern which of the cached images it holds, off PiG's
// owner loop, and queues those it does not hold to go through the pty with
// the next flush. With no reply in time, all of them go.
func (s *session) confirmBlobs(tsp *tern.Session, imgs []*fe.ViewImage) {
	ids := make([]string, len(imgs))
	for i, img := range imgs {
		ids[i] = img.Ref
	}
	ctx, cancel := context.WithTimeout(context.Background(), blobReplyWait)
	have, err := tsp.HaveBlobs(ctx, ids)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.fellBack || s.tsp != tsp {
		return
	}
	requeued := false
	for _, img := range imgs {
		if err == nil && slices.Contains(have, img.Ref) {
			continue
		}
		if s.blobInline == nil {
			s.blobInline = map[string]bool{}
		}
		s.blobInline[img.Ref] = true
		delete(s.blobsSent, img.Ref)
		s.blobQueue = append(s.blobQueue, img)
		requeued = true
	}
	if requeued {
		_ = s.flushLocked()
	}
}
