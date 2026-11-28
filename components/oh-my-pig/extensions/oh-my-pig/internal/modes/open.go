package modes

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// openURLFn is an injectable hook for tests. Defaults to launching the OS
// browser with an argv-only command (never shell-eval) for http/https URLs.
var openURLFn = defaultOpenURL

// defaultOpenURL opens an http:// or https:// URL in the user's default browser
// using argv-only exec (never shell-eval).
// upstream: packages/coding-agent/src/utils/open.ts
func defaultOpenURL(href string) error {
	parsed, err := url.Parse(href)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (only http and https are allowed)", parsed.Scheme)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", href)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", href)
	default:
		cmd = exec.Command("xdg-open", href)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// handleOpenCommand implements `/open [link]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:637-660
func (m *manager) handleOpenCommand(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg != "" && arg != "link" && arg != "url" {
		notifyInfo(ctx, "Usage: /open [link]")
		return nil
	}
	branch, err := ctx.GetBranch()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to read conversation: %v", err))
		return nil
	}
	href := ExtractLastLink(branch)
	if href == "" {
		notifyInfo(ctx, "No link to open.")
		return nil
	}
	if openURLFn != nil {
		if err := openURLFn(href); err != nil {
			notifyWarn(ctx, fmt.Sprintf("Failed to open %s: %v", href, err))
			return nil
		}
	}
	notifyInfo(ctx, fmt.Sprintf("Opening %s", href))
	return nil
}
