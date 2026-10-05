module gatefixture

go 1.26

// Test fixture only (not part of the Package): a Go extension that holds the write tool until the
// end-to-end test lets it run, so the test decides whether the adapter or the write comes first.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1
