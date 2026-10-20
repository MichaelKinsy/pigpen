package warden

// Stand-ins for the context lane's half of output.ts (output_context.go, excerpt.go). The security lane compiles
// alone against these; whichever branch lands second deletes this file, the real definitions take over.

// OutputFormat names a recognised tool-output format (excerpt.go in the context lane).
type OutputFormat string

// CompressionLearner learns which retention a tool's outputs tolerate (output_context.go in the context lane).
type CompressionLearner struct{}

func retentionQuestions() Questions { return Questions{} }

func applyRetention(*OutputVerdict, map[string]Answer, ContextConfig) {}

func learnerSuggest(*CompressionLearner, string) Retention { return "" }
