package typesafe

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// LogLevel is the log verbosity; LogOff disables logging.
type LogLevel string

// Log levels from most to least verbose.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
	LogOff   LogLevel = "off"
)

// LogLevels lists the supported levels, most verbose first.
var LogLevels = []LogLevel{LogDebug, LogInfo, LogWarn, LogError, LogOff}

// DefaultLogLevel applies when neither the configuration nor the environment set one.
const DefaultLogLevel = LogWarn

// ParseLogLevel validates a level; the error names value and source (for example
// `Invalid log level "loud" from TYPESAFE_LOG_LEVEL. Expected one of: debug, info, warn, error, off.`).
func ParseLogLevel(value, source string) (LogLevel, error) {
	for _, l := range LogLevels {
		if string(l) == value {
			return l, nil
		}
	}
	names := make([]string, len(LogLevels))
	for i, l := range LogLevels {
		names[i] = string(l)
	}
	return "", errorf("Invalid log level %q from %s. Expected one of: %s.", value, source, strings.Join(names, ", "))
}

// Logger receives a message and structured values. The SDK never logs at warn or
// error by itself; info logs request summaries and debug adds headers and bodies.
type Logger interface {
	Debug(message string, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message string, args ...any)
}

const logPrefix = "[typesafe-sdk] "

type stderrLogger struct{}

// NewStderrLogger returns the default logger: lines prefixed "[typesafe-sdk] " on stderr.
func NewStderrLogger() Logger { return stderrLogger{} }

func (stderrLogger) write(message string, args []any) {
	var b strings.Builder
	b.WriteString(logPrefix)
	b.WriteString(message)
	for _, a := range args {
		b.WriteByte(' ')
		switch v := a.(type) {
		case error:
			b.WriteString(v.Error())
		case string:
			b.WriteString(v)
		default:
			if raw, err := json.Marshal(a); err == nil {
				b.Write(raw)
			} else {
				fmt.Fprintf(&b, "%v", a)
			}
		}
	}
	b.WriteByte('\n')
	_, _ = os.Stderr.WriteString(b.String())
}

func (l stderrLogger) Debug(m string, a ...any) { l.write(m, a) }
func (l stderrLogger) Info(m string, a ...any)  { l.write(m, a) }
func (l stderrLogger) Warn(m string, a ...any)  { l.write(m, a) }
func (l stderrLogger) Error(m string, a ...any) { l.write(m, a) }

var levelRank = map[LogLevel]int{LogDebug: 0, LogInfo: 1, LogWarn: 2, LogError: 3, LogOff: 4}

type levelLogger struct {
	sink Logger
	rank int
}

// WithLevel filters a logger to level and above.
func WithLevel(sink Logger, level LogLevel) Logger {
	return levelLogger{sink: sink, rank: levelRank[level]}
}

func (l levelLogger) Debug(m string, a ...any) {
	if l.rank <= levelRank[LogDebug] {
		l.sink.Debug(m, a...)
	}
}
func (l levelLogger) Info(m string, a ...any) {
	if l.rank <= levelRank[LogInfo] {
		l.sink.Info(m, a...)
	}
}
func (l levelLogger) Warn(m string, a ...any) {
	if l.rank <= levelRank[LogWarn] {
		l.sink.Warn(m, a...)
	}
}
func (l levelLogger) Error(m string, a ...any) {
	if l.rank <= levelRank[LogError] {
		l.sink.Error(m, a...)
	}
}

var whitespaceRun = regexp.MustCompile(`\s+`)

// redactKey masks a key, preserving its scheme and the last four characters of secrets longer than eight.
func redactKey(value string) string {
	scheme, secret := "", value
	if strings.Contains(value, " ") {
		parts := whitespaceRun.Split(value, -1) // JavaScript split(/\s+/, 2) keeps the first two parts
		scheme = parts[0]
		secret = ""
		if len(parts) > 1 {
			secret = parts[1]
		}
	}
	tail := ""
	if r := []rune(secret); len(r) > 8 {
		tail = string(r[len(r)-4:])
	}
	if scheme != "" {
		scheme += " "
	}
	return scheme + "***" + tail
}

// RedactHeaders returns a copy with credentials masked: authorization,
// proxy-authorization and x-api-key keep their scheme and the last four characters
// of secrets longer than eight ("Bearer ***cdef"); cookie and set-cookie become "***".
func RedactHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "x-api-key":
			out[name] = redactKey(value)
		case "cookie", "set-cookie":
			out[name] = "***"
		default:
			out[name] = value
		}
	}
	return out
}
