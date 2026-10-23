import Base
import ./utf8.bend as U

# JSON for the PiG wire. See utf8.bend. Numbers keep their source spelling.

type Json is Data:
  JNull{}
  JBool{v: Bool}
  JNum{text: String}
  JStr{s: String}
  JArr{xs: List<&2, Json>}
  JObj{kvs: List<&2, JKV>}

type JKV is Data:
  JKV{key: String, val: Json}

# What a parse answers: the value and the bytes after it, or why not.
type PR is Data:
  POk{val: Json, rest: U.Bytes}
  PErr{msg: String}

type SR is Data:
  SOk{s: String, rest: U.Bytes}
  SErr{msg: String}

# four hex digits: their value and the bytes after, or 65536 and nothing
type HR is Data:
  HOk{v: U32, rest: U.Bytes}
  HErr{}

# ---- scalars -----------------------------------------------------------

# value of a hex digit, or 256
def Json.hexval(+b: U32) -> U32:
  Bool.pick(U32, Bool.and(U32.is_ge(b, 48), U32.is_le(b, 57)), (b - 48 : U32),
    Bool.pick(U32, Bool.and(U32.is_ge(b, 97), U32.is_le(b, 102)), (b - 87 : U32),
      Bool.pick(U32, Bool.and(U32.is_ge(b, 65), U32.is_le(b, 70)), (b - 55 : U32), 256)))

def Json.hex4.chk(ok: Bool, +a: U32, +b: U32, +c: U32, +d: U32, r: U.Bytes) -> HR:
  match ok:
    case True{}:
      HOk{(((a << 12n : U32) .|. (b << 8n : U32) : U32) .|. ((c << 4n : U32) .|. d : U32) : U32), r}
    case False{}:
      HErr{}

def Json.hex4.fin(+a: U32, +b: U32, +c: U32, +d: U32, r: U.Bytes) -> HR:
  Json.hex4.chk(U32.is_lt(U32.or(U32.or(a, b), U32.or(c, d)), 16), a, b, c, d, r)

def Json.hex4(s: U.Bytes) -> HR:
  match s:
    case Con{a, Con{b, Con{c, Con{d, r}}}}:
      Json.hex4.fin(Json.hexval(a), Json.hexval(b), Json.hexval(c), Json.hexval(d), r)
    case _:
      HErr{}

def Json.ws(+b: U32) -> Bool:
  Bool.or(Bool.or(U32.is_eq(b, 32), U32.is_eq(b, 10)), Bool.or(U32.is_eq(b, 13), U32.is_eq(b, 9)))

@unsafe
def Json.skip.if(w: Bool, +b: U32, t: U.Bytes) -> U.Bytes:
  match w:
    case True{}:
      Json.skip(t)
    case False{}:
      Con{b, t}

# drops leading JSON whitespace
@unsafe
def Json.skip(s: U.Bytes) -> U.Bytes:
  match s:
    case Nil{}:
      Nil{}
    case Con{+b, t}:
      Json.skip.if(Json.ws(b), b, t)

# ---- strings -----------------------------------------------------------

def Json.sur.hi(+v: U32) -> Bool:
  Bool.and(U32.is_ge(v, 55296), U32.is_lt(v, 56320))

def Json.sur.lo(+v: U32) -> Bool:
  Bool.and(U32.is_ge(v, 56320), U32.is_lt(v, 57344))

# the supplementary code point of a surrogate pair
def Json.sur.join(+hi: U32, +lo: U32) -> U32:
  (65536 + (((hi - 55296 : U32) << 10n : U32) .|. (lo - 56320 : U32) : U32) : U32)

# after \uXXXX (value v): a high surrogate followed by \uYYYY low surrogate joins
@unsafe
def Json.uesc.pair(ok: Bool, +hi: U32, +lo: U32, r: U.Bytes, r0: U.Bytes, acc: String) -> SR:
  match ok:
    case True{}:
      Json.str(r, SCon{Chr{Json.sur.join(hi, lo)}, acc})
    case False{}:
      Json.str(Con{92, Con{117, r0}}, SCon{Chr{hi}, acc})

# r0 is what follows the \u of the second escape
@unsafe
def Json.uesc.lo(hr: HR, +hi: U32, r0: U.Bytes, acc: String) -> SR:
  match hr:
    case HOk{+lo, r}:
      Json.uesc.pair(Json.sur.lo(lo), hi, lo, r, r0, acc)
    case HErr{}:
      Json.str(Con{92, Con{117, r0}}, SCon{Chr{hi}, acc})

# a high surrogate hi was read; rest follows it
@unsafe
def Json.uesc.next(+hi: U32, rest: U.Bytes, acc: String) -> SR:
  match rest:
    case Con{92, Con{117, +r}}:
      Json.uesc.lo(Json.hex4(r), hi, r, acc)
    case _:
      Json.str(rest, SCon{Chr{hi}, acc})

@unsafe
def Json.uesc.v(hr: HR, acc: String) -> SR:
  match hr:
    case HErr{}:
      SErr{"bad \\u escape"}
    case HOk{+v, r}:
      Json.uesc.v2(Json.sur.hi(v), v, r, acc)

@unsafe
def Json.uesc.v2(hi: Bool, +v: U32, r: U.Bytes, acc: String) -> SR:
  match hi:
    case True{}:
      Json.uesc.next(v, r, acc)
    case False{}:
      Json.str(r, SCon{Chr{v}, acc})

# after a backslash
@unsafe
def Json.esc(s: U.Bytes, acc: String) -> SR:
  match s:
    case Nil{}:
      SErr{"unterminated escape"}
    case Con{b, t}:
      Json.esc.b(b, t, acc)

@unsafe
def Json.esc.b(b: U32, t: U.Bytes, acc: String) -> SR:
  match b:
    case 34:
      Json.str(t, SCon{Chr{34}, acc})
    case 92:
      Json.str(t, SCon{Chr{92}, acc})
    case 47:
      Json.str(t, SCon{Chr{47}, acc})
    case 98:
      Json.str(t, SCon{Chr{8}, acc})
    case 102:
      Json.str(t, SCon{Chr{12}, acc})
    case 110:
      Json.str(t, SCon{Chr{10}, acc})
    case 114:
      Json.str(t, SCon{Chr{13}, acc})
    case 116:
      Json.str(t, SCon{Chr{9}, acc})
    case 117:
      Json.uesc.v(Json.hex4(t), acc)
    case _:
      SErr{"bad escape"}

@unsafe
def Json.str.cp(r: U.CP, acc: String) -> SR:
  match r:
    case U.CPErr{}:
      SErr{"invalid UTF-8 in string"}
    case U.CPOk{c, rest}:
      Json.str(rest, SCon{Chr{c}, acc})

@unsafe
def Json.str.b(b: U32, t: U.Bytes, acc: String) -> SR:
  match b:
    case 34:
      SOk{String.reverse(acc), t}
    case 92:
      Json.esc(t, acc)
    case _:
      Json.str.cp(U.Utf8.read(b, t), acc)

# the body of a string, after its opening quote; acc holds its chars reversed
@unsafe
def Json.str(s: U.Bytes, acc: String) -> SR:
  match s:
    case Nil{}:
      SErr{"unterminated string"}
    case Con{b, t}:
      Json.str.b(b, t, acc)

# ---- numbers and literals ----------------------------------------------

def Json.numch(+b: U32) -> Bool:
  Bool.or(Bool.and(U32.is_ge(b, 48), U32.is_le(b, 57)),
    Bool.or(Bool.or(U32.is_eq(b, 45), U32.is_eq(b, 43)),
      Bool.or(U32.is_eq(b, 46), Bool.or(U32.is_eq(b, 101), U32.is_eq(b, 69)))))

@unsafe
def Json.num.if(ok: Bool, +b: U32, t: U.Bytes, acc: String) -> PR:
  match ok:
    case True{}:
      Json.num(t, SCon{Chr{b}, acc})
    case False{}:
      Json.num.end(Con{b, t}, acc)

def Json.num.end(rest: U.Bytes, acc: String) -> PR:
  POk{JNum{String.reverse(acc)}, rest}

# the digits of a number; acc holds them reversed
@unsafe
def Json.num(s: U.Bytes, acc: String) -> PR:
  match s:
    case Nil{}:
      Json.num.end(Nil{}, acc)
    case Con{+b, t}:
      Json.num.if(Json.numch(b), b, t, acc)

# ---- values ------------------------------------------------------------

@unsafe
def Json.val.str(r: SR) -> PR:
  match r:
    case SErr{m}:
      PErr{m}
    case SOk{s, rest}:
      POk{JStr{s}, rest}

@unsafe
def Json.arr.sep(r: PR, acc: List<&2, Json>) -> PR:
  match r:
    case PErr{m}:
      PErr{m}
    case POk{v, rest}:
      Json.arr.sep2(Json.skip(rest), Con{v, acc})

@unsafe
def Json.arr.sep2(s: U.Bytes, acc: List<&2, Json>) -> PR:
  match s:
    case Con{44, t}:
      Json.arr.sep(Json.value(t), acc)
    case Con{93, t}:
      POk{JArr{List.reverse(&2, Json, acc)}, t}
    case _:
      PErr{"expected , or ] in array"}

# after [
@unsafe
def Json.arr(s: U.Bytes) -> PR:
  Json.arr.first(Json.skip(s))

@unsafe
def Json.arr.first(s: U.Bytes) -> PR:
  match s:
    case Con{93, t}:
      POk{JArr{Nil{}}, t}
    case _:
      Json.arr.sep(Json.value(s), Nil{})

@unsafe
def Json.obj.val(r: PR, key: String, acc: List<&2, JKV>) -> PR:
  match r:
    case PErr{m}:
      PErr{m}
    case POk{v, rest}:
      Json.obj.sep(Json.skip(rest), Con{JKV{key, v}, acc})

@unsafe
def Json.obj.sep(s: U.Bytes, acc: List<&2, JKV>) -> PR:
  match s:
    case Con{44, t}:
      Json.obj.key(Json.skip(t), acc)
    case Con{125, t}:
      POk{JObj{List.reverse(&2, JKV, acc)}, t}
    case _:
      PErr{"expected , or } in object"}

@unsafe
def Json.obj.colon(r: SR, acc: List<&2, JKV>) -> PR:
  match r:
    case SErr{m}:
      PErr{m}
    case SOk{key, rest}:
      Json.obj.colon2(Json.skip(rest), key, acc)

@unsafe
def Json.obj.colon2(s: U.Bytes, key: String, acc: List<&2, JKV>) -> PR:
  match s:
    case Con{58, t}:
      Json.obj.val(Json.value(t), key, acc)
    case _:
      PErr{"expected : after object key"}

# at a key: a string
@unsafe
def Json.obj.key(s: U.Bytes, acc: List<&2, JKV>) -> PR:
  match s:
    case Con{34, t}:
      Json.obj.colon(Json.str(t, SNil{}), acc)
    case _:
      PErr{"expected string key in object"}

# after {
@unsafe
def Json.obj(s: U.Bytes) -> PR:
  Json.obj.first(Json.skip(s))

@unsafe
def Json.obj.first(s: U.Bytes) -> PR:
  match s:
    case Con{125, t}:
      POk{JObj{Nil{}}, t}
    case _:
      Json.obj.key(s, Nil{})

@unsafe
def Json.value.b(+b: U32, t: U.Bytes) -> PR:
  match b:
    case 34:
      Json.val.str(Json.str(t, SNil{}))
    case 91:
      Json.arr(t)
    case 123:
      Json.obj(t)
    case 116:
      Json.lit.true(t)
    case 102:
      Json.lit.false(t)
    case 110:
      Json.lit.null(t)
    case _:
      Json.value.num(Json.numch(b), b, t)

@unsafe
def Json.value.num(ok: Bool, +b: U32, t: U.Bytes) -> PR:
  match ok:
    case True{}:
      Json.num(t, SCon{Chr{b}, SNil{}})
    case False{}:
      PErr{"unexpected byte in JSON"}

@unsafe
def Json.lit.true(t: U.Bytes) -> PR:
  match t:
    case Con{114, Con{117, Con{101, r}}}:
      POk{JBool{True{}}, r}
    case _:
      PErr{"bad literal"}

@unsafe
def Json.lit.false(t: U.Bytes) -> PR:
  match t:
    case Con{97, Con{108, Con{115, Con{101, r}}}}:
      POk{JBool{False{}}, r}
    case _:
      PErr{"bad literal"}

@unsafe
def Json.lit.null(t: U.Bytes) -> PR:
  match t:
    case Con{117, Con{108, Con{108, r}}}:
      POk{JNull{}, r}
    case _:
      PErr{"bad literal"}

# one value, after any whitespace
@unsafe
def Json.value(s: U.Bytes) -> PR:
  Json.value.at(Json.skip(s))

@unsafe
def Json.value.at(s: U.Bytes) -> PR:
  match s:
    case Nil{}:
      PErr{"unexpected end of JSON"}
    case Con{b, t}:
      Json.value.b(b, t)

# the whole input is one value and nothing else
@unsafe
def Json.parse.end(r: PR) -> PR:
  match r:
    case PErr{m}:
      PErr{m}
    case POk{v, rest}:
      Json.parse.tail(v, Json.skip(rest))

@unsafe
def Json.parse.tail(v: Json, rest: U.Bytes) -> PR:
  match rest:
    case Nil{}:
      POk{v, Nil{}}
    case _:
      PErr{"trailing bytes after JSON value"}

@unsafe
def Json.parse(s: U.Bytes) -> PR:
  Json.parse.end(Json.value(s))

# ---- writer ------------------------------------------------------------
# Every writer pushes bytes onto a reversed accumulator; Json.write reverses
# once at the end.

def Json.hexch(+n: U32) -> U32:
  Bool.pick(U32, U32.is_lt(n, 10), (48 + n : U32), (87 + n : U32))

# \uXXXX for a code unit c < 65536
def Json.u4(+c: U32, acc: U.Bytes) -> U.Bytes:
  Con{Json.hexch((c .&. 15 : U32)), Con{Json.hexch(((c >> 4n : U32) .&. 15 : U32)), Con{Json.hexch(((c >> 8n : U32) .&. 15 : U32)), Con{Json.hexch(((c >> 12n : U32) .&. 15 : U32)), Con{117, Con{92, acc}}}}}}

def Json.wchar.d(esc: Bool, +c: U32, acc: U.Bytes) -> U.Bytes:
  match esc:
    case True{}:
      Json.u4(c, acc)
    case False{}:
      U.Utf8.push(c, acc)

def Json.wchar(+c: U32, acc: U.Bytes) -> U.Bytes:
  match c:
    case 34:
      Con{34, Con{92, acc}}
    case 92:
      Con{92, Con{92, acc}}
    case 10:
      Con{110, Con{92, acc}}
    case 13:
      Con{114, Con{92, acc}}
    case 9:
      Con{116, Con{92, acc}}
    case 8:
      Con{98, Con{92, acc}}
    case 12:
      Con{102, Con{92, acc}}
    case _:
      Json.wchar.d(Bool.or(U32.is_lt(c, 32), Bool.and(U32.is_ge(c, 55296), U32.is_lt(c, 57344))), c, acc)

def Json.wstr.go(s: String, acc: U.Bytes) -> U.Bytes:
  match s:
    case SNil{}:
      acc
    case SCon{Chr{c}, t}:
      Json.wstr.go(t, Json.wchar(c, acc))

def Json.wstr(s: String, acc: U.Bytes) -> U.Bytes:
  Con{34, Json.wstr.go(s, Con{34, acc})}

@unsafe
def Json.warr.go(xs: List<&2, Json>, first: Bool, acc: U.Bytes) -> U.Bytes:
  match xs:
    case Nil{}:
      acc
    case Con{h, t}:
      Json.warr.next(first, h, t, acc)

@unsafe
def Json.warr.next(first: Bool, h: Json, t: List<&2, Json>, acc: U.Bytes) -> U.Bytes:
  match first:
    case True{}:
      Json.warr.go(t, False{}, Json.w(h, acc))
    case False{}:
      Json.warr.go(t, False{}, Json.w(h, Con{44, acc}))

@unsafe
def Json.wobj.go(xs: List<&2, JKV>, first: Bool, acc: U.Bytes) -> U.Bytes:
  match xs:
    case Nil{}:
      acc
    case Con{JKV{k, v}, t}:
      Json.wobj.next(first, k, v, t, acc)

@unsafe
def Json.wobj.next(first: Bool, k: String, v: Json, t: List<&2, JKV>, acc: U.Bytes) -> U.Bytes:
  match first:
    case True{}:
      Json.wobj.go(t, False{}, Json.w(v, Con{58, Json.wstr(k, acc)}))
    case False{}:
      Json.wobj.go(t, False{}, Json.w(v, Con{58, Json.wstr(k, Con{44, acc})}))

# bytes of j pushed last-first onto acc
@unsafe
def Json.w(j: Json, acc: U.Bytes) -> U.Bytes:
  match j:
    case JNull{}:
      U.Utf8.put("null", acc)
    case JBool{True{}}:
      U.Utf8.put("true", acc)
    case JBool{False{}}:
      U.Utf8.put("false", acc)
    case JNum{t}:
      U.Utf8.put(t, acc)
    case JStr{s}:
      Json.wstr(s, acc)
    case JArr{xs}:
      Con{93, Json.warr.go(xs, True{}, Con{91, acc})}
    case JObj{kvs}:
      Con{125, Json.wobj.go(kvs, True{}, Con{123, acc})}

# the UTF-8 bytes of j
@unsafe
def Json.write(j: Json) -> U.Bytes:
  U.Bytes.rev(Json.w(j, Nil{}))

# ---- access ------------------------------------------------------------
# Pure and total. A missing member or a wrong type answers None, never a
# default: callers decide what a missing value means.

def Json.get.go(kvs: List<&2, JKV>, +key: String) -> Maybe<&2, Json>:
  match kvs:
    case Nil{}:
      None{}
    case Con{JKV{+k, v}, t}:
      Bool.pick(Maybe<&2, Json>, String.eq(k, key), Some{v}, Json.get.go(t, key))

# the first member named key of an object
def Json.get(j: Json, key: String) -> Maybe<&2, Json>:
  match j:
    case JObj{kvs}:
      Json.get.go(kvs, key)
    case _:
      None{}

def Json.as_str(j: Json) -> Maybe<&2, String>:
  match j:
    case JStr{s}:
      Some{s}
    case _:
      None{}

def Json.as_bool(j: Json) -> Maybe<&2, Bool>:
  match j:
    case JBool{b}:
      Some{b}
    case _:
      None{}

def Json.as_arr(j: Json) -> Maybe<&2, List<&2, Json>>:
  match j:
    case JArr{xs}:
      Some{xs}
    case _:
      None{}

# the number's text, as the sender wrote it
def Json.as_num(j: Json) -> Maybe<&2, String>:
  match j:
    case JNum{t}:
      Some{t}
    case _:
      None{}

# a whole number that fits U32, else None (never truncates or rounds)
def Json.as_u32(j: Json) -> Maybe<&2, U32>:
  match j:
    case JNum{t}:
      U32.read(t)
    case _:
      None{}

def Json.m.str(m: Maybe<&2, Json>) -> Maybe<&2, String>:
  match m:
    case None{}:
      None{}
    case Some{j}:
      Json.as_str(j)

def Json.m.str_or(m: Maybe<&2, String>, d: String) -> String:
  match m:
    case None{}:
      d
    case Some{s}:
      s

# member key of an object as a String, or d when it is missing or not a string
def Json.field_str(j: Json, key: String, d: String) -> String:
  Json.m.str_or(Json.m.str(Json.get(j, key)), d)

# builders
def Json.of_str(s: String) -> Json:
  JStr{s}

def Json.of_num(n: U32) -> Json:
  JNum{U32.show(n)}

def Json.of_bool(b: Bool) -> Json:
  JBool{b}

def Json.of_obj(kvs: List<&2, JKV>) -> Json:
  JObj{kvs}

def Json.of_arr(xs: List<&2, Json>) -> Json:
  JArr{xs}

# reversed accumulate, then reverse: tail calls only
def Json.of_strs.go(xs: List<&2, String>, acc: List<&2, Json>) -> List<&2, Json>:
  match xs:
    case Nil{}:
      List.reverse(&2, Json, acc)
    case Con{h, t}:
      Json.of_strs.go(t, Con{JStr{h}, acc})

def Json.of_strs(xs: List<&2, String>) -> Json:
  JArr{Json.of_strs.go(xs, Nil{})}
