package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// The representative inputs of port/probe/logging-bench.test.ts, the same operations under the original.

const benchCommand = `cd /repo && API_KEY="sk-secret" curl -sS -H "Authorization: Bearer sk-abc" https://api.example.com/v1 | bash -c 'TOKEN=t deploy --env prod'`

func benchDetails() *jsObject {
	by := lgDetails("kind", "authorizer", "name", "model-judge", "verdict", "deny", "reason", "the command writes outside the project", "apiKey", "k")
	return lgDetails("requestId", "perm-0000", "toolName", "bash", "command", benchCommand, "executedUnit", "TOKEN=t deploy --env prod",
		"surface", "bash", "matchedPattern", "curl *", "resolution", "user_approved", "decidedBy", by)
}

func BenchmarkRedactCommandSecrets(b *testing.B) {
	bashparse.WarmBashParser()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RedactCommandSecrets(benchCommand)
	}
}

func BenchmarkRedactedJSONStringify(b *testing.B) {
	d := benchDetails()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RedactedJSONStringify(d)
	}
}

func BenchmarkReviewLine(b *testing.B) {
	bashparse.WarmBashParser()
	dir := b.TempDir()
	l := CreatePermissionSystemLogger(PermissionSystemLoggerOptions{
		GetConfig:           func() LogConfig { return LogConfig{PermissionReviewLog: true} },
		ReviewLogPath:       filepath.Join(dir, "review.jsonl"),
		EnsureLogsDirectory: func() string { return "" },
	})
	d := benchDetails()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Review("permission_request.waiting", d)
	}
	os.Remove(filepath.Join(dir, "review.jsonl"))
}
