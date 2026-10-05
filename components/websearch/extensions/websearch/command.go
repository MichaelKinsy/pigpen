package websearch

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandUI is the part of the host UI the /search command needs (ctx.ui.notify / ctx.ui.select).
type CommandUI interface {
	Notify(message, level string)
	// Select shows options and returns the choice; ok is false when the dialog was dismissed.
	Select(title string, options []string) (choice string, ok bool)
}

var searchChoiceRE = regexp.MustCompile(`^\[([a-z0-9]+)\]`)

func minutesAgo(ts int64) int64 { return (nowMs() - ts) / 60000 }

// SearchCommand is the /search command (index.ts:3618): browse the stored results, view or delete one.
func (r *Runtime) SearchCommand(ui CommandUI) {
	results := GetAllResults()
	if len(results) == 0 {
		ui.Notify("No stored search results", "info")
		return
	}
	short := func(id string) string { return jsSlice(id, 0, 6) }
	options := make([]string, len(results))
	for i, res := range results {
		age := minutesAgo(res.Timestamp)
		ageStr := fmt.Sprintf("%dm ago", age)
		if age >= 60 {
			ageStr = fmt.Sprintf("%dh ago", age/60)
		}
		switch {
		case res.Type == "search" && res.Queries != nil:
			query := "unknown"
			if len(res.Queries) > 0 && res.Queries[0].Query != "" {
				query = res.Queries[0].Query
			}
			options[i] = fmt.Sprintf("[%s] \"%s\" (%d queries) - %s", short(res.ID), query, len(res.Queries), ageStr)
		case res.Type == "fetch" && (res.URLs != nil || res.URLMetadata != nil):
			n := len(res.URLMetadata)
			if res.URLs != nil {
				n = len(res.URLs)
			}
			options[i] = fmt.Sprintf("[%s] %d URLs fetched - %s", short(res.ID), n, ageStr)
		default:
			options[i] = fmt.Sprintf("[%s] %s - %s", short(res.ID), res.Type, ageStr)
		}
	}
	choice, ok := ui.Select("Stored Search Results", options)
	if !ok || choice == "" {
		return
	}
	match := searchChoiceRE.FindStringSubmatch(choice)
	if match == nil {
		return
	}
	var selected *StoredSearchData
	for _, res := range results {
		if strings.HasPrefix(res.ID, match[1]) {
			selected = res
			break
		}
	}
	if selected == nil {
		return
	}
	action, ok := ui.Select("Result "+short(selected.ID), []string{"View details", "Delete"})
	switch {
	case ok && action == "Delete":
		DeleteResult(selected.ID)
		ui.Notify("Deleted "+short(selected.ID), "info")
	case ok && action == "View details":
		ui.Notify(searchDetails(selected), "info")
	}
}

func searchDetails(s *StoredSearchData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ID: %s\nType: %s\nAge: %dm\n\n", s.ID, s.Type, minutesAgo(s.Timestamp))
	if s.Type == "search" && s.Queries != nil {
		b.WriteString("Queries:\n")
		for i, q := range s.Queries {
			if i == 10 {
				break
			}
			fmt.Fprintf(&b, "- \"%s\" (%d results)\n", q.Query, len(q.Results))
		}
		if len(s.Queries) > 10 {
			fmt.Fprintf(&b, "... and %d more\n", len(s.Queries)-10)
		}
	}
	if s.Type == "fetch" && (s.URLs != nil || s.URLMetadata != nil) {
		b.WriteString("URLs:\n")
		type item struct {
			url  string
			err  *string
			size int
		}
		var items []item
		if s.URLs != nil {
			for _, u := range s.URLs {
				items = append(items, item{u.URL, u.Error, jsLen(u.Content)})
			}
		} else {
			for _, u := range s.URLMetadata {
				items = append(items, item{u.URL, u.Error, u.ContentLength})
			}
		}
		for i, u := range items {
			if i == 10 {
				break
			}
			display := u.url
			if jsLen(display) > 50 {
				display = jsSlice(display, 0, 47) + "..."
			}
			status := fmt.Sprintf("%d chars", u.size)
			if u.err != nil && *u.err != "" {
				status = *u.err
			}
			fmt.Fprintf(&b, "- %s (%s)\n", display, status)
		}
		if len(items) > 10 {
			fmt.Fprintf(&b, "... and %d more\n", len(items)-10)
		}
	}
	return b.String()
}
