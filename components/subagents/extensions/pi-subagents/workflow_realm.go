package pi_subagents

import (
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// wfRealm is the goja runtime of one workflow run with the built-in operations the host needs, taken before any script
// ran so that a script that replaces Object.keys or Array.isArray cannot change what the host sees. The original does the
// same by running the host half in the worker's own realm, apart from the script's vm context (scripted-workflow.ts:1008-1014).
type wfRealm struct {
	vm *goja.Runtime

	objectProto *goja.Object
	arrayProto  *goja.Object

	isArray      goja.Callable
	getProto     goja.Callable
	ownKeys      goja.Callable // Object.keys
	getDesc      goja.Callable // Object.getOwnPropertyDescriptor
	getSymbols   goja.Callable // Object.getOwnPropertySymbols
	hasOwn       goja.Callable // Object.prototype.hasOwnProperty
	propIsEnum   goja.Callable // Object.prototype.propertyIsEnumerable
	freeze       goja.Callable
	isFrozen     goja.Callable
	reflectHas   goja.Callable
	reflectGet   goja.Callable
	jsonStringfy goja.Callable
	numberIsInt  goja.Callable
	errorProto   *goja.Object
	promiseCtor  *goja.Object
	promiseThen  goja.Callable
	promiseProto *goja.Object
	symIterator  *goja.Symbol
	wrapped      string // the compiled script text, for error messages that quote the source
	objToString  goja.Callable
	symToString  goja.Callable
}

func newWfRealm(vm *goja.Runtime) *wfRealm {
	r := &wfRealm{vm: vm}
	fn := func(src string) goja.Callable {
		v, err := vm.RunString(src)
		if err != nil {
			panic(err)
		}
		c, ok := goja.AssertFunction(v)
		if !ok {
			panic("workflow realm: " + src + " is not a function")
		}
		return c
	}
	obj := func(src string) *goja.Object {
		v, err := vm.RunString(src)
		if err != nil {
			panic(err)
		}
		return v.ToObject(vm)
	}
	r.objectProto = obj("Object.prototype")
	r.arrayProto = obj("Array.prototype")
	r.errorProto = obj("Error.prototype")
	r.promiseCtor = obj("Promise")
	r.promiseProto = obj("Promise.prototype")
	r.isArray = fn("Array.isArray")
	r.getProto = fn("Object.getPrototypeOf")
	r.ownKeys = fn("Object.keys")
	r.getDesc = fn("Object.getOwnPropertyDescriptor")
	r.getSymbols = fn("Object.getOwnPropertySymbols")
	r.hasOwn = fn("Object.prototype.hasOwnProperty")
	r.propIsEnum = fn("Object.prototype.propertyIsEnumerable")
	r.freeze = fn("Object.freeze")
	r.isFrozen = fn("Object.isFrozen")
	r.reflectHas = fn("Reflect.has")
	r.reflectGet = fn("Reflect.get")
	r.jsonStringfy = fn("JSON.stringify")
	r.numberIsInt = fn("Number.isInteger")
	r.promiseThen = fn("Promise.prototype.then")
	r.objToString = fn("Object.prototype.toString")
	r.symToString = fn("Symbol.prototype.toString")
	if s, ok := obj("Symbol.iterator").Export().(*goja.Symbol); ok {
		r.symIterator = s
	}
	return r
}

// call runs a built-in and rethrows a JavaScript exception as one (the caller is a host function the script called).
func (r *wfRealm) call(fn goja.Callable, this goja.Value, args ...goja.Value) goja.Value {
	v, err := fn(this, args...)
	if err != nil {
		if ex, ok := err.(*goja.Exception); ok {
			panic(ex.Value())
		}
		panic(r.vm.NewGoError(err))
	}
	return v
}

func (r *wfRealm) array(v goja.Value) bool {
	return r.call(r.isArray, goja.Undefined(), v).ToBoolean()
}

func (r *wfRealm) hasOwnProp(o *goja.Object, key string) bool {
	return r.call(r.hasOwn, o, r.vm.ToValue(key)).ToBoolean()
}

func (r *wfRealm) hasOwnSym(o *goja.Object, key goja.Value) bool {
	return r.call(r.hasOwn, o, key).ToBoolean()
}

// protoOf is Object.getPrototypeOf (nil for null).
func (r *wfRealm) protoOf(o *goja.Object) *goja.Object {
	v := r.call(r.getProto, goja.Undefined(), o)
	if goja.IsNull(v) || isUndef(v) {
		return nil
	}
	return v.ToObject(r.vm)
}

func (r *wfRealm) symbolKeys(o *goja.Object) []goja.Value {
	arr := r.call(r.getSymbols, goja.Undefined(), o).ToObject(r.vm)
	n := int(arr.Get("length").ToInteger())
	out := make([]goja.Value, n)
	for i := range n {
		out[i] = arr.Get(strconv.Itoa(i))
	}
	return out
}

// isUndef reports a JavaScript undefined (goja returns a nil Value for a missing property).
func isUndef(v goja.Value) bool { return v == nil || goja.IsUndefined(v) }

// jsTypeof is the typeof operator.
func jsTypeof(v goja.Value) string {
	if isUndef(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "object"
	}
	if _, ok := v.(*goja.Symbol); ok {
		return "symbol"
	}
	if _, ok := v.(*goja.Object); ok {
		if _, callable := goja.AssertFunction(v); callable {
			return "function"
		}
		return "object"
	}
	t := v.ExportType()
	if t == nil {
		return "undefined"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	}
	if t == reflect.TypeOf((*big.Int)(nil)) {
		return "bigint"
	}
	return "object"
}

func isJSString(v goja.Value) (string, bool) {
	if v == nil || isUndef(v) || goja.IsNull(v) {
		return "", false
	}
	if _, isObj := v.(*goja.Object); isObj {
		return "", false
	}
	if jsTypeof(v) == "string" {
		return v.String(), true
	}
	return "", false
}

func isJSNumber(v goja.Value) (float64, bool) {
	if v == nil || isUndef(v) || goja.IsNull(v) {
		return 0, false
	}
	if _, isObj := v.(*goja.Object); isObj {
		return 0, false
	}
	if jsTypeof(v) == "number" {
		return v.ToFloat(), true
	}
	return 0, false
}

func isJSBool(v goja.Value) (bool, bool) {
	if v == nil || isUndef(v) || goja.IsNull(v) {
		return false, false
	}
	if _, isObj := v.(*goja.Object); isObj {
		return false, false
	}
	if jsTypeof(v) == "boolean" {
		return v.ToBoolean(), true
	}
	return false, false
}

func isJSInteger(v goja.Value) bool {
	f, ok := isJSNumber(v)
	return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
}

// ---- util.inspect(value, { depth: 4, breakLength: 120 }) (node:util, used by the sandbox's console) ----

// workflowInspect formats a console argument as Node's util.inspect does with { depth: 4, breakLength: 120 }
// (scripted-workflow.ts:850); it follows lib/internal/util/inspect.js for the values a workflow script prints.
func (r *wfRealm) inspect(v goja.Value) string {
	c := &inspectCtx{r: r, depth: 4, breakLength: 120, compact: 3, seen: map[*goja.Object]bool{}, circular: map[*goja.Object]int{}}
	return c.format(v, 0, false)
}

type inspectCtx struct {
	r            *wfRealm
	depth        int
	breakLength  int
	compact      int
	indentLvl    int
	currentDepth int
	seen         map[*goja.Object]bool
	seenList     []*goja.Object
	circular     map[*goja.Object]int
}

const (
	kObjectType = iota
	kArrayType
	kArrayExtrasType
)

func (c *inspectCtx) format(v goja.Value, recurse int, typed bool) string {
	if v == nil {
		return "undefined"
	}
	obj, isObj := v.(*goja.Object)
	if !isObj {
		return c.primitive(v)
	}
	if c.seen[obj] {
		idx, ok := c.circular[obj]
		if !ok {
			idx = len(c.circular) + 1
			c.circular[obj] = idx
		}
		return "[Circular *" + strconv.Itoa(idx) + "]"
	}
	return c.raw(obj, recurse)
}

func (c *inspectCtx) primitive(v goja.Value) string {
	switch {
	case isUndef(v):
		return "undefined"
	case goja.IsNull(v):
		return "null"
	}
	if sym, ok := v.(*goja.Symbol); ok {
		return c.r.call(c.r.symToString, sym).String()
	}
	switch jsTypeof(v) {
	case "string":
		return c.str(v.String())
	case "number":
		f := v.ToFloat()
		if f == 0 && math.Signbit(f) {
			return "-0"
		}
		return jsNumber(f)
	case "bigint":
		return v.String() + "n"
	case "boolean":
		return strconv.FormatBool(v.ToBoolean())
	}
	return v.String()
}

var inspectKeyRe = func(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// quote picks the quote like strEscape: single, else double when the text holds a single quote only, else backtick.
func inspectQuote(s string) (string, string) {
	q := "'"
	if strings.Contains(s, "'") {
		if !strings.Contains(s, `"`) {
			q = `"`
		} else if !strings.Contains(s, "`") && !strings.Contains(s, "${") {
			q = "`"
		}
	}
	var b strings.Builder
	b.WriteString(q)
	for _, r := range s {
		switch {
		case string(r) == q:
			b.WriteString("\\" + q)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\v':
			b.WriteString(`\v`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + strings.ToUpper(strconv.FormatInt(int64(r)+0x100, 16)[1:]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(q)
	return b.String(), q
}

func (c *inspectCtx) str(s string) string {
	// strings of a container: long ones with newlines split into joined lines (formatPrimitive, compact !== true)
	if len(s) > 16 && jsLen(s) > c.breakLength-c.indentLvl-4 && strings.Contains(s, "\n") {
		parts := splitKeepNewline(s)
		if len(parts) > 1 {
			out := make([]string, len(parts))
			for i, p := range parts {
				out[i], _ = inspectQuote(p)
			}
			return strings.Join(out, " +\n"+strings.Repeat(" ", c.indentLvl+2))
		}
	}
	q, _ := inspectQuote(s)
	return q
}

func splitKeepNewline(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 || i == len(s)-1 {
			if s != "" {
				out = append(out, s)
			}
			return out
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
}

func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// constructorName walks the prototype chain for the first own "constructor" function with a name (getConstructorName).
func (c *inspectCtx) constructorName(obj *goja.Object) (string, bool) {
	cur := obj
	for i := 0; cur != nil && i < 1000; i++ {
		if c.r.hasOwnProp(cur, "constructor") {
			desc := c.r.call(c.r.getDesc, goja.Undefined(), cur, c.r.vm.ToValue("constructor"))
			if d, ok := desc.(*goja.Object); ok {
				ctor := d.Get("value")
				if f, ok := ctor.(*goja.Object); ok {
					if _, callable := goja.AssertFunction(f); callable {
						if name, ok := isJSString(f.Get("name")); ok && name != "" {
							return name, true
						}
					}
				}
			}
		}
		cur = c.r.protoOf(cur)
	}
	return "", false
}

// builtinTag is the [[Class]]-like tag Object.prototype.toString reports, which is how this engine tells a Set from an Object.
func (r *wfRealm) builtinTag(obj *goja.Object) string {
	t := r.call(r.objToString, obj).String()
	t = strings.TrimPrefix(t, "[object ")
	return strings.TrimSuffix(t, "]")
}

var inspectTypedArrays = map[string]bool{"Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true, "Int16Array": true, "Uint16Array": true, "Int32Array": true, "Uint32Array": true, "Float32Array": true, "Float64Array": true, "BigInt64Array": true, "BigUint64Array": true}

func (c *inspectCtx) raw(obj *goja.Object, recurse int) string {
	r := c.r
	class := obj.ClassName()
	if class == "Object" {
		if t := r.builtinTag(obj); t == "Set" || t == "Map" || t == "Promise" || t == "WeakMap" || t == "WeakSet" || inspectTypedArrays[t] {
			class = t
		}
	}
	isArr := r.array(obj)
	ctorName, hasCtor := c.constructorName(obj)
	tag := ""
	if t, ok := isJSString(obj.GetSymbol(symToStringTag(r.vm))); ok && t != "" && t != ctorName {
		tag = t
	}
	nullProto := r.protoOf(obj) == nil

	prefixFor := func(fallback, size string) string {
		name := ctorName
		if !hasCtor {
			name = "[" + fallback + ": null prototype]"
			if size != "" {
				name = "[" + fallback + size + ": null prototype]"
			}
		} else if size != "" {
			name += size
		}
		if tag != "" {
			name += " [" + tag + "]"
		}
		return name + " "
	}

	var keys []goja.Value // own enumerable keys to show
	for _, k := range r.keysOf(obj) {
		keys = append(keys, r.vm.ToValue(k))
	}
	var symKeys []goja.Value
	for _, s := range r.symbolKeys(obj) {
		if r.call(r.propIsEnum, obj, s).ToBoolean() {
			symKeys = append(symKeys, s)
		}
	}

	braces := [2]string{"{", "}"}
	base := ""
	extras := kObjectType
	var formatter func() []string
	noEntries := false

	switch {
	case isArr:
		extras = kArrayExtrasType
		n := int(obj.Get("length").ToInteger())
		prefix := ""
		if !hasCtor || ctorName != "Array" || tag != "" {
			prefix = prefixFor("Array", "("+strconv.Itoa(n)+")")
		}
		braces = [2]string{prefix + "[", "]"}
		// index keys are formatted by the array formatter
		var rest []goja.Value
		for _, k := range keys {
			if isArrayIndex(k.String()) {
				continue
			}
			rest = append(rest, k)
		}
		keys = rest
		if n == 0 && len(keys) == 0 && len(symKeys) == 0 {
			return braces[0] + "]"
		}
		formatter = func() []string { return c.arrayEntries(obj, n, recurse) }
	case inspectTypedArrays[class]:
		extras = kArrayExtrasType
		n := int(obj.Get("length").ToInteger())
		prefix := prefixFor(class, "("+strconv.Itoa(n)+")")
		braces = [2]string{prefix + "[", "]"}
		keys = filterIndexKeys(keys)
		if n == 0 && len(keys) == 0 && len(symKeys) == 0 {
			return braces[0] + "]"
		}
		formatter = func() []string { return c.arrayEntries(obj, n, recurse) }
	case class == "Set" && hasCtorOrTag(class, ctorName):
		size := int(obj.Get("size").ToInteger())
		prefix := prefixFor("Set", "("+strconv.Itoa(size)+")")
		if size == 0 && len(keys) == 0 && len(symKeys) == 0 {
			return prefix + "{}"
		}
		braces = [2]string{prefix + "{", "}"}
		formatter = func() []string { return c.setEntries(obj, recurse) }
	case class == "Map" && hasCtorOrTag(class, ctorName):
		size := int(obj.Get("size").ToInteger())
		prefix := prefixFor("Map", "("+strconv.Itoa(size)+")")
		if size == 0 && len(keys) == 0 && len(symKeys) == 0 {
			return prefix + "{}"
		}
		braces = [2]string{prefix + "{", "}"}
		formatter = func() []string { return c.mapEntries(obj, recurse) }
	default:
		noEntries = true
		if _, callable := goja.AssertFunction(obj); callable {
			base = c.functionBase(obj, ctorName, hasCtor, tag)
			if len(keys) == 0 && len(symKeys) == 0 {
				return base
			}
		} else if class == "RegExp" {
			base = obj.String()
			if p := prefixFor("RegExp", ""); ctorName != "RegExp" {
				base = p + base
			}
			if len(keys) == 0 && len(symKeys) == 0 {
				return base
			}
		} else if class == "Date" {
			base = c.dateBase(obj)
			if ctorName != "Date" {
				base = prefixFor("Date", "") + base
			}
			if len(keys) == 0 && len(symKeys) == 0 {
				return base
			}
		} else if r.isError(obj) {
			base = c.errorBase(obj)
			keys = removeKey(keys, "stack")
			keys = removeKey(keys, "message")
			if len(keys) == 0 && len(symKeys) == 0 {
				return base
			}
		} else if class == "Promise" {
			braces[0] = prefixFor("Promise", "") + "{"
			formatter = func() []string { return c.promiseEntries(obj, recurse) }
			noEntries = false
		} else if class == "Number" || class == "String" || class == "Boolean" || class == "Symbol" {
			if prim, ok := c.boxed(obj, class); ok {
				typeName := class
				base = "[" + typeName
				if ctorName != typeName {
					if !hasCtor {
						base += " (null prototype)"
					} else {
						base += " (" + ctorName + ")"
					}
				}
				base += ": " + prim + "]"
				if tag != "" && tag != ctorName {
					base += " [" + tag + "]"
				}
				if class == "String" {
					keys = filterIndexKeys(keys)
				}
				if len(keys) == 0 && len(symKeys) == 0 {
					return base
				}
			}
		} else {
			if ctorName == "Object" && tag == "" && hasCtor {
				if len(keys) == 0 && len(symKeys) == 0 {
					return "{}"
				}
			} else if !hasCtor || ctorName != "Object" || tag != "" {
				braces[0] = prefixFor("Object", "") + "{"
				if len(keys) == 0 && len(symKeys) == 0 {
					return braces[0] + "}"
				}
			}
		}
	}
	_ = noEntries
	_ = nullProto

	if recurse > c.depth {
		name := ctorName
		if !hasCtor {
			name = "Object: null prototype"
		}
		if isArr {
			name = "Array"
		}
		return "[" + name + "]"
	}
	recurse++
	c.seen[obj] = true
	c.seenList = append(c.seenList, obj)
	c.currentDepth = recurse
	var output []string
	if formatter != nil {
		output = formatter()
	}
	for _, k := range keys {
		output = append(output, c.property(obj, k, recurse, extras, false))
	}
	for _, k := range symKeys {
		output = append(output, c.property(obj, k, recurse, extras, true))
	}
	delete(c.seen, obj)
	c.seenList = c.seenList[:len(c.seenList)-1]

	if idx, ok := c.circular[obj]; ok {
		ref := "<ref *" + strconv.Itoa(idx) + ">"
		if base == "" {
			braces[0] = ref + " " + braces[0]
		} else {
			base = ref + " " + base
		}
	}
	return c.reduce(output, base, braces, extras, recurse, obj)
}

func hasCtorOrTag(class, ctor string) bool { return true }

func symToStringTag(vm *goja.Runtime) *goja.Symbol {
	v, _ := vm.RunString("Symbol.toStringTag")
	s, _ := v.Export().(*goja.Symbol)
	return s
}

func removeKey(keys []goja.Value, name string) []goja.Value {
	var out []goja.Value
	for _, k := range keys {
		if k.String() != name {
			out = append(out, k)
		}
	}
	return out
}

func filterIndexKeys(keys []goja.Value) []goja.Value {
	var out []goja.Value
	for _, k := range keys {
		if !isArrayIndex(k.String()) {
			out = append(out, k)
		}
	}
	return out
}

// keysOf is Object.keys(obj).
func (r *wfRealm) keysOf(obj *goja.Object) []string {
	arr := r.call(r.ownKeys, goja.Undefined(), obj).ToObject(r.vm)
	n := int(arr.Get("length").ToInteger())
	out := make([]string, n)
	for i := range n {
		out[i] = arr.Get(strconv.Itoa(i)).String()
	}
	return out
}

func (r *wfRealm) isError(obj *goja.Object) bool {
	if obj.ClassName() == "Error" {
		return true
	}
	for p := r.protoOf(obj); p != nil; p = r.protoOf(p) {
		if p == r.errorProto || p.SameAs(r.errorProto) {
			return true
		}
	}
	return false
}

func (c *inspectCtx) functionBase(obj *goja.Object, ctorName string, hasCtor bool, tag string) string {
	src := ""
	if v := c.r.vm.ToValue(obj); v != nil {
		src = c.r.call(c.r.mustFn("Function.prototype.toString"), obj).String()
	}
	isClass := strings.HasPrefix(src, "class")
	name, _ := isJSString(obj.Get("name"))
	if isClass {
		b := "[class " + name
		if name == "" {
			b = "[class (anonymous)"
		}
		if ext := c.superName(obj); ext != "" {
			b += " extends " + ext
		}
		return b + "]"
	}
	typ := "Function"
	if strings.HasPrefix(src, "async function*") || strings.HasPrefix(src, "async *") {
		typ = "AsyncGeneratorFunction"
	} else if strings.HasPrefix(src, "function*") || strings.HasPrefix(src, "*") {
		typ = "GeneratorFunction"
	} else if strings.HasPrefix(src, "async ") {
		typ = "AsyncFunction"
	}
	b := "[" + typ
	if !hasCtor {
		b += " (null prototype)"
	}
	if name == "" {
		b += " (anonymous)"
	} else {
		b += ": " + name
	}
	b += "]"
	if hasCtor && ctorName != typ && ctorName != "Function" {
		b += " " + ctorName
	}
	if tag != "" && tag != ctorName {
		b += " [" + tag + "]"
	}
	return b
}

func (c *inspectCtx) superName(obj *goja.Object) string {
	p := c.r.protoOf(obj)
	if p == nil {
		return ""
	}
	if _, callable := goja.AssertFunction(p); !callable {
		return ""
	}
	name, _ := isJSString(p.Get("name"))
	return name
}

func (r *wfRealm) mustFn(src string) goja.Callable {
	v, err := r.vm.RunString(src)
	if err != nil {
		panic(err)
	}
	f, _ := goja.AssertFunction(v)
	return f
}

func (c *inspectCtx) dateBase(obj *goja.Object) string {
	t := obj.Get("getTime")
	if f, ok := goja.AssertFunction(t); ok {
		v, _ := f(obj)
		if f64 := v.ToFloat(); math.IsNaN(f64) {
			return "Invalid Date"
		}
	}
	if f, ok := goja.AssertFunction(obj.Get("toISOString")); ok {
		if v, err := f(obj); err == nil {
			return v.String()
		}
	}
	return "Invalid Date"
}

func (c *inspectCtx) errorBase(obj *goja.Object) string {
	stack, ok := isJSString(obj.Get("stack"))
	if !ok || stack == "" {
		name, _ := isJSString(obj.Get("name"))
		msg, _ := isJSString(obj.Get("message"))
		if name == "" {
			name = "Error"
		}
		if msg != "" {
			stack = name + ": " + msg
		} else {
			stack = name
		}
		return "[" + stack + "]"
	}
	stack = workflowV8Stack(stack)
	if c.indentLvl > 0 {
		lines := strings.Split(stack, "\n")
		for i := 1; i < len(lines); i++ {
			lines[i] = strings.Repeat(" ", c.indentLvl) + lines[i]
		}
		stack = strings.Join(lines, "\n")
	}
	return stack
}

func (c *inspectCtx) boxed(obj *goja.Object, class string) (string, bool) {
	f, ok := goja.AssertFunction(obj.Get("valueOf"))
	if !ok {
		return "", false
	}
	v, err := f(obj)
	if err != nil {
		return "", false
	}
	switch class {
	case "String":
		q, _ := inspectQuote(v.String())
		return q, true
	case "Symbol":
		return v.String(), true
	}
	return c.primitive(v), true
}

func (c *inspectCtx) promiseEntries(obj *goja.Object, recurse int) []string {
	p, ok := obj.Export().(*goja.Promise)
	if !ok {
		return nil
	}
	switch p.State() {
	case goja.PromiseStatePending:
		return []string{"<pending>"}
	case goja.PromiseStateRejected:
		c.indentLvl += 2
		s := c.format(p.Result(), recurse, false)
		c.indentLvl -= 2
		return []string{"<rejected> " + s}
	}
	c.indentLvl += 2
	s := c.format(p.Result(), recurse, false)
	c.indentLvl -= 2
	return []string{s}
}

func (c *inspectCtx) arrayEntries(obj *goja.Object, n, recurse int) []string {
	limit := min(n, 100)
	var output []string
	i := 0
	for i < n && len(output) < limit {
		if !c.r.hasOwnProp(obj, strconv.Itoa(i)) {
			j := i
			for j < n && !c.r.hasOwnProp(obj, strconv.Itoa(j)) {
				j++
			}
			holes := j - i
			s := "s"
			if holes == 1 {
				s = ""
			}
			output = append(output, "<"+strconv.Itoa(holes)+" empty item"+s+">")
			i = j
			continue
		}
		output = append(output, c.property(obj, c.r.vm.ToValue(strconv.Itoa(i)), recurse, kArrayType, false))
		i++
	}
	if rem := n - i; rem > 0 {
		s := "s"
		if rem == 1 {
			s = ""
		}
		output = append(output, "... "+strconv.Itoa(rem)+" more item"+s)
	}
	return output
}

func (c *inspectCtx) iterate(obj *goja.Object, method string, fn func(vals []goja.Value)) {
	f, ok := goja.AssertFunction(obj.Get(method))
	if !ok {
		return
	}
	it, err := f(obj)
	if err != nil {
		return
	}
	itObj := it.ToObject(c.r.vm)
	next, _ := goja.AssertFunction(itObj.Get("next"))
	for {
		res, err := next(itObj)
		if err != nil {
			return
		}
		ro := res.ToObject(c.r.vm)
		if ro.Get("done").ToBoolean() {
			return
		}
		fn([]goja.Value{ro.Get("value")})
	}
}

func (c *inspectCtx) setEntries(obj *goja.Object, recurse int) []string {
	var output []string
	c.indentLvl += 2
	count := 0
	c.iterate(obj, "values", func(vals []goja.Value) {
		if count < 100 {
			output = append(output, c.format(vals[0], recurse, false))
		}
		count++
	})
	c.indentLvl -= 2
	if count > 100 {
		output = append(output, "... "+strconv.Itoa(count-100)+" more item"+plural(count-100))
	}
	return output
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (c *inspectCtx) mapEntries(obj *goja.Object, recurse int) []string {
	var output []string
	c.indentLvl += 2
	count := 0
	c.iterate(obj, "entries", func(vals []goja.Value) {
		if count < 100 {
			pair := vals[0].ToObject(c.r.vm)
			output = append(output, c.format(pair.Get("0"), recurse, false)+" => "+c.format(pair.Get("1"), recurse, false))
		}
		count++
	})
	c.indentLvl -= 2
	if count > 100 {
		output = append(output, "... "+strconv.Itoa(count-100)+" more item"+plural(count-100))
	}
	return output
}

// property is formatProperty.
func (c *inspectCtx) property(obj *goja.Object, key goja.Value, recurse, typ int, isSym bool) string {
	r := c.r
	desc := r.call(r.getDesc, goja.Undefined(), obj, key)
	var str string
	if d, ok := desc.(*goja.Object); ok && !isUndef(d.Get("value")) || (ok && d != nil && r.hasOwnProp(d, "value")) {
		d := desc.ToObject(r.vm)
		diff := 2
		c.indentLvl += diff
		str = c.format(d.Get("value"), recurse, false)
		c.indentLvl -= diff
	} else if d, ok := desc.(*goja.Object); ok {
		get, set := !isUndef(d.Get("get")), !isUndef(d.Get("set"))
		switch {
		case get && set:
			str = "[Getter/Setter]"
		case get:
			str = "[Getter]"
		case set:
			str = "[Setter]"
		default:
			str = "undefined"
		}
	} else {
		str = "undefined"
	}
	if typ == kArrayType {
		return str
	}
	var name string
	if isSym {
		name = "[" + key.String() + "]"
	} else if ks := key.String(); inspectKeyRe(ks) {
		name = ks
	} else {
		name, _ = inspectQuote(ks)
	}
	return name + ": " + str
}

func (c *inspectCtx) reduce(output []string, base string, braces [2]string, extras, recurse int, obj *goja.Object) string {
	entries := len(output)
	if extras == kArrayExtrasType && entries > 6 {
		output = c.groupArrayElements(output, obj)
	}
	if c.currentDepth-recurse < c.compact && entries == len(output) {
		start := len(output) + c.indentLvl + jsLen(braces[0]) + jsLen(base) + 10
		if c.belowBreakLength(output, start, base) {
			joined := strings.Join(output, ", ")
			if !strings.Contains(joined, "\n") {
				b := ""
				if base != "" {
					b = base + " "
				}
				return b + braces[0] + " " + joined + " " + braces[1]
			}
		}
	}
	indentation := "\n" + strings.Repeat(" ", c.indentLvl)
	b := ""
	if base != "" {
		b = base + " "
	}
	return b + braces[0] + indentation + "  " + strings.Join(output, ","+indentation+"  ") + indentation + braces[1]
}

func (c *inspectCtx) belowBreakLength(output []string, start int, base string) bool {
	total := len(output) + start
	if total+len(output) > c.breakLength {
		return false
	}
	for _, o := range output {
		total += jsLen(o)
		if total > c.breakLength {
			return false
		}
	}
	return base == "" || !strings.Contains(base, "\n")
}

// groupArrayElements lays out many short array entries in columns (groupArrayElements in inspect.js).
func (c *inspectCtx) groupArrayElements(output []string, obj *goja.Object) []string {
	totalLength := 0
	maxLength := 0
	i := 0
	outputLength := len(output)
	if c.r != nil && outputLength > 0 && strings.HasPrefix(output[outputLength-1], "... ") && strings.Contains(output[outputLength-1], " more item") {
		outputLength--
	}
	separatorSpace := 2
	dataLen := make([]int, outputLength)
	for ; i < outputLength; i++ {
		l := jsLen(output[i])
		dataLen[i] = l
		totalLength += l + separatorSpace
		if maxLength < l {
			maxLength = l
		}
	}
	actualMax := maxLength + separatorSpace
	if actualMax*3+c.indentLvl < c.breakLength && (float64(totalLength)/float64(actualMax) > 5 || maxLength <= 6) {
		approxCharHeights := 2.5
		averageBias := math.Sqrt(float64(actualMax) - float64(totalLength)/float64(len(output)))
		biasedMax := math.Max(float64(actualMax)-3-averageBias, 1)
		columns := int(math.Min(math.Min(math.Round(math.Sqrt(approxCharHeights*biasedMax*float64(outputLength))/biasedMax), math.Floor(float64(c.breakLength-c.indentLvl)/float64(actualMax))), math.Min(float64(c.compact*4), 15)))
		if columns <= 1 {
			return output
		}
		var tmp []string
		var maxLineLength []int
		for i := 0; i < columns; i++ {
			lineLength := 0
			for j := i; j < len(output); j += columns {
				if j < outputLength && dataLen[j] > lineLength {
					lineLength = dataLen[j]
				}
			}
			maxLineLength = append(maxLineLength, lineLength+separatorSpace)
		}
		padStart := false
		if obj != nil {
			// numbers and bigints are right aligned
			allNum := true
			for idx := 0; idx < int(obj.Get("length").ToInteger()) && idx < outputLength; idx++ {
				t := jsTypeof(obj.Get(strconv.Itoa(idx)))
				if t != "number" && t != "bigint" {
					allNum = false
					break
				}
			}
			padStart = allNum
		}
		for i := 0; i < outputLength; i += columns {
			maxI := min(i+columns, outputLength)
			var str strings.Builder
			j := i
			for ; j < maxI-1; j++ {
				padding := maxLineLength[j-i] + jsLen(output[j]) - dataLen[j]
				cell := output[j] + ", "
				if padStart {
					str.WriteString(strings.Repeat(" ", max(0, padding-jsLen(cell)+0)) + cell)
				} else {
					str.WriteString(cell + strings.Repeat(" ", max(0, padding-jsLen(cell))))
				}
			}
			if padStart {
				padding := maxLineLength[j-i] + jsLen(output[j]) - dataLen[j] - separatorSpace
				str.WriteString(strings.Repeat(" ", max(0, padding-jsLen(output[j]))) + output[j])
			} else {
				str.WriteString(output[j])
			}
			tmp = append(tmp, str.String())
		}
		if outputLength < len(output) {
			tmp = append(tmp, output[outputLength])
		}
		return tmp
	}
	return output
}
