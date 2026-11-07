# HistoryLane mutations: equivalent mutants removed

* `windowedEntry-empty-text`: flattenEntry returns ok only with a non-empty text ("[type] " prefix), so the `text == ""` guard is unreachable.
* `rarity-anchor`: the anchor byte only changes how fast foldPattern finds a match, never which texts match.
* `windowEntries-malformed`: a malformed line parses to a nil entry, which has no id and is never windowed; skipping it or not gives the same yields.
* `membership-memo`: the memo only avoids re-reading headers; membership answers are the same without it.
* `foldIndexString-threshold`: short texts are scanned directly and long ones through foldPattern; both give the same index (`TestFoldPatternDifferential` compares them), so the threshold only changes speed.
* `windowHandoff-nil-entry`: a nil rec reads as empty (`str` of a nil map is "", false), so the explicit nil check returns what the lookups below return anyway.
* `isAnchor-key`: `searchAccumulator.add` skips every hit until the exact qualified id of the cursor while seeking, so the collector's anchor flag only bounds which candidates are kept, never which hits are returned.
* `searchPage-tail-length`: the tail is ASCII (a skipped note and a base64url cursor), where `len` and `u16len` agree.
