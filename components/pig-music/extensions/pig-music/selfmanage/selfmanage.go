// Package selfmanage downloads the programs pig-music needs into its own data directory, only when the user agrees, and
// keeps the yt-dlp it downloaded current. It never uses sudo, never writes outside Dir, never downloads silently (the
// caller shows Plan.Describe and asks first), and checks every download against the checksum the release publishes.
//
// The checksum comes from the same release page as the file, so it guards against a truncated or corrupted download and
// a mirror that serves something else, not against a compromised release. Nothing is bundled in Pigpen: the programs
// are fetched from their official release pages onto the user's machine.
package selfmanage

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// SumsFormat says how a plan's checksum file is laid out.
type SumsFormat int

const (
	// SumsList is a SHA2-256SUMS file: one "hash  name" line per release file.
	SumsList SumsFormat = iota
	// SumsSingle is a file holding the one hash of the asset ("hash  name", or the bare hash).
	SumsSingle
)

const (
	ytdlpBase = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"
	denoBase  = "https://github.com/denoland/deno/releases/latest/download/"

	maxDownload = 300 << 20

	dailyEvery  = 24 * time.Hour
	forcedEvery = time.Hour
)

// Plan is one download, shown to the user before anything is fetched.
type Plan struct {
	Name       string // "yt-dlp" or "Deno"
	Asset      string
	URL        string
	SumsURL    string
	SumsFormat SumsFormat
	Licence    string
	Dest       string
	Zip        bool   // the asset is a zip; unpack ZipMember into Dest
	ZipMember  string // the file in the zip
}

// Describe is the text for the consent dialog.
func (p Plan) Describe() string {
	return fmt.Sprintf("Download %s from its official release page:\n  %s\nInstall it to:\n  %s\nLicence: %s\nThe download is checked against the SHA-256 checksum the release publishes (%s), which catches a corrupted or swapped download, not a compromised release. It is stored in pig-music's own data directory; no sudo, nothing else on this machine is changed.",
		p.Name, p.URL, p.Dest, p.Licence, path.Base(p.SumsURL))
}

// Installer downloads into Dir/bin.
type Installer struct {
	Dir          string // Paths.Data
	Client       *http.Client
	GOOS, GOARCH string
	// Python reports the version of python3 when it can be run.
	Python func(ctx context.Context) (doctor.Version, bool)
	Now    func() time.Time
	// Run runs `yt-dlp -U`; ytdlp.ExecRunner when nil.
	Run ytdlp.Runner
	// YtdlpBase and DenoBase replace the official release URLs (tests); each ends in a slash.
	YtdlpBase, DenoBase string
	// Stall is how long a download may go without receiving a byte before it is given up; default 60 s.
	Stall time.Duration
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func (in *Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return http.DefaultClient
}

func (in *Installer) binDir() string { return filepath.Join(in.Dir, "bin") }

func exe(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// YtdlpPlan chooses the official release file: the zipimport `yt-dlp` script when Python 3.10 or later is there (it is
// small, updates itself with -U and is public domain), else the platform executable (a PyInstaller build, GPL-3.0-or-later).
func (in *Installer) YtdlpPlan(ctx context.Context) (Plan, error) {
	p := Plan{Name: "yt-dlp", SumsURL: orDefault(in.YtdlpBase, ytdlpBase) + "SHA2-256SUMS", SumsFormat: SumsList, Dest: filepath.Join(in.binDir(), exe("yt-dlp", in.GOOS))}
	const unlicence = "yt-dlp is released into the public domain (The Unlicense); this is the Python script, which needs Python 3.10 or later, found on this machine."
	const gpl = "the yt-dlp executable bundles Python and libraries and is GPL-3.0-or-later (https://github.com/yt-dlp/yt-dlp/blob/master/LICENSE)."
	havePython := false
	if in.GOOS != "windows" && in.Python != nil {
		if v, ok := in.Python(ctx); ok && v.AtLeast(doctor.Version{3, 10, 0}) {
			havePython = true
		}
	}
	switch {
	case havePython:
		p.Asset, p.Licence = "yt-dlp", unlicence
	case in.GOOS == "windows" && in.GOARCH == "amd64":
		p.Asset, p.Licence = "yt-dlp.exe", gpl
	case in.GOOS == "windows" && in.GOARCH == "arm64":
		p.Asset, p.Licence = "yt-dlp_arm64.exe", gpl
	case in.GOOS == "linux" && in.GOARCH == "amd64":
		p.Asset, p.Licence = "yt-dlp_linux", gpl
	case in.GOOS == "linux" && in.GOARCH == "arm64":
		p.Asset, p.Licence = "yt-dlp_linux_aarch64", gpl
	case in.GOOS == "darwin":
		p.Asset, p.Licence = "yt-dlp_macos", gpl
	case in.GOOS == "android":
		return Plan{}, errors.New("on Termux install yt-dlp with `pkg install python-yt-dlp`: there is no official executable for Android")
	default:
		return Plan{}, fmt.Errorf("there is no official yt-dlp executable for %s/%s: install Python 3.10 or later and run /music setup again, or install yt-dlp with your package manager", in.GOOS, in.GOARCH)
	}
	p.URL = orDefault(in.YtdlpBase, ytdlpBase) + p.Asset
	return p, nil
}

// DenoPlan is the official Deno release for this machine (MIT).
func (in *Installer) DenoPlan() (Plan, error) {
	var triple string
	switch {
	case in.GOOS == "linux" && in.GOARCH == "amd64":
		triple = "x86_64-unknown-linux-gnu"
	case in.GOOS == "linux" && in.GOARCH == "arm64":
		triple = "aarch64-unknown-linux-gnu"
	case in.GOOS == "darwin" && in.GOARCH == "amd64":
		triple = "x86_64-apple-darwin"
	case in.GOOS == "darwin" && in.GOARCH == "arm64":
		triple = "aarch64-apple-darwin"
	case in.GOOS == "windows" && in.GOARCH == "amd64":
		triple = "x86_64-pc-windows-msvc"
	case in.GOOS == "android":
		return Plan{}, errors.New("Deno has no Android release: on Termux run `pkg install nodejs` and set \"jsRuntime\": \"node\" in the pig-music settings")
	default:
		return Plan{}, fmt.Errorf("Deno has no official release for %s/%s: install Node 22 or later and set \"jsRuntime\": \"node\" in the pig-music settings", in.GOOS, in.GOARCH)
	}
	asset := "deno-" + triple + ".zip"
	return Plan{
		Name: "Deno", Asset: asset, URL: orDefault(in.DenoBase, denoBase) + asset, SumsURL: orDefault(in.DenoBase, denoBase) + asset + ".sha256sum", SumsFormat: SumsSingle,
		Licence: "Deno is MIT licensed (https://github.com/denoland/deno/blob/main/LICENSE.md).",
		Dest:    filepath.Join(in.binDir(), exe("deno", in.GOOS)), Zip: true, ZipMember: exe("deno", in.GOOS),
	}, nil
}

// errStalled is the cause of a download given up because the server stopped sending.
var errStalled = errors.New("the server stopped sending")

func (in *Installer) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	stall := in.Stall
	if stall <= 0 {
		stall = 60 * time.Second
	}
	// Nothing can cancel /music setup, so a download that receives nothing for the stall time is given up.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(stall, func() { cancel(errStalled) })
	defer watchdog.Stop()
	fail := func(err error) error {
		if cause := context.Cause(ctx); errors.Is(cause, errStalled) {
			return fmt.Errorf("%s: %w for %s; nothing was installed", url, errStalled, stall)
		}
		return fmt.Errorf("%s: %w", url, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.client().Do(req)
	if err != nil {
		return nil, fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(progress{resp.Body, func() { watchdog.Reset(stall) }}, limit+1))
	if err != nil {
		return nil, fail(err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d MiB", url, limit>>20)
	}
	return b, nil
}

// progress calls tick whenever bytes arrive.
type progress struct {
	r    io.Reader
	tick func()
}

func (p progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.tick()
	}
	return n, err
}

var hexHash = lazyre.New(`(?i)\b[0-9a-f]{64}\b`)

func expectedHash(sums []byte, p Plan) (string, error) {
	switch p.SumsFormat {
	case SumsSingle:
		if h := hexHash.Find(sums); h != nil {
			return strings.ToLower(string(h)), nil
		}
		return "", fmt.Errorf("%s holds no SHA-256 hash", p.SumsURL)
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == p.Asset && hexHash.MatchString(f[0]) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s has no checksum for %s", p.SumsURL, p.Asset)
}

// Install downloads the plan, checks its checksum, and replaces Dest atomically. On any failure Dest is untouched and
// nothing is left behind.
func (in *Installer) Install(ctx context.Context, p Plan) error {
	sums, err := in.get(ctx, p.SumsURL, 1<<20)
	if err != nil {
		return fmt.Errorf("checksum file: %w", err)
	}
	want, err := expectedHash(sums, p)
	if err != nil {
		return err
	}
	data, err := in.get(ctx, p.URL, maxDownload)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: the release says %s, the download is %x; nothing was installed", p.Asset, want, got)
	}
	if p.Zip {
		if data, err = unzipMember(data, p.ZipMember); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.Dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p.Dest)
}

func unzipMember(zipped []byte, member string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, fmt.Errorf("the download is not a zip file: %w", err)
	}
	for _, f := range r.File {
		if f.Name != member {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxDownload+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxDownload {
			return nil, fmt.Errorf("%s in the zip is larger than %d MiB", member, maxDownload>>20)
		}
		return b, nil
	}
	return nil, fmt.Errorf("the zip file has no %s", member)
}

// ErrNotSelfManaged is returned by Update for a yt-dlp pig-music did not download: it is the user's or the package
// manager's to update.
var ErrNotSelfManaged = errors.New("this yt-dlp is not the copy pig-music manages")

func (in *Installer) stampPath() string { return filepath.Join(in.Dir, "last-update") }

func (in *Installer) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// Update runs `yt-dlp -U` on the self-managed copy at most once a day. force is for right after a 403 (an update may
// have shipped the fix) and is held to once an hour, so a 403 that an update cannot fix does not become a loop. It
// reports whether it ran; a failed run is recorded like a successful one, so it is not retried until the interval has passed.
func (in *Installer) Update(ctx context.Context, ytdlpPath string, force bool) (ran bool, out string, err error) {
	if rel, relErr := filepath.Rel(in.binDir(), ytdlpPath); relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false, "", ErrNotSelfManaged
	}
	every := dailyEvery
	if force {
		every = forcedEvery
	}
	if b, readErr := os.ReadFile(in.stampPath()); readErr == nil {
		if secs, parseErr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); parseErr == nil && in.now().Sub(time.Unix(secs, 0)) < every {
			return false, "", nil
		}
	}
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return false, "", err
	}
	if err := os.WriteFile(in.stampPath(), []byte(strconv.FormatInt(in.now().Unix(), 10)), 0o644); err != nil {
		return false, "", err
	}
	run := in.Run
	if run == nil {
		run = ytdlp.ExecRunner
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// --ignore-config: a user's config can name --cookies-from-browser, which -U's network setup would load.
	stdout, stderr, runErr := run(ctx, ytdlpPath, []string{"--ignore-config", "-U"})
	out = strings.TrimSpace(string(stdout) + string(stderr))
	if runErr != nil {
		return true, out, fmt.Errorf("yt-dlp -U: %w: %s", runErr, out)
	}
	return true, out, nil
}

// Ask puts a question to the user; ok is true for yes.
type Ask func(title, message string) (ok bool, err error)

// Setup fixes what a download can fix, asking before each: no yt-dlp, or one that is old and not pig-music's (pig-music
// then keeps its own copy and leaves yours alone), and no JavaScript runtime (Deno). A self-managed yt-dlp that is old
// is updated with -U. It returns what it did, one line each.
func (in *Installer) Setup(ctx context.Context, rep doctor.Report, ask Ask) (did []string, err error) {
	yt, _ := rep.Check("yt-dlp")
	switch {
	case rep.SelfManaged && yt.Status != doctor.OK:
		ran, out, upErr := in.Update(ctx, rep.YtdlpPath, true)
		switch {
		case upErr != nil:
			return did, upErr
		case ran:
			did = append(did, "ran yt-dlp -U: "+firstLine(out))
		default:
			did = append(did, "yt-dlp was updated within the last hour; not updated again")
		}
	case yt.Status == doctor.Fail || yt.Status == doctor.Warn:
		plan, planErr := in.YtdlpPlan(ctx)
		if planErr != nil {
			did = append(did, planErr.Error())
			break
		}
		line, err := in.offer(ctx, plan, ask)
		if err != nil {
			return did, err
		}
		did = append(did, line)
	}
	if js, ok := rep.Check("js-runtime"); ok && js.Status == doctor.Warn {
		plan, planErr := in.DenoPlan()
		if planErr != nil {
			return append(did, planErr.Error()), nil
		}
		line, err := in.offer(ctx, plan, ask)
		if err != nil {
			return did, err
		}
		did = append(did, line)
	}
	return did, nil
}

func (in *Installer) offer(ctx context.Context, p Plan, ask Ask) (string, error) {
	ok, err := ask("Download "+p.Name+"?", p.Describe())
	if err != nil {
		return "", err
	}
	if !ok {
		return "did not download " + p.Name, nil
	}
	if err := in.Install(ctx, p); err != nil {
		return "", fmt.Errorf("installing %s: %w", p.Name, err)
	}
	return "installed " + p.Name + " to " + p.Dest, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// NewInstaller is an Installer for the machine env describes, downloading into dir (Paths.Data).
func NewInstaller(dir string, env doctor.Env) *Installer {
	return &Installer{
		Dir: dir, GOOS: env.GOOS, GOARCH: env.GOARCH, Now: env.Now, Run: env.Run,
		Python: func(ctx context.Context) (doctor.Version, bool) {
			for _, name := range []string{"python3", "python"} {
				path, err := env.LookPath(name)
				if err != nil {
					continue
				}
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				stdout, stderr, _ := env.Run(ctx, path, []string{"--version"})
				cancel()
				if v, ok := doctor.ParseVersion(string(append(stdout, stderr...))); ok && strings.Contains(strings.ToLower(string(append(stdout, stderr...))), "python") {
					return v, true
				}
			}
			return doctor.Version{}, false
		},
	}
}
