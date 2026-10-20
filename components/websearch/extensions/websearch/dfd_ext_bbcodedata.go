package websearch

import (
	"math"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/bbcode-data.js: Steam group announcements, whose body is BBCode in the
// data-partnereventstore attribute of #application_config.

type dfdBbcodeData struct {
	ctx    dfdExtCtx
	event  any
	loaded bool
}

func init() {
	dfdRegisterExtractor("bbcodedata", func(c dfdExtCtx) dfdExtractor { return &dfdBbcodeData{ctx: c} })
}

func (e *dfdBbcodeData) name() string { return "bbcodedata" }

func (e *dfdBbcodeData) getEventData() any {
	if !e.loaded {
		e.event = e.parseConfigAttr("data-partnereventstore")
		e.loaded = true
	}
	return e.event
}

// parseConfigAttr is the attribute of #application_config parsed as JSON (the first element of an array); nil for
// a missing, empty or invalid value.
func (e *dfdBbcodeData) parseConfigAttr(attr string) any {
	raw := extAttr(ldQuerySelector(e.ctx.doc, "#application_config"), attr)
	if raw == "" {
		return nil
	}
	parsed, err := jsonParse(raw)
	if err != nil {
		return nil
	}
	if arr, ok := parsed.([]any); ok {
		if len(arr) == 0 {
			return nil
		}
		return arr[0]
	}
	return parsed
}

// bbField is `obj?.key` for a value that may not be an object.
func bbField(v any, key string) any {
	if o, ok := v.(*jsObj); ok {
		x, _ := o.get(key)
		return x
	}
	return nil
}

func (e *dfdBbcodeData) canExtract() bool {
	return jsTruthy(bbField(bbField(e.getEventData(), "announcement_body"), "body"))
}

// bbStr is a title-like value of the event: a string or a number, as String() writes it.
func bbStr(v any) string {
	switch v.(type) {
	case string, float64, bool:
		return jsToString(v)
	}
	return ""
}

func (e *dfdBbcodeData) extract() dfdExtracted {
	event := e.getEventData()
	body := bbField(event, "announcement_body")
	text := ""
	if v := bbField(body, "body"); jsTruthy(v) {
		s, ok := v.(string)
		if !ok {
			ldThrowType("html.replace is not a function")
		}
		text = s
	}
	contentHTML := bbcodeToHTML(text)
	title := bbStr(bbField(body, "headline"))
	if !jsTruthy(bbField(body, "headline")) {
		title = bbStr(bbField(event, "event_name"))
	}
	published := ""
	if pt := bbField(body, "posttime"); jsTruthy(pt) {
		var n float64
		switch x := pt.(type) {
		case float64:
			n = x
		case string:
			n = jsToNumber(x)
		case bool:
			n = 1
		case []any:
			parts := make([]string, len(x))
			for i, it := range x {
				if it != nil {
					parts[i] = jsToString(it)
				}
			}
			n = jsToNumber(strings.Join(parts, ","))
		default:
			n = math.NaN()
		}
		published = jsISOString(jsDateFromNumber(n * 1000))
	}
	author := bbStr(bbField(e.parseConfigAttr("data-groupvanityinfo"), "group_name"))
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables:   map[string]string{"title": title, "author": author, "published": published},
	}
}
