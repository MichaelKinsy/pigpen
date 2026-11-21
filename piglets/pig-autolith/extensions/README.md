# Owned extensions

- `autolith-bridge/` is the Piglet's extension: Common Lisp on the PiG Lisp SDK (an ASDF system). PiG builds it into a cached
  SBCL image the first time it loads; it is not fused into the Binary.
- `fuse-anchor/` is an empty Go factory with no tools, commands or hooks. PiG refuses a Binary build with no fused member
  (building only the Lisp extension stops with "nothing to embed or fuse"), so this member is what lets the Piglet build a Binary.
