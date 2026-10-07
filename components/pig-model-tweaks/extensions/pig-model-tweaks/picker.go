package pigmodeltweaks

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// pickerRows is how many model rows are visible at once.
const pickerRows = 20

// modelPicker is the interactive chooser both picker commands open: a filter box,
// a cursor, and checkboxes. It is a [sdk.RemoteComponent], so PiG renders the
// lines in its focused overlay and hands raw keystrokes back to HandleInput.
//
// It replaces the pi original's `ctx.ui.custom` components, with the same keys:
// type to filter, arrows to move, Space to toggle, `a`/`n` for all and none while
// the filter is empty, Enter to confirm, Esc to cancel.
type modelPicker struct {
	// models is every candidate, in display order.
	models []modelRef
	// selected is the result set, seeded from the current allow-list.
	selected map[string]bool
	// multiple is false for a single-choice picker, where Enter returns the
	// highlighted row and Space is inert.
	multiple bool

	filter string
	cursor int
	top    int
	// unread is the last input chunk this picker could not decode, rendered as
	// an escape so the frame shows it. It is the only way to tell, from the
	// screen, which encoding a terminal is using for a key the picker does not
	// know yet.
	unread  string
	done    bool
	cancel  bool
	message string
}

// newModelPicker seeds a picker. checked names the rows that start selected.
func newModelPicker(models []modelRef, checked []string, multiple bool) *modelPicker {
	selected := make(map[string]bool, len(checked))
	for _, name := range checked {
		selected[name] = true
	}
	return &modelPicker{models: models, selected: selected, multiple: multiple}
}

// visible is the filtered row order the cursor moves through.
func (p *modelPicker) visible() []modelRef {
	if p.filter == "" {
		return p.models
	}
	needle := strings.ToLower(p.filter)
	out := make([]modelRef, 0, len(p.models))
	for _, model := range p.models {
		if strings.Contains(strings.ToLower(model.String()), needle) {
			out = append(out, model)
		}
	}
	return out
}

// Render draws one frame. Every line is cut to width: PiG renders a component's
// lines verbatim and a line wider than the terminal is a rendering error, so the
// truncation here is load-bearing, not cosmetic.
func (p *modelPicker) Render(width int) []string {
	rows := p.visible()
	if p.cursor >= len(rows) {
		p.cursor = max(0, len(rows)-1)
	}
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+pickerRows {
		p.top = p.cursor - pickerRows + 1
	}
	if p.top < 0 {
		p.top = 0
	}

	title := "Select a model"
	if p.multiple {
		title = "Select preferred models"
	}
	if p.message != "" {
		title += "  " + p.message
	}
	lines := []string{
		title,
		"  filter: " + p.filter + "_",
		"",
	}
	if len(rows) == 0 {
		lines = append(lines, "  (no model matches the filter)")
	}
	end := min(p.top+pickerRows, len(rows))
	for index := p.top; index < end; index++ {
		model := rows[index]
		cursor := "  "
		if index == p.cursor {
			cursor = "> "
		}
		box := "  "
		if p.multiple {
			box = "[ ] "
			if p.selected[model.String()] {
				box = "[x] "
			}
		}
		lines = append(lines, cursor+box+model.String())
	}

	switch {
	case len(rows) == 0:
	case p.top > 0:
		lines = append(lines, "  ... "+itoa(p.top)+" more above")
	case end < len(rows):
		lines = append(lines, "  ... "+itoa(len(rows)-end)+" more below")
	}
	lines = append(lines, "")
	hint := "  arrows move"
	if p.multiple {
		hint += " | space toggles"
		if p.filter == "" {
			hint += " | a all, n none"
		}
	}
	hint += " | enter confirms | esc cancels"
	lines = append(lines, hint)
	if p.unread != "" {
		lines = append(lines, "  unrecognised input: "+escapeForDisplay(p.unread))
	}

	fitted := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted = append(fitted, fit(line, width))
	}
	return fitted
}

// HandleInput applies one keystroke. The chunk is decoded first, so the same key
// works whether the terminal delivers legacy CSI, application cursor mode or the
// Kitty protocol.
func (p *modelPicker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	p.unread = ""
	// A paste types its whole text into the filter instead of being read as one
	// keystroke.
	if text, ok := pastedText(data); ok {
		p.filter += strings.ReplaceAll(text, "\n", " ")
		p.cursor, p.top = 0, 0
		return sdk.RemoteComponentResult{}, nil
	}
	decoded, typed := keyOf(data)
	if decoded == keyNone {
		p.unread = data
		return sdk.RemoteComponentResult{}, nil
	}

	switch decoded {
	case keyEnter:
		return p.confirm(), nil
	case keyEscape:
		if p.filter != "" {
			// Escape clears the filter first, and cancels only when it is empty.
			p.filter = ""
			return sdk.RemoteComponentResult{}, nil
		}
		p.cancel, p.done = true, true
		return sdk.RemoteComponentResult{Done: true, Value: nil}, nil
	case keyBackspace:
		if p.filter != "" {
			_, size := utf8.DecodeLastRuneInString(p.filter)
			p.filter = p.filter[:len(p.filter)-size]
		}
		return sdk.RemoteComponentResult{}, nil
	case keyUp:
		p.move(-1)
		return sdk.RemoteComponentResult{}, nil
	case keyDown:
		p.move(1)
		return sdk.RemoteComponentResult{}, nil
	case keySpace:
		p.toggle()
		return sdk.RemoteComponentResult{}, nil
	case keyRune:
		// `a` and `n` are shortcuts only while the filter is empty, so they stay
		// typeable in a search.
		if p.filter == "" && p.multiple {
			switch typed {
			case 'a':
				p.selectAll(true)
				return sdk.RemoteComponentResult{}, nil
			case 'n':
				p.selectAll(false)
				return sdk.RemoteComponentResult{}, nil
			}
		}
		p.filter += string(typed)
		p.cursor, p.top = 0, 0
	}
	return sdk.RemoteComponentResult{}, nil
}

// selectAll sets every filtered row, or clears every selection.
func (p *modelPicker) selectAll(on bool) {
	for _, model := range p.visible() {
		if on {
			p.selected[model.String()] = true
			continue
		}
		delete(p.selected, model.String())
	}
}

// move moves the cursor, clamped to the filtered rows.
func (p *modelPicker) move(delta int) {
	p.cursor += delta
	if rows := len(p.visible()); p.cursor < 0 {
		p.cursor = 0
	} else if p.cursor >= rows {
		p.cursor = rows - 1
	}
}

// toggle flips the highlighted row in a multiple-choice picker.
func (p *modelPicker) toggle() {
	if !p.multiple {
		return
	}
	rows := p.visible()
	if p.cursor >= len(rows) {
		return
	}
	name := rows[p.cursor].String()
	if p.selected[name] {
		delete(p.selected, name)
		return
	}
	p.selected[name] = true
}

// confirm returns the selection: every checked row in a multiple-choice picker, or
// the highlighted row in a single-choice one. A cancelled or empty picker returns
// nothing.
func (p *modelPicker) confirm() sdk.RemoteComponentResult {
	rows := p.visible()
	if !p.multiple {
		if p.cursor >= len(rows) {
			return sdk.RemoteComponentResult{Done: true, Value: []string(nil)}
		}
		return sdk.RemoteComponentResult{Done: true, Value: []string{rows[p.cursor].String()}}
	}
	chosen := make([]string, 0, len(p.selected))
	for name := range p.selected {
		chosen = append(chosen, name)
	}
	sort.Strings(chosen)
	if len(chosen) == 0 {
		return sdk.RemoteComponentResult{Done: true, Value: []string(nil)}
	}
	return sdk.RemoteComponentResult{Done: true, Value: chosen}
}

// fit cuts a line to width display columns. Model ids and providers are ASCII, so
// a rune count is the column count; a wide rune is charged two columns so the
// line still fits the narrow case.
func fit(line string, width int) string {
	if width <= 0 {
		return line
	}
	columns := 0
	for index, rune := range line {
		advance := 1
		if isWide(rune) {
			advance = 2
		}
		if columns+advance > width {
			return line[:index]
		}
		columns += advance
	}
	return line
}

// isWide reports whether a rune occupies two terminal columns. The ranges are the
// East Asian Wide and Fullwidth blocks plus emoji, which is what a model list can
// realistically contain.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF && r != 0x303F, // CJK
		r >= 0xAC00 && r <= 0xD7A3,                // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,                // CJK compatibility
		r >= 0xFE30 && r <= 0xFE6F,                // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60,                // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF:
		return true
	}
	return false
}

// escapeForDisplay renders a raw input chunk readably: printable characters stay,
// and everything else becomes \xNN.
func escapeForDisplay(data string) string {
	var out strings.Builder
	for _, r := range data {
		switch {
		case r == '\x1b':
			out.WriteString("\\x1b")
		case r >= ' ' && r != 0x7f:
			out.WriteRune(r)
		default:
			out.WriteString(fmt.Sprintf("\\x%02x", r))
		}
	}
	return out.String()
}

// selection reads a component result whatever shape the transport decoded it
// into. The SDK returns the component's Value through a JSON call result, so a
// []string comes back as []any; asserting []string alone silently turns every
// confirmation into a cancellation.
func selection(result any) []string {
	switch typed := result.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

// itoa formats a count without pulling strconv into the render path.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// pickerCatalog is the list a picker offers: every model the host can serve when
// it can be asked, and otherwise the models already in play for this extension, so
// a picker is never refused just because the host cannot enumerate a catalog.
func pickerCatalog(ctx sdk.Context, known []modelRef) []modelRef {
	if models := catalogModels(ctx, catalogUsable); len(models) > 0 {
		return models
	}
	seen := map[string]bool{}
	models := make([]modelRef, 0, len(known)+1)
	add := func(model modelRef) {
		name := model.String()
		if model.Provider == "" || model.Model == "" || seen[name] {
			return
		}
		seen[name] = true
		models = append(models, model)
	}
	if current := currentModel(ctx); current.Provider != "" {
		add(current)
	}
	for _, model := range known {
		add(model)
	}
	sortModels(models)
	return models
}

// catalogScope is which half of the registry a caller wants.
type catalogScope int

const (
	// catalogUsable is the models the host can serve right now, which is what its
	// credentials allow.
	catalogUsable catalogScope = iota
	// catalogEvery is every model the host knows, credentials or not. A provider
	// lock is a configuration choice, so it may be worth making for a model the
	// host cannot reach yet.
	catalogEvery
)

// catalogModels lists models from the host registry, newest API first.
//
// PiG's SDK grew the registry list only after the revision this repository's CI
// pins, so the call is resolved by name: a host that exposes it answers, and one
// that does not yields nothing here, which is the signal for the caller to fall
// back. Both shapes are read the same way, so neither generation needs its own
// code path.
func catalogModels(ctx sdk.Context, scope catalogScope) []modelRef {
	order := []string{"GetAvailable", "GetAll"}
	if scope == catalogEvery {
		order = []string{"GetAll", "GetAvailable"}
	}
	for _, name := range order {
		models, ok := callModelRegistry(ctx, name)
		if ok && len(models) > 0 {
			return models
		}
	}
	return nil
}

// callModelRegistry calls ModelRegistry.<name>() through the context and decodes
// the result as a list of models.
func callModelRegistry(ctx sdk.Context, name string) ([]modelRef, bool) {
	registry := reflect.ValueOf(ctx).MethodByName("ModelRegistry")
	if !registry.IsValid() || registry.Type().NumOut() == 0 {
		return nil, false
	}
	instance := registry.Call(nil)[0]
	method := instance.MethodByName(name)
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() == 0 {
		return nil, false
	}
	results := method.Call(nil)
	if len(results) > 1 {
		if failure, isError := results[1].Interface().(error); isError && failure != nil {
			return nil, false
		}
	}
	list, isList := results[0].Interface().([]map[string]any)
	if !isList {
		return nil, false
	}
	models := make([]modelRef, 0, len(list))
	for _, model := range list {
		provider, _ := model["provider"].(string)
		id, _ := model["id"].(string)
		// A provider may arrive as an object with an id, as the newer SDK does.
		if nested, isObject := model["provider"].(map[string]any); isObject {
			if value, _ := nested["id"].(string); value != "" {
				provider = value
			}
		}
		if provider == "" || id == "" {
			continue
		}
		models = append(models, modelRef{Provider: provider, Model: id})
	}
	sortModels(models)
	return models, true
}

// sortModels orders rows the way the pi original presented them: selected models
// first, then keyed models, then alphabetically.
func sortModels(models []modelRef) {
	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].String()) < strings.ToLower(models[j].String())
	})
}
