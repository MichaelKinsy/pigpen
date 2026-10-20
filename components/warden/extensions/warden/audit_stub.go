package warden

var SkipDirs = map[string]bool{}
var SourceRE = &stubRE{}

type stubRE struct{}

func (*stubRE) MatchString(string) bool { return false }

func DiscoverSourceFiles(dir string, max int) []string      { return nil }
func FindProjects(cwd string) []string                      { return nil }
func BuildAuditPrompt(cwd string, projects []string) string { return "" }

type ReportSnapshot struct {
	Exists  bool
	MtimeMs float64
}

func SnapshotReport(path string) ReportSnapshot         { return ReportSnapshot{} }
func ReportOutcome(before, after ReportSnapshot) string { return "" }
