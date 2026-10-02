package jev

import (
	"fmt"
	"strings"
)

// The output judge asks about text that exists only after the call: a credential
// echoed into the transcript, and what kind of failure a result reports. The leak
// question separated 0.92-0.99 from 0.01-0.02 upstream with no overlap; the class
// answered at 0.88-1.00 when right and 0.42 when unsure, which is why a
// confidence floor silences the advice.
var outputQuestions = []question{
	{ID: "leaks_secret", Type: "noul",
		Instructions: "Does `output` contain a secret or credential that must not be written to a session transcript?",
		CriteriaTrue: "A key, token, password, or private key appears in the text", CriteriaFalse: "Only ordinary program output"},
	{ID: "failure_class", Type: "choice", Instructions: "What kind of failure is `output` reporting?",
		Options: []option{
			{"transient", desc("A network or resource hiccup that may succeed on a retry")},
			{"environment", desc("A missing dependency, port, or tool in the local setup")},
			{"code_bug", desc("The code or types are wrong")},
			{"permission", desc("Access was denied by the OS or a server")},
			{"user_error", desc("The command itself was invoked wrongly")},
			{"no_failure", desc("Output reports success or nothing wrong")},
		}},
}

// classAdvice is a table, not a branch: the class names are Jev's, and adding
// one is a row rather than a code path.
var classAdvice = map[string]string{
	"transient":   "retrying the same command unchanged is reasonable",
	"environment": "fix the environment (missing tool, port, or service) before retrying",
	"code_bug":    "fix the code or types; retrying unchanged will not help",
	"permission":  "access was denied; change what is being accessed or ask the user",
	"user_error":  "the invocation itself was wrong; fix the command",
}

const outputArgChars = 400

func outputStateJSON(cwd, tool string, input any, output string, isError bool, outputChars, maxState int) []byte {
	build := func(argChars, outChars int, dropArgs, _ bool) []byte {
		var b strings.Builder
		b.WriteString(`{"cwd":`)
		b.Write(jsonString(cwd))
		b.WriteString(`,"tool":`)
		b.Write(jsonString(tool))
		fmt.Fprintf(&b, `,"is_error":%t,"arguments":`, isError)
		if dropArgs {
			b.Write(jsonString("…[elided to fit maxStateChars]"))
		} else {
			b.Write(jsonValue(summarize(input, argChars, 0)))
		}
		b.WriteString(`,"output":`)
		b.Write(jsonString(elide(output, outChars)))
		b.WriteByte('}')
		return []byte(b.String())
	}
	return fitState(maxState, outputArgChars, outputChars, build)
}

type outputVerdict struct {
	LeaksSecret     float64
	FailureClass    string
	ClassConfidence float64
	HasClass        bool
	Notice          string
	Kind            string // leak, advice, none
	Resp            *response
}

func evaluateOutput(r *response, c config) outputVerdict {
	v := outputVerdict{Kind: "none", Resp: r}
	v.LeaksSecret = r.Answers["leaks_secret"].Noul
	cls := r.Answers["failure_class"]
	v.FailureClass, v.ClassConfidence, v.HasClass = cls.Choice, cls.Confidence, cls.HasConfidence
	switch {
	case v.LeaksSecret >= c.Output.LeakThreshold:
		v.Kind = "leak"
		v.Notice = fmt.Sprintf("Jev flagged this output as containing a secret (%s). Do not repeat the value in a reply, a file, or a command; refer to it by name instead.", toFixed2(v.LeaksSecret))
	case v.FailureClass != "" && classAdvice[v.FailureClass] != "" && (!v.HasClass || v.ClassConfidence >= c.Output.MinConfidence):
		v.Kind = "advice"
		conf := "n/a"
		if v.HasClass {
			conf = toFixed2(v.ClassConfidence)
		}
		v.Notice = fmt.Sprintf("Jev read this as a %s failure (confidence %s): %s.", v.FailureClass, conf, classAdvice[v.FailureClass])
	}
	return v
}
