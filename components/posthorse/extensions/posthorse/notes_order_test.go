package posthorse

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TEMPORARY-GATE: removed together with notes_order.go.
// official-host.ts:1457 'note publication ... serializes write+append': the executions of one batch reach the host's
// file queue in the order they started, whatever their host round trips before it took. The second ticket asks for
// its turn first (its earlier work was shorter); the host must still see the first ticket's request first.
func TestTEMPORARYGATENoteMutationsReachTheQueueInStartOrder(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	first, second := takeNoteTicket(), takeNoteTicket()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ran []string
	run := func(name string, ticket *noteTicket) {
		defer wg.Done()
		_, _ = queuedInOrder(ticket, nil, dir+"/"+name, func() (struct{}, error) {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			return struct{}{}, nil
		})
	}
	wg.Add(2)
	go run("second", second)
	// The second execution is waiting at the gate: the host has not been asked for its turn yet.
	time.Sleep(100 * time.Millisecond)
	if n := len(f.H().CallsTo("fileMutation.enter")); n != 0 {
		t.Fatalf("the second ticket asked the host for its turn before the first one passed (%d requests)", n)
	}
	go run("first", first)
	wg.Wait()
	if got := strings.Join(ran, ","); got != "first,second" {
		t.Fatalf("queue turns granted in order %s, want first,second", got)
	}
	enters := f.H().CallsTo("fileMutation.enter")
	if len(enters) != 2 || !strings.HasSuffix(enters[0].Args["path"].(string), "/first") {
		t.Fatalf("host saw %v, want the first ticket's request first", enters)
	}
}

// TEMPORARY-GATE: removed together with notes_order.go.
// A ticket that never reaches the queue (a read, a validation error) passes without holding later ones.
func TestTEMPORARYGATENoteTicketPassesWithoutQueueing(t *testing.T) {
	first, second := takeNoteTicket(), takeNoteTicket()
	first.pass()
	first.pass()
	select {
	case <-second.previous:
	case <-time.After(time.Second):
		t.Fatal("a passed ticket must release the next one")
	}
	second.pass() // the gate is process-wide: leave no ticket behind for the next test
	var nilTicket *noteTicket
	nilTicket.pass()
}
