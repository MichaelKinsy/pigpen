package selfmanage

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func python(major, minor int) func(context.Context) (doctor.Version, bool) {
	return func(context.Context) (doctor.Version, bool) { return doctor.Version{major, minor, 0}, major > 0 }
}

func installer(t *testing.T, goos, goarch string, py func(context.Context) (doctor.Version, bool)) *Installer {
	t.Helper()
	return &Installer{Dir: t.TempDir(), GOOS: goos, GOARCH: goarch, Python: py, Now: time.Now}
}

func TestYtdlpPlanPicksTheZipWithPythonAndTheExecutableWithout(t *testing.T) {
	for _, tc := range []struct {
		goos, arch string
		py         func(context.Context) (doctor.Version, bool)
		asset      string
		licence    string
	}{
		{"linux", "amd64", python(3, 12), "yt-dlp", "Unlicense"},
		{"darwin", "arm64", python(3, 10), "yt-dlp", "Unlicense"},
		{"linux", "amd64", python(3, 9), "yt-dlp_linux", "GPL"},
		{"linux", "arm64", python(0, 0), "yt-dlp_linux_aarch64", "GPL"},
		{"darwin", "arm64", python(0, 0), "yt-dlp_macos", "GPL"},
		{"windows", "amd64", python(3, 12), "yt-dlp.exe", "GPL"}, // a zip needs a launcher on Windows
		{"windows", "arm64", python(0, 0), "yt-dlp_arm64.exe", "GPL"},
		{"android", "arm64", python(3, 12), "yt-dlp", "Unlicense"},
	} {
		p, err := installer(t, tc.goos, tc.arch, tc.py).YtdlpPlan(context.Background())
		if err != nil {
			t.Errorf("%+v: %v", tc, err)
			continue
		}
		if p.Asset != tc.asset || !strings.Contains(p.Licence, tc.licence) {
			t.Errorf("%s/%s: asset %q licence %q", tc.goos, tc.arch, p.Asset, p.Licence)
		}
		wantDest := "yt-dlp"
		if tc.goos == "windows" {
			wantDest = "yt-dlp.exe"
		}
		if filepath.Base(p.Dest) != wantDest || filepath.Base(filepath.Dir(p.Dest)) != "bin" {
			t.Errorf("dest %q", p.Dest)
		}
		if !strings.HasPrefix(p.URL, "https://github.com/yt-dlp/yt-dlp/releases/latest/download/") || !strings.HasSuffix(p.SumsURL, "/SHA2-256SUMS") {
			t.Errorf("urls %q %q", p.URL, p.SumsURL)
		}
	}
	for _, tc := range []struct{ goos, arch, want string }{
		{"android", "arm64", "pkg install python-yt-dlp"},
		{"linux", "386", "Python 3.10"},
		{"freebsd", "amd64", "Python 3.10"},
	} {
		_, err := installer(t, tc.goos, tc.arch, python(0, 0)).YtdlpPlan(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s/%s: %v", tc.goos, tc.arch, err)
		}
	}
}

func TestDenoPlanTriples(t *testing.T) {
	for _, tc := range []struct{ goos, arch, asset, dest string }{
		{"linux", "amd64", "deno-x86_64-unknown-linux-gnu.zip", "deno"},
		{"linux", "arm64", "deno-aarch64-unknown-linux-gnu.zip", "deno"},
		{"darwin", "amd64", "deno-x86_64-apple-darwin.zip", "deno"},
		{"darwin", "arm64", "deno-aarch64-apple-darwin.zip", "deno"},
		{"windows", "amd64", "deno-x86_64-pc-windows-msvc.zip", "deno.exe"},
	} {
		p, err := installer(t, tc.goos, tc.arch, nil).DenoPlan()
		if err != nil {
			t.Errorf("%+v: %v", tc, err)
			continue
		}
		if p.Asset != tc.asset || filepath.Base(p.Dest) != tc.dest || !strings.Contains(p.Licence, "MIT") ||
			p.URL != "https://github.com/denoland/deno/releases/latest/download/"+tc.asset || p.SumsURL != p.URL+".sha256sum" {
			t.Errorf("%+v", p)
		}
	}
	if _, err := installer(t, "android", "arm64", nil).DenoPlan(); err == nil || !strings.Contains(err.Error(), "pkg install nodejs") {
		t.Errorf("android: %v", err)
	}
	if _, err := installer(t, "linux", "386", nil).DenoPlan(); err == nil {
		t.Error("linux/386 has no Deno release")
	}
}

func TestDescribeShowsWhatWillHappen(t *testing.T) {
	p, _ := installer(t, "linux", "amd64", python(3, 12)).YtdlpPlan(context.Background())
	d := p.Describe()
	for _, want := range []string{"yt-dlp", p.URL, p.Dest, "Unlicense", "SHA2-256SUMS", "no sudo"} {
		if !strings.Contains(d, want) {
			t.Errorf("%q lacks %q", d, want)
		}
	}
}

// release serves files by path.
func release(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func plan(srv *httptest.Server, in *Installer, asset string, zipped bool, member string) Plan {
	p := Plan{Name: "tool", Asset: asset, URL: srv.URL + "/" + asset, Dest: filepath.Join(in.Dir, "bin", member), Zip: zipped, ZipMember: member}
	if zipped {
		p.SumsURL, p.SumsFormat = p.URL+".sha256sum", SumsSingle
	} else {
		p.SumsURL, p.SumsFormat = srv.URL+"/SHA2-256SUMS", SumsList
	}
	return p
}

func TestInstallVerifiesTheChecksumAndWritesAnExecutable(t *testing.T) {
	body := []byte("#!/usr/bin/env python3\nprint('yt-dlp')\n")
	other := []byte("other")
	srv := release(t, map[string][]byte{
		"/yt-dlp":       body,
		"/SHA2-256SUMS": []byte(sum(other) + "  yt-dlp_linux\n" + sum(body) + "  yt-dlp\n" + sum(other) + " *yt-dlp.exe\n"),
	})
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	p := plan(srv, in, "yt-dlp", false, "yt-dlp")
	if err := in.Install(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p.Dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("%v %q", err, got)
	}
	if st, _ := os.Stat(p.Dest); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(p.Dest))
	if len(entries) != 1 {
		t.Errorf("leftovers: %v", entries)
	}
}

func TestInstallRefusesABadChecksumAndLeavesTheOldFileAlone(t *testing.T) {
	srv := release(t, map[string][]byte{"/yt-dlp": []byte("tampered"), "/SHA2-256SUMS": []byte(sum([]byte("genuine")) + "  yt-dlp\n")})
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	p := plan(srv, in, "yt-dlp", false, "yt-dlp")
	_ = os.MkdirAll(filepath.Dir(p.Dest), 0o755)
	if err := os.WriteFile(p.Dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := in.Install(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(p.Dest); string(b) != "old" {
		t.Errorf("the old file was replaced: %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(p.Dest)); len(entries) != 1 {
		t.Errorf("leftovers: %v", entries)
	}
}

func TestInstallFailsWhenTheSumsFileHasNoEntryOrTheDownloadIsMissing(t *testing.T) {
	srv := release(t, map[string][]byte{"/yt-dlp": []byte("x"), "/SHA2-256SUMS": []byte(sum([]byte("x")) + "  something-else\n" + sum([]byte("x")) + "  missing\n")})
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	if err := in.Install(context.Background(), plan(srv, in, "yt-dlp", false, "yt-dlp")); err == nil || !strings.Contains(err.Error(), "yt-dlp") {
		t.Errorf("no entry: %v", err)
	}
	if err := in.Install(context.Background(), plan(srv, in, "missing", false, "missing")); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
	if _, err := os.Stat(filepath.Join(in.Dir, "bin", "yt-dlp")); err == nil {
		t.Error("a file was written")
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallUnpacksTheDenoMemberFromItsZip(t *testing.T) {
	z := makeZip(t, map[string]string{"README": "no", "deno": "DENO-BINARY"})
	srv := release(t, map[string][]byte{"/deno.zip": z, "/deno.zip.sha256sum": []byte(sum(z) + "  deno.zip\n")})
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	p := plan(srv, in, "deno.zip", true, "deno")
	if err := in.Install(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Dest); string(b) != "DENO-BINARY" {
		t.Errorf("%q", b)
	}
	if st, _ := os.Stat(p.Dest); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	// a zip without the member, or a bare hash file
	z2 := makeZip(t, map[string]string{"other": "x"})
	srv2 := release(t, map[string][]byte{"/deno.zip": z2, "/deno.zip.sha256sum": []byte(sum(z2))})
	in.Client = srv2.Client()
	if err := in.Install(context.Background(), plan(srv2, in, "deno.zip", true, "deno")); err == nil || !strings.Contains(err.Error(), "deno") {
		t.Errorf("zip without the member: %v", err)
	}
}

func TestUpdateRunsAtMostDailyAndAfterA403ButNotInALoop(t *testing.T) {
	in := installer(t, "linux", "amd64", nil)
	self := filepath.Join(in.Dir, "bin", "yt-dlp")
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	in.Now = func() time.Time { return clock }
	var runs [][]string
	var fail error
	in.Run = func(_ context.Context, bin string, args []string) ([]byte, []byte, error) {
		runs = append(runs, append([]string{bin}, args...))
		return []byte("Updated yt-dlp to stable@2026.09.01\n"), nil, fail
	}
	ran, out, err := in.Update(context.Background(), self, false)
	if err != nil || !ran || len(runs) != 1 || runs[0][len(runs[0])-1] != "-U" || !strings.Contains(out, "Updated") {
		t.Fatalf("first: ran=%v out=%q err=%v runs=%v", ran, out, err, runs)
	}
	clock = clock.Add(23 * time.Hour)
	if ran, _, _ := in.Update(context.Background(), self, false); ran {
		t.Error("ran twice in a day")
	}
	if ran, _, _ := in.Update(context.Background(), self, true); !ran {
		t.Error("a 403 an hour after the last update did not update")
	}
	if ran, _, _ := in.Update(context.Background(), self, true); ran {
		t.Error("two forced updates in a row")
	}
	clock = clock.Add(26 * time.Hour)
	fail = errors.New("exit status 1")
	ran, _, err = in.Update(context.Background(), self, false)
	if !ran || err == nil {
		t.Errorf("a failed update: ran=%v err=%v", ran, err)
	}
	if ran, _, _ := in.Update(context.Background(), self, false); ran {
		t.Error("a failed update was retried at once")
	}
	if _, _, err := in.Update(context.Background(), "/usr/bin/yt-dlp", true); !errors.Is(err, ErrNotSelfManaged) {
		t.Errorf("a system yt-dlp: %v", err)
	}
	_ = fmt.Sprint()
}

func TestSetupAsksBeforeEachDownloadAndDoesNothingOnNo(t *testing.T) {
	ytBody := []byte("#!/usr/bin/env python3\n")
	z := makeZip(t, map[string]string{"deno": "DENO"})
	srv := release(t, map[string][]byte{
		"/yt-dlp": ytBody, "/SHA2-256SUMS": []byte(sum(ytBody) + "  yt-dlp\n"),
		"/deno-x86_64-unknown-linux-gnu.zip": z, "/deno-x86_64-unknown-linux-gnu.zip.sha256sum": []byte(sum(z)),
	})
	in := installer(t, "linux", "amd64", python(3, 12))
	in.Client, in.YtdlpBase, in.DenoBase = srv.Client(), srv.URL+"/", srv.URL+"/"
	rep := doctor.Report{Checks: []doctor.Check{
		{ID: "yt-dlp", Status: doctor.Fail}, {ID: "js-runtime", Status: doctor.Warn},
	}}
	var asked []string
	no := func(title, msg string) (bool, error) { asked = append(asked, title); return false, nil }
	did, err := in.Setup(context.Background(), rep, no)
	if err != nil || len(asked) != 2 || len(did) != 2 || !strings.Contains(did[0], "did not download") {
		t.Fatalf("did=%v asked=%v err=%v", did, asked, err)
	}
	if entries, _ := os.ReadDir(in.Dir); len(entries) != 0 {
		t.Fatalf("a refusal left %v", entries)
	}
	yes := func(title, msg string) (bool, error) {
		if !strings.Contains(msg, srv.URL) {
			t.Errorf("the dialog does not say where from: %q", msg)
		}
		return true, nil
	}
	did, err = in.Setup(context.Background(), rep, yes)
	if err != nil || len(did) != 2 {
		t.Fatalf("did=%v err=%v", did, err)
	}
	for _, f := range []string{"yt-dlp", "deno"} {
		if _, err := os.Stat(filepath.Join(in.Dir, "bin", f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	// a healthy machine is asked nothing
	asked = nil
	if did, _ := in.Setup(context.Background(), doctor.Report{Checks: []doctor.Check{{ID: "yt-dlp", Status: doctor.OK}, {ID: "js-runtime", Status: doctor.OK}}}, no); len(did) != 0 || len(asked) != 0 {
		t.Errorf("did=%v asked=%v", did, asked)
	}
}

func TestSetupUpdatesASelfManagedYtdlpInsteadOfDownloadingAgain(t *testing.T) {
	in := installer(t, "linux", "amd64", python(3, 12))
	self := filepath.Join(in.Dir, "bin", "yt-dlp")
	var ran int
	in.Run = func(_ context.Context, bin string, args []string) ([]byte, []byte, error) {
		ran++
		return []byte("Updated yt-dlp\n"), nil, nil
	}
	rep := doctor.Report{YtdlpPath: self, SelfManaged: true, Checks: []doctor.Check{{ID: "yt-dlp", Status: doctor.Warn}}}
	ask := func(string, string) (bool, error) { t.Error("asked"); return false, nil }
	did, err := in.Setup(context.Background(), rep, ask)
	if err != nil || ran != 1 || len(did) != 1 || !strings.Contains(did[0], "Updated") {
		t.Fatalf("did=%v ran=%d err=%v", did, ran, err)
	}
}
