package ts

// libc character classes as the language's wasm links them (wasi-libc, a musl): see wctype_gen.go
// for the tables, recorded from the oracle itself.

func isdigit(c int32) bool  { return uint32(c)-'0' < 10 }
func iswdigit(c int32) bool { return uint32(c)-'0' < 10 }
func iswalnum(c int32) bool { return iswdigit(c) || iswalpha(c) }
