// Package art draws cover art and a spinning disc as plain text lines, and fetches and caches the thumbnails.
//
// The extension returns text lines that the host paints; it cannot write to the terminal itself, so it cannot use the
// Kitty, iTerm2 or sixel image protocols. Instead a thumbnail is drawn with half blocks (two pixels per cell, the upper one
// as the foreground colour and the lower one as the background), in 24-bit colour, xterm-256 colour or, when colour is not
// available or NO_COLOR is set, with a block-shade ramp. Nothing here ever writes an image escape: the only escapes are
// ordinary colour (SGR) sequences, and none at all in monochrome.
//
// Thumbnails come from a plain HTTPS fetch of a public image, with no cookies and no credentials.
package art
