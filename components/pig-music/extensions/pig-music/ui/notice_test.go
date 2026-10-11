package ui

import (
	"strings"
	"testing"
)

// The player moves past a track whose stream YouTube refused again right after it was resolved afresh, and says why
// (mpv.Config.Unplayable): the reason shows on the status line, as a failed command's does, until the next key.
func TestANoticeFromThePlayerShowsOnTheStatusLine(t *testing.T) {
	r := newRig(t, 100, 30)
	r.state(playing(tracks(3), 1))
	const reason = `skipped "Song": YouTube refused its stream again right after it was resolved afresh`
	r.send(NoticeMsg(reason))
	if !strings.Contains(r.text(), reason) {
		t.Errorf("the reason is not shown:\n%s", r.text())
	}
	r.key("j")
	if strings.Contains(r.text(), reason) {
		t.Error("the reason outlived the next key")
	}
}
