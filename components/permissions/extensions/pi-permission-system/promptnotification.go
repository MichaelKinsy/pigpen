package pi_permission_system

import "strings"

// Port of src/presentation/prompt-notification.ts: what a prompt notification says, and the terminal bytes that carry it.

const (
	bel = "\x07"
	osc = "\x1b]"
)

// PromptNotice is what a prompt notification says: OSC 777 carries both fields.
type PromptNotice struct{ Title, Body string }

// NotificationSession is the session facts a notice names, read when the prompt opens. A nil Name is undefined.
type NotificationSession struct {
	Name *string
	Cwd  string
}

// NoticeRequestFacts is the request facts a notice may name: labels only, never the value.
type NoticeRequestFacts struct {
	ToolName  *string
	Surface   string
	Requester PromptRequester
}

// DescribePromptNotice says which session is waiting, and what is being asked and by whom. Labels only: the request's value (the
// command, path, MCP target or skill) never appears, because the OS notification history is a store this package does not
// control.
func DescribePromptNotice(dialogTitle string, request NoticeRequestFacts, session NotificationSession) PromptNotice {
	label := sessionLabel(session)
	subject := askSubject(request)
	notice := PromptNotice{Title: "pi", Body: dialogTitle}
	if label != "" {
		notice.Title = "pi \u2014 " + label
	}
	if subject != "" {
		notice.Body = dialogTitle + ": " + subject
	}
	return notice
}

// sessionLabel is the session's name, or the name of the directory it runs in, as Pi's own terminal title does. An empty name is
// unnamed.
func sessionLabel(session NotificationSession) string {
	if session.Name != nil && *session.Name != "" {
		return *session.Name
	}
	return HostPath.Basename(session.Cwd)
}

// askSubject is what is asked (the tool, else the gate surface) and by which agent.
func askSubject(request NoticeRequestFacts) string {
	what := request.Surface
	if request.ToolName != nil {
		what = *request.ToolName
	}
	who := ""
	if request.Requester.AgentName != nil {
		who = *request.Requester.AgentName
	}
	if what != "" && who != "" {
		return what + " (" + who + ")"
	}
	if what != "" {
		return what
	}
	return who
}

// RenderPromptNotification is the bytes that ask a terminal for attention as a permission dialog opens: one sequence per configured
// channel ("bell", "osc9" or "osc777"), in configured order; an empty list renders nothing. Every field is stripped of control
// characters, so it cannot end a sequence early or start another one.
func RenderPromptNotification(channels []string, notice PromptNotice) string {
	clean := PromptNotice{Title: withoutControlCharacters(notice.Title), Body: withoutControlCharacters(notice.Body)}
	var out strings.Builder
	for _, channel := range channels {
		out.WriteString(renderChannel(channel, clean))
	}
	return out.String()
}

func renderChannel(channel string, notice PromptNotice) string {
	switch channel {
	case "bell":
		return bel
	case "osc9":
		// One field, so the title leads it: it is what names the session.
		return osc + "9;" + notice.Title + ": " + notice.Body + bel
	case "osc777":
		return osc + "777;notify;" + osc777Field(notice.Title) + ";" + osc777Field(notice.Body) + bel
	}
	return ""
}

// osc777Field replaces ";" (OSC 777's field separator), so one inside a field would not split it.
func osc777Field(text string) string { return strings.ReplaceAll(text, ";", ":") }

// withoutControlCharacters drops the C0 controls and DEL. Every one is a single ASCII byte, so a byte walk equals the original's
// code-point walk and keeps any other byte (a lone surrogate included) untouched.
func withoutControlCharacters(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if c := value[i]; c > 31 && c != 127 {
			b.WriteByte(c)
		}
	}
	return b.String()
}
