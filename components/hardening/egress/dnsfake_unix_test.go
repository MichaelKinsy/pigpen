//go:build unix

package egress

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeDNS answers A and AAAA queries from a table, in-process: the Go resolver is pointed at it through
// net.Resolver.Dial, so names resolve without a network or a DNS server.
type fakeDNS struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	queries atomic.Int64
	byName  sync.Map // name -> *atomic.Int64
}

func newFakeDNS(answers map[string][]string) *fakeDNS {
	f := &fakeDNS{answers: map[string][]netip.Addr{}}
	for name, as := range answers {
		f.set(name, as...)
	}
	return f
}

func (f *fakeDNS) set(name string, addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []netip.Addr
	for _, a := range addrs {
		out = append(out, netip.MustParseAddr(a))
	}
	f.answers[strings.ToLower(name)] = out
}

func (f *fakeDNS) lookups(name string) int64 {
	if v, ok := f.byName.Load(strings.ToLower(name)); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

func (f *fakeDNS) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		client, server := net.Pipe()
		go f.serve(server)
		return client, nil
	}}
}

func (f *fakeDNS) serve(c net.Conn) {
	defer c.Close()
	for {
		var n uint16
		if err := binary.Read(c, binary.BigEndian, &n); err != nil {
			return
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(c, msg); err != nil {
			return
		}
		resp := f.answer(msg)
		out := make([]byte, 2, 2+len(resp))
		binary.BigEndian.PutUint16(out, uint16(len(resp)))
		out = append(out, resp...)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func (f *fakeDNS) answer(msg []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	f.queries.Add(1)
	ctr, _ := f.byName.LoadOrStore(name, new(atomic.Int64))
	ctr.(*atomic.Int64).Add(1)
	f.mu.Lock()
	addrs, known := f.answers[name]
	f.mu.Unlock()
	rcode := dnsmessage.RCodeSuccess
	if !known {
		rcode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RecursionAvailable: true, RCode: rcode})
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	hdr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 0}
	for _, a := range addrs {
		switch {
		case a.Is4() && q.Type == dnsmessage.TypeA:
			_ = b.AResource(hdr, dnsmessage.AResource{A: a.As4()})
		case a.Is6() && q.Type == dnsmessage.TypeAAAA:
			_ = b.AAAAResource(hdr, dnsmessage.AAAAResource{AAAA: a.As16()})
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

func TestFakeDNSWorks(t *testing.T) {
	f := newFakeDNS(map[string][]string{"x.test": {"192.0.2.1", "2001:db8::1"}})
	addrs, err := f.resolver().LookupNetIP(context.Background(), "ip", "x.test")
	if err != nil || len(addrs) != 2 {
		t.Fatalf("%v %v", addrs, err)
	}
	if _, err := f.resolver().LookupNetIP(context.Background(), "ip", "nope.test"); err == nil {
		t.Fatal("unknown name resolved")
	}
}
