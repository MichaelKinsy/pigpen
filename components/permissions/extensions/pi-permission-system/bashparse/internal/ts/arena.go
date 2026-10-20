package ts

// Slab allocators for what one parse creates and throws away. The C runtime pools subtrees and
// stack nodes with reference counts; a Go parse makes them by the thousand (a 5 KB script makes
// about 24000 allocations), and the collector's cost of those, not the parse, was most of the
// time. A Parser keeps its slabs between parses, so a warm parse allocates almost nothing: the
// caller must be done with every subtree (the Tree and everything read from it) before the next
// Parse call on the same Parser.

// slab hands out zeroed elements from chunks it keeps for reuse. Pointers stay valid until reset.
type slab[T any] struct {
	chunks [][]T
	cur    int // chunk being filled
	used   int // elements handed out of chunks[cur]
	total  int // capacity of all chunks
}

const (
	slabFirstChunk = 64
	slabMaxChunk   = 2048
)

func (s *slab[T]) alloc() *T {
	if s.cur < len(s.chunks) && s.used < len(s.chunks[s.cur]) {
		p := &s.chunks[s.cur][s.used]
		s.used++
		var zero T
		*p = zero
		return p
	}
	return s.allocSlow()
}

func (s *slab[T]) allocSlow() *T {
	if s.cur+1 < len(s.chunks) {
		s.cur++
		s.used = 1
		p := &s.chunks[s.cur][0]
		var zero T
		*p = zero
		return p
	}
	n := slabFirstChunk
	if len(s.chunks) > 0 {
		n = len(s.chunks[len(s.chunks)-1]) * 2
		if n > slabMaxChunk {
			n = slabMaxChunk
		}
	}
	s.chunks = append(s.chunks, make([]T, n))
	s.total += n
	s.cur = len(s.chunks) - 1
	s.used = 1
	return &s.chunks[s.cur][0]
}

// reset makes every chunk available again. Chunks keep their stale contents: alloc zeroes.
func (s *slab[T]) reset() {
	s.cur, s.used = 0, 0
}

// release drops the chunks when the slab grew past max elements, so one huge input does not pin
// its memory for the life of the parser.
func (s *slab[T]) release(max int) {
	if s.total > max {
		*s = slab[T]{}
	}
}

// subtreeArray hands out the child arrays of subtrees (and pop results): exact-length slices
// whose capacity is clipped, so an append by a caller copies instead of overwriting a neighbor.
type subtreeArray struct {
	chunks [][]*subtree
	cur    int
	used   int
	total  int
}

func (a *subtreeArray) alloc(n int) []*subtree {
	if n == 0 {
		return nil
	}
	for a.cur < len(a.chunks) {
		c := a.chunks[a.cur]
		if a.used+n <= len(c) {
			s := c[a.used : a.used+n : a.used+n]
			a.used += n
			return s
		}
		a.cur++
		a.used = 0
	}
	size := 512
	if len(a.chunks) > 0 {
		size = len(a.chunks[len(a.chunks)-1]) * 2
		if size > 16384 {
			size = 16384
		}
	}
	if size < n {
		size = n
	}
	a.chunks = append(a.chunks, make([]*subtree, size))
	a.total += size
	a.cur = len(a.chunks) - 1
	a.used = n
	return a.chunks[a.cur][:n:n]
}

func (a *subtreeArray) reset() { a.cur, a.used = 0, 0 }

func (a *subtreeArray) release(max int) {
	if a.total > max {
		*a = subtreeArray{}
	}
}

// arenas is everything one parse allocates and the parser reuses.
type arenas struct {
	subs       slab[subtree]
	stackNodes slab[stackNode]
	arrays     subtreeArray
}

// Bounds past which a parser lets go of a slab after a parse (elements, not bytes): about 4 MB of
// subtrees, 3 MB of stack nodes, 1 MB of child pointers.
const (
	maxRetainedSubtrees   = 32768
	maxRetainedStackNodes = 32768
	maxRetainedArrayPtrs  = 131072
)

func (a *arenas) reset() {
	a.subs.reset()
	a.stackNodes.reset()
	a.arrays.reset()
}

func (a *arenas) release() {
	a.subs.release(maxRetainedSubtrees)
	a.stackNodes.release(maxRetainedStackNodes)
	a.arrays.release(maxRetainedArrayPtrs)
}
