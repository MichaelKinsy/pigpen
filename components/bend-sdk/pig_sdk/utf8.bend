import Base

# JSON and UTF-8 for the PiG wire.
#
# Numbers keep their source spelling (JNum{text}), so a timestamp above 2^32,
# an OAuth `expires` value or a float never rounds through U32 or F32. Objects
# keep member order. Input and output are UTF-8 bytes. A lone UTF-16 surrogate
# (a \uD800..\uDFFF escape with no partner) is kept as a Char and written back
# as the same escape, as the Go SDK's json package does. Loops over bytes are
# tail calls: the JS lane overflows the host stack on deep non-tail recursion.

def Bytes() -> Data:
  List<&2, U32>

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
  POk{val: Json, rest: Bytes}
  PErr{msg: String}

type SR is Data:
  SOk{s: String, rest: Bytes}
  SErr{msg: String}

# One decoded char and the bytes after it.
type CP is Data:
  CPOk{c: U32, rest: Bytes}
  CPErr{}

# ---- bytes -------------------------------------------------------------

def Bytes.rev.go(xs: Bytes, acc: Bytes) -> Bytes:
  match xs:
    case Nil{}:
      acc
    case Con{h, t}:
      Bytes.rev.go(t, Con{h, acc})

def Bytes.rev(xs: Bytes) -> Bytes:
  Bytes.rev.go(xs, Nil{})

# the elements of xs pushed onto acc, last of xs first
def Bytes.rev_onto(xs: Bytes, acc: Bytes) -> Bytes:
  Bytes.rev.go(xs, acc)

def Bytes.len.go(xs: Bytes, acc: U32) -> U32:
  match xs:
    case Nil{}:
      acc
    case Con{h, t}:
      Bytes.len.go(t, (acc + 1 : U32))

def Bytes.len(xs: Bytes) -> U32:
  Bytes.len.go(xs, 0)

# ---- UTF-8 encode ------------------------------------------------------

def Utf8.push4(+c: U32, acc: Bytes) -> Bytes:
  Con{(128 .|. (c .&. 63 : U32) : U32), Con{(128 .|. ((c >> 6n : U32) .&. 63 : U32) : U32), Con{(128 .|. ((c >> 12n : U32) .&. 63 : U32) : U32), Con{(240 .|. (c >> 18n : U32) : U32), acc}}}}

def Utf8.push3(+c: U32, acc: Bytes) -> Bytes:
  Con{(128 .|. (c .&. 63 : U32) : U32), Con{(128 .|. ((c >> 6n : U32) .&. 63 : U32) : U32), Con{(224 .|. (c >> 12n : U32) : U32), acc}}}

def Utf8.push2(+c: U32, acc: Bytes) -> Bytes:
  Con{(128 .|. (c .&. 63 : U32) : U32), Con{(192 .|. (c >> 6n : U32) : U32), acc}}

def Utf8.push.big(+c: U32, +acc: Bytes) -> Bytes:
  Bool.pick(Bytes, U32.is_lt(c, 65536), Utf8.push3(c, acc), Utf8.push4(c, acc))

def Utf8.push.mid(+c: U32, +acc: Bytes) -> Bytes:
  Bool.pick(Bytes, U32.is_lt(c, 2048), Utf8.push2(c, acc), Utf8.push.big(c, acc))

# the char c as UTF-8, its bytes pushed last-first onto acc
def Utf8.push(+c: U32, +acc: Bytes) -> Bytes:
  Bool.pick(Bytes, U32.is_lt(c, 128), Con{c, acc}, Utf8.push.mid(c, acc))

# every char of s as UTF-8, pushed last-first onto acc. A char in D800..DFFF
# (a lone surrogate) has no UTF-8 form: it is encoded as three bytes of the
# WTF-8 kind, which only Json.put_str and Utf8.decode_wtf8 ever see.
def Utf8.put(s: String, acc: Bytes) -> Bytes:
  match s:
    case SNil{}:
      acc
    case SCon{Chr{c}, t}:
      Utf8.put(t, Utf8.push(c, acc))

def Utf8.encode(s: String) -> Bytes:
  Bytes.rev(Utf8.put(s, Nil{}))

# ---- UTF-8 decode ------------------------------------------------------

# the payload of a continuation byte 10xxxxxx, or 256 when b is not one
def Utf8.cont(+b: U32) -> U32:
  Bool.pick(U32, U32.is_eq((b .&. 192 : U32), 128), (b .&. 63 : U32), 256)

@unsafe
def Utf8.more.if(ok: Bool, +n: U32, +v: U32, +c: U32, t: Bytes) -> CP:
  match ok:
    case True{}:
      Utf8.more((n - 1 : U32), ((v << 6n : U32) .|. c : U32), t)
    case False{}:
      CPErr{}

@unsafe
def Utf8.more.ok(+c: U32, +n: U32, +v: U32, t: Bytes) -> CP:
  Utf8.more.if(U32.is_lt(c, 256), n, v, c, t)

# n continuation bytes still to read; v the code point so far
@unsafe
def Utf8.more(+n: U32, +v: U32, t: Bytes) -> CP:
  Utf8.more.n(U32.is_eq(n, 0), n, v, t)

@unsafe
def Utf8.more.n(z: Bool, +n: U32, +v: U32, t: Bytes) -> CP:
  match z:
    case True{}:
      CPOk{v, t}
    case False{}:
      match t:
        case Nil{}:
          CPErr{}
        case Con{b, t2}:
          Utf8.more.ok(Utf8.cont(b), n, v, t2)

# first byte b of a char, then the bytes after it
@unsafe
def Utf8.read(+b: U32, t: Bytes) -> CP:
  Utf8.read.a(U32.is_lt(b, 128), b, t)

@unsafe
def Utf8.read.a(z: Bool, +b: U32, t: Bytes) -> CP:
  match z:
    case True{}:
      CPOk{b, t}
    case False{}:
      Utf8.read.b(U32.is_lt(b, 224), b, t)

@unsafe
def Utf8.read.b(z: Bool, +b: U32, t: Bytes) -> CP:
  match z:
    case True{}:
      Utf8.read.lead(U32.is_ge(b, 192), 1, (b .&. 31 : U32), t)
    case False{}:
      Utf8.read.c(U32.is_lt(b, 240), b, t)

@unsafe
def Utf8.read.c(z: Bool, +b: U32, t: Bytes) -> CP:
  match z:
    case True{}:
      Utf8.more(2, (b .&. 15 : U32), t)
    case False{}:
      Utf8.read.lead(U32.is_lt(b, 248), 3, (b .&. 7 : U32), t)

@unsafe
def Utf8.read.lead(ok: Bool, n: U32, +v: U32, t: Bytes) -> CP:
  match ok:
    case True{}:
      Utf8.more(n, v, t)
    case False{}:
      CPErr{}

# bytes -> chars pushed in reverse onto acc; None on malformed UTF-8
@unsafe
def Utf8.dec(xs: Bytes, acc: String) -> Maybe<&2, String>:
  match xs:
    case Nil{}:
      Some{acc}
    case Con{b, t}:
      Utf8.dec.cp(Utf8.read(b, t), acc)

@unsafe
def Utf8.dec.cp(r: CP, acc: String) -> Maybe<&2, String>:
  match r:
    case CPErr{}:
      None{}
    case CPOk{c, rest}:
      Utf8.dec(rest, SCon{Chr{c}, acc})

def Utf8.decode.fin(r: Maybe<&2, String>) -> Maybe<&2, String>:
  match r:
    case None{}:
      None{}
    case Some{s}:
      Some{String.reverse(s)}

# bytes of valid UTF-8 -> String, or None
def Utf8.decode(xs: Bytes) -> Maybe<&2, String>:
  Utf8.decode.fin(Utf8.dec(xs, SNil{}))
