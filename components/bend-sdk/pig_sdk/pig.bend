import Base
import ./utf8.bend as U
import ./json.bend as J
import ./unix.bend as X

# PiG extension SDK for Bend: the subprocess wire client.
# See README.md. Layout of this file:
#   1. constants and the H monad
#   2. messages, Ctx and frame codec
#   3. reader, writer and the service loop
#   4. the author-facing API

# ---- 1. constants and the H monad --------------------------------------

# Same constant as protocol.go MaxFrameSize and the other SDKs (128 MiB).
def MAX_FRAME_SIZE() -> U32:
  134217728

# A host call or handler step that failed. code is the host's error code when
# it sent one, else "". message is the host's text.
type HErr is Data:
  HErr{code: String, message: String}

# H(A): an IO step that either answers an A or fails with an HErr. A handler
# that ends in Fail answers the host's request with that error, as a raised
# HostCallError does in the Python SDK and a returned error does in Go.
def H(-A: Type) -> Type:
  IO(Result<&1, &1, HErr, A>)

def H.pure(-R: Type, x: R) -> H(R):
  IO.pure(Result<&1, &1, HErr, R>, Done{x})

def H.step(-A: Type, -R: Type, r: Result<&1, &1, HErr, A>, f: A -> H(R)) -> H(R):
  match r:
    case Fail{e}:
      IO.pure(Result<&1, &1, HErr, R>, Fail{e})
    case Done{x}:
      f(x)

def H.bind(-A: Type, -R: Type, m: H(A), f: A -> H(R)) -> H(R):
  IO.bind(Result<&1, &1, HErr, A>, Result<&1, &1, HErr, R>, m, r => H.step(A, R, r, f))

# lifts a plain IO step
def H.io(-A: Type, m: IO(A)) -> H(A):
  IO.bind(A, Result<&1, &1, HErr, A>, m, x => IO.pure(Result<&1, &1, HErr, A>, Done{x}))

def H.fail(-A: Type, code: String, message: String) -> H(A):
  IO.pure(Result<&1, &1, HErr, A>, Fail{HErr{code, message}})

# ---- 2. messages, Ctx and the frame codec ------------------------------

# What the service computation receives.
type Msg is Data:
  MFrame{payload: U.Bytes}
  MEof{}
  MCall{method: String, args: J.Json, parent: String, reply: Chan(J.Json)}
  MDone{id: String}
  MAsk{id: String, reply: Chan(Bool)}

# The handle a handler gets: where to send, and which request it serves.
type Ctx is Data:
  Ctx{svc: Chan(Msg), out: Chan(U.Bytes), req: String, call_id: String, flags: J.Json}

def Wire.drop(-A: Type, r: Result<&1, &1, A, Unit>) -> IO(Unit):
  match r:
    case Fail{v}:
      IO.pure(Unit, Unit{})
    case Done{u}:
      IO.pure(Unit, Unit{})

# send v on c; a closed channel drops it
def Wire.put(-A: Type, c: Chan(A), v: A) -> IO(Unit):
  do IO<Unit>:
    r : Result<&1, &1, A, Unit> <- Chan.send(A, c, v)
    Wire.drop(A, r)

# four big-endian bytes of n
def Wire.be32(+n: U32) -> U.Bytes:
  Con{(n >> 24n : U32), Con{((n >> 16n : U32) .&. 255 : U32), Con{((n >> 8n : U32) .&. 255 : U32), Con{(n .&. 255 : U32), Nil{}}}}}

def Wire.unbe32(h: U.Bytes) -> U32:
  match h:
    case Con{+a, Con{+b, Con{+c, Con{+d, Nil{}}}}}:
      (((a << 24n : U32) .|. (b << 16n : U32) : U32) .|. ((c << 8n : U32) .|. d : U32) : U32)
    case _:
      0

# a payload with its length header
def Wire.frame(+payload: U.Bytes) -> U.Bytes:
  List.append(&2, U32, Wire.be32(U.Bytes.len(payload)), payload)

# the JSON value as one frame
def Wire.frame_of(j: J.Json) -> U.Bytes:
  Wire.frame(J.Json.write(j))

def Wire.send(out: Chan(U.Bytes), j: J.Json) -> IO(Unit):
  Wire.put(U.Bytes, out, Wire.frame_of(j))

# one object member
def kv(k: String, v: J.Json) -> J.JKV:
  J.JKV{k, v}

def jstr(s: String) -> J.Json:
  J.JStr{s}

def jobj(kvs: List<&2, J.JKV>) -> J.Json:
  J.JObj{kvs}

# ---- envelopes ---------------------------------------------------------

def Env.response_err(id: String, message: String) -> J.Json:
  jobj([kv("type", jstr("response")), kv("id", jstr(id)),
    kv("response", jobj([kv("error", jobj([kv("message", jstr(message))]))]))])

def Env.response_ok(id: String, result: J.Json) -> J.Json:
  jobj([kv("type", jstr("response")), kv("id", jstr(id)),
    kv("response", jobj([kv("result", result)]))])

# a response with no result member: the handler returned nothing
def Env.response_none(id: String) -> J.Json:
  jobj([kv("type", jstr("response")), kv("id", jstr(id)), kv("response", jobj(Nil{}))])

def Env.state(id: String, state: String) -> J.Json:
  jobj([kv("type", jstr("request_state")),
    kv("request_state", jobj([kv("request_id", jstr(id)), kv("state", jstr(state))]))])

def Env.state_why(id: String, state: String, reason: String) -> J.Json:
  jobj([kv("type", jstr("request_state")),
    kv("request_state", jobj([kv("request_id", jstr(id)), kv("state", jstr(state)), kv("reason", jstr(reason))]))])

def Env.pong(nonce: String) -> J.Json:
  jobj([kv("type", jstr("pong")), kv("pong", jobj([kv("nonce", jstr(nonce))]))])

def Env.call.parent(+parent: String, +rest: List<&2, J.JKV>) -> List<&2, J.JKV>:
  Bool.pick(List<&2, J.JKV>, String.is_empty(parent), rest, Con{kv("parent_request_id", jstr(parent)), rest})

def Env.call(id: String, method: String, args: J.Json, parent: String) -> J.Json:
  jobj([kv("type", jstr("call")), kv("id", jstr(id)),
    kv("call", jobj(Con{kv("method", jstr(method)), Con{kv("args", args), Env.call.parent(parent, Nil{})}}))])

# ---- 3. reader, writer and the service loop ----------------------------

# what one read answers
type RGot is Data:
  RBytes{bytes: U.Bytes}
  REof{}
  RErr{why: String}

# at most 64 KiB per recv, and never past the end of the wanted bytes, so the
# bytes of the next frame are never read early
def Wire.chunk(+n: U32) -> U32:
  Bool.pick(U32, U32.is_lt(n, 65536), n, 65536)

def Wire.err_text(e: U32 & String) -> String:
  (c, t) = e
  t

@unsafe
def Wire.recv_exact.more(+chunk: U.Bytes, s: Socket, +n: U32, acc: U.Bytes) -> IO(Socket & RGot):
  Wire.recv_exact(s, (n - U.Bytes.len(chunk) : U32), U.Bytes.rev_onto(chunk, acc))

@unsafe
def Wire.recv_exact.some(x: Maybe<&1, List<&2, U32>>, s: Socket, +n: U32, acc: U.Bytes) -> IO(Socket & RGot):
  match x:
    case None{}:
      IO.pure(Socket & RGot, (s, REof{}))
    case Some{chunk}:
      Wire.recv_exact.more(chunk, s, n, acc)

@unsafe
def Wire.recv_exact.got(m: Socket & Result<&1, &1, U32 & String, Maybe<&1, List<&2, U32>>>, +n: U32, acc: U.Bytes) -> IO(Socket & RGot):
  (s, r) = m
  match r:
    case Fail{e}:
      IO.pure(Socket & RGot, (s, RErr{Wire.err_text(e)}))
    case Done{x}:
      Wire.recv_exact.some(x, s, n, acc)

@unsafe
def Wire.recv_exact.z(z: Bool, s: Socket, +n: U32, acc: U.Bytes) -> IO(Socket & RGot):
  match z:
    case True{}:
      IO.pure(Socket & RGot, (s, RBytes{U.Bytes.rev(acc)}))
    case False{}:
      do IO<Socket & RGot>:
        m : Socket & Result<&1, &1, U32 & String, Maybe<&1, List<&2, U32>>> <- TCP.recv_bytes(s, Wire.chunk(n))
        Wire.recv_exact.got(m, n, acc)

# exactly n bytes, or EOF or an error
@unsafe
def Wire.recv_exact(s: Socket, +n: U32, acc: U.Bytes) -> IO(Socket & RGot):
  Wire.recv_exact.z(U32.is_eq(n, 0), s, n, acc)

@unsafe
def Wire.read_frame.ok(ok: Bool, +n: U32, s: Socket) -> IO(Socket & RGot):
  match ok:
    case True{}:
      Wire.recv_exact(s, n, Nil{})
    case False{}:
      IO.pure(Socket & RGot, (s, RErr{"frame too large: " ++ U32.show(n) ++ " bytes"}))

@unsafe
def Wire.read_frame.len(m: Socket & RGot) -> IO(Socket & RGot):
  (s, r) = m
  match r:
    case RBytes{+h}:
      Wire.read_frame.ok(U32.is_le(Wire.unbe32(h), MAX_FRAME_SIZE()), Wire.unbe32(h), s)
    case REof{}:
      IO.pure(Socket & RGot, (s, REof{}))
    case RErr{w}:
      IO.pure(Socket & RGot, (s, RErr{w}))

# one frame's payload bytes: a 4-byte big-endian length, then that many bytes
@unsafe
def Wire.read_frame(s: Socket) -> IO(Socket & RGot):
  do IO<Socket & RGot>:
    m : Socket & RGot <- Wire.recv_exact(s, 4, Nil{})
    Wire.read_frame.len(m)

@unsafe
def Wire.reader.got(m: Socket & RGot, +svc: Chan(Msg)) -> IO(Unit):
  (s, r) = m
  match r:
    case RBytes{b}:
      do IO<Unit>:
        Wire.put(Msg, svc, MFrame{b})
        Wire.reader(s, svc)
    case REof{}:
      do IO<Unit>:
        Socket.close(s)
        Wire.put(Msg, svc, MEof{})
    case RErr{w}:
      do IO<Unit>:
        IO.print_err("pig_sdk: read failed: " ++ w)
        Socket.close(s)
        Wire.put(Msg, svc, MEof{})

# reads frames until the host closes the connection
@unsafe
def Wire.reader(s: Socket, svc: Chan(Msg)) -> IO(Unit):
  do IO<Unit>:
    m : Socket & RGot <- Wire.read_frame(s)
    Wire.reader.got(m, svc)

@unsafe
def Wire.writer.sent(m: Socket & Result<&1, &1, (U32 & String) & List<&2, U32>, Unit>, out: Chan(U.Bytes)) -> IO(Unit):
  (s, r) = m
  match r:
    case Done{u}:
      Wire.writer(s, out)
    case Fail{e}:
      do IO<Unit>:
        IO.print_err("pig_sdk: write failed")
        IO.exit(1)

@unsafe
def Wire.writer.got(m: Maybe<&1, U.Bytes>, s: Socket, +out: Chan(U.Bytes)) -> IO(Unit):
  match m:
    case None{}:
      do IO<Unit>:
        Socket.close(s)
        IO.exit(0)
    case Some{b}:
      do IO<Unit>:
        sm : Socket & Result<&1, &1, (U32 & String) & List<&2, U32>, Unit> <- TCP.send_bytes(s, b)
        Wire.writer.sent(sm, out)

# sends each queued frame whole; ends the process when the channel is closed
# and drained (shutdown) so no response is lost
@unsafe
def Wire.writer(s: Socket, +out: Chan(U.Bytes)) -> IO(Unit):
  do IO<Unit>:
    m : Maybe<&1, U.Bytes> <- Chan.recv(U.Bytes, out)
    Wire.writer.got(m, s, out)

# ---- service state -----------------------------------------------------

# a host call waiting for its call_result
type Pend is Data:
  Pend{id: String, parent: String, reply: Chan(J.Json)}

# next: the next call id. pend: calls in flight. live: request ids a handler is
# serving. dead: live request ids the host cancelled.
type Svc is Data:
  Svc{next: Nat, pend: List<&2, Pend>, live: List<&2, String>, dead: List<&2, String>}

def Wire.rm.cons(+h: String, +id: String, +r: List<&2, String>) -> List<&2, String>:
  Bool.pick(List<&2, String>, String.eq(h, id), r, Con{h, r})

def Wire.rm(xs: List<&2, String>, +id: String) -> List<&2, String>:
  match xs:
    case Nil{}:
      Nil{}
    case Con{+h, t}:
      Wire.rm.cons(h, id, Wire.rm(t, id))

def Wire.mem.cons(+h: String, +id: String, r: Bool) -> Bool:
  Bool.or(String.eq(h, id), r)

def Wire.mem(xs: List<&2, String>, +id: String) -> Bool:
  match xs:
    case Nil{}:
      False{}
    case Con{+h, t}:
      Wire.mem.cons(h, id, Wire.mem(t, id))

def Wire.pend_rm.cons(+p: Pend, +id: String, +r: List<&2, Pend>) -> List<&2, Pend>:
  match p:
    case Pend{+pid, +parent, reply}:
      Bool.pick(List<&2, Pend>, String.eq(pid, id), r, Con{Pend{pid, parent, reply}, r})

def Wire.pend_rm(xs: List<&2, Pend>, +id: String) -> List<&2, Pend>:
  match xs:
    case Nil{}:
      Nil{}
    case Con{+p, t}:
      Wire.pend_rm.cons(p, id, Wire.pend_rm(t, id))

def Wire.pend_find.cons(+p: Pend, +id: String, r: Maybe<&2, Chan(J.Json)>) -> Maybe<&2, Chan(J.Json)>:
  match p:
    case Pend{+pid, parent, reply}:
      Bool.pick(Maybe<&2, Chan(J.Json)>, String.eq(pid, id), Some{reply}, r)

def Wire.pend_find(xs: List<&2, Pend>, +id: String) -> Maybe<&2, Chan(J.Json)>:
  match xs:
    case Nil{}:
      None{}
    case Con{+p, t}:
      Wire.pend_find.cons(p, id, Wire.pend_find(t, id))

# the call_result payload for a host call whose parent request was cancelled
def Wire.cancelled(parent: String) -> J.Json:
  jobj([kv("error", jobj([kv("message", jstr("host call cancelled with its parent request " ++ parent))]))])

@unsafe
def Wire.pend_cancel.if(hit: Bool, +id: String, +p: String, reply: Chan(J.Json), t: List<&2, Pend>, +parent: String, acc: List<&2, Pend>) -> IO(List<&2, Pend>):
  match hit:
    case True{}:
      do IO<List<&2, Pend>>:
        Wire.put(J.Json, reply, Wire.cancelled(parent))
        Wire.pend_cancel(t, parent, acc)
    case False{}:
      Wire.pend_cancel(t, parent, Con{Pend{id, p, reply}, acc})

# answers every pending call of request `parent` with a cancel error; the rest stay
@unsafe
def Wire.pend_cancel(ps: List<&2, Pend>, +parent: String, acc: List<&2, Pend>) -> IO(List<&2, Pend>):
  match ps:
    case Nil{}:
      IO.pure(List<&2, Pend>, acc)
    case Con{Pend{+id, +p, reply}, t}:
      Wire.pend_cancel.if(String.eq(p, parent), id, p, reply, t, parent, acc)

# the envelope type as a code
def Wire.kind(+s: String) -> U32:
  Bool.pick(U32, String.eq(s, "request"), 1,
    Bool.pick(U32, String.eq(s, "call_result"), 2,
      Bool.pick(U32, String.eq(s, "cancel"), 3,
        Bool.pick(U32, String.eq(s, "ping"), 4,
          Bool.pick(U32, String.eq(s, "shutdown"), 5, 0)))))

def Svc.live_add(st: Svc, +id: String) -> Svc:
  match st:
    case Svc{next, pend, live, dead}:
      Svc{next, pend, Con{id, live}, dead}

def Svc.done(st: Svc, +id: String) -> Svc:
  match st:
    case Svc{next, pend, live, dead}:
      Svc{next, pend, Wire.rm(live, id), Wire.rm(dead, id)}

def Svc.pend_set(st: Svc, pend: List<&2, Pend>) -> Svc:
  match st:
    case Svc{next, p0, live, dead}:
      Svc{next, pend, live, dead}

# a live request the host cancelled
def Svc.cancel.if(isl: Bool, st: Svc, +id: String) -> Svc:
  match isl:
    case True{}:
      match st:
        case Svc{next, pend, live, dead}:
          Svc{next, pend, live, Con{id, dead}}
    case False{}:
      st

def Svc.cancel(st: Svc, +id: String) -> Svc:
  match st:
    case Svc{next, pend, +live, dead}:
      Svc.cancel.if(Wire.mem(live, id), Svc{next, pend, live, dead}, id)

# the dispatcher's three-part exit: stop reading and let the writer drain
def Wire.stop(out: Chan(U.Bytes)) -> IO(Unit):
  Chan.close(U.Bytes, out)

def Wire.at.get(m: Maybe<&2, J.Json>, key: String) -> Maybe<&2, J.Json>:
  match m:
    case None{}:
      None{}
    case Some{o}:
      J.Json.get(o, key)

# the member key of the object at `at`, as text
def Wire.at(j: J.Json, outer: String, key: String) -> String:
  J.Json.m.str_or(J.Json.m.str(Wire.at.get(J.Json.get(j, outer), key)), "")

# the object member `key` of j, or JNull{} when absent
def Wire.member.m(m: Maybe<&2, J.Json>) -> J.Json:
  match m:
    case None{}:
      J.JNull{}
    case Some{v}:
      v

def Wire.member(j: J.Json, key: String) -> J.Json:
  Wire.member.m(J.Json.get(j, key))

# ---- the service: one computation owns the connection's state ----------

type Net is Data:
  Net{svc: Chan(Msg), out: Chan(U.Bytes)}

def Net.svc(n: Net) -> Chan(Msg):
  match n:
    case Net{s, o}:
      s

def Net.out(n: Net) -> Chan(U.Bytes):
  match n:
    case Net{s, o}:
      o

def Svc.new() -> Svc:
  Svc{0n, Nil{}, Nil{}, Nil{}}

# a host call from a handler: a call frame to the host, and the handler's
# reply channel parked in `pend`. A call whose parent request was cancelled
# fails at once.
@unsafe
def Wire.svc.call.if(dead: Bool, +next: Nat, pend: List<&2, Pend>, live: List<&2, String>, ds: List<&2, String>, method: String, args: J.Json, +parent: String, +reply: Chan(J.Json), out: Chan(U.Bytes)) -> IO(Svc):
  match dead:
    case True{}:
      do IO<Svc>:
        Wire.put(J.Json, reply, Wire.cancelled(parent))
        IO.pure(Svc, Svc{next, pend, live, ds})
    case False{}:
      do IO<Svc>:
        Wire.send(out, Env.call("c" ++ Nat.show(next), method, args, parent))
        IO.pure(Svc, Svc{1n+next, Con{Pend{"c" ++ Nat.show(next), parent, reply}, pend}, live, ds})

@unsafe
def Wire.svc.call(st: Svc, method: String, args: J.Json, +parent: String, reply: Chan(J.Json), out: Chan(U.Bytes)) -> IO(Svc):
  match st:
    case Svc{+next, pend, live, +dead}:
      Wire.svc.call.if(Wire.mem(dead, parent), next, pend, live, dead, method, args, parent, reply, out)

# the payload a call_result carried goes to the handler waiting for it
@unsafe
def Wire.svc.result.to(m: Maybe<&2, Chan(J.Json)>, payload: J.Json) -> IO(Unit):
  match m:
    case None{}:
      IO.pure(Unit, Unit{})
    case Some{reply}:
      Wire.put(J.Json, reply, payload)

@unsafe
def Wire.svc.result(+id: String, payload: J.Json, st: Svc) -> IO(Svc):
  match st:
    case Svc{next, +pend, live, dead}:
      do IO<Svc>:
        Wire.svc.result.to(Wire.pend_find(pend, id), payload)
        IO.pure(Svc, Svc{next, Wire.pend_rm(pend, id), live, dead})

@unsafe
def Wire.svc.cancel(+rid: String, st: Svc) -> IO(Svc):
  match st:
    case Svc{next, pend, live, dead}:
      do IO<Svc>:
        +pend2 : List<&2, Pend> <- Wire.pend_cancel(pend, rid, Nil{})
        IO.pure(Svc, Svc.cancel(Svc{next, pend2, live, dead}, rid))

@unsafe
def Wire.svc.request(~dispatch: Ctx -> J.Json -> IO(Unit), +id: String, req: J.Json, st: Svc, +net: Net) -> IO(Svc):
  do IO<Svc>:
    IO.spawn(Unit, dispatch(Ctx{Net.svc(net), Net.out(net), id, "", J.JNull{}}, req))
    IO.pure(Svc, Svc.live_add(st, id))

@unsafe
def Wire.svc.env(~dispatch: Ctx -> J.Json -> IO(Unit), k: U32, +env: J.Json, st: Svc, +net: Net) -> IO(Svc):
  match k:
    case 1:
      Wire.svc.request(~dispatch, J.Json.field_str(env, "id", ""), Wire.member(env, "request"), st, net)
    case 2:
      Wire.svc.result(J.Json.field_str(env, "id", ""), Wire.member(env, "call_result"), st)
    case 3:
      Wire.svc.cancel(Wire.at(env, "cancel", "request_id"), st)
    case 4:
      do IO<Svc>:
        Wire.send(Net.out(net), Env.pong(Wire.at(env, "ping", "nonce")))
        IO.pure(Svc, st)
    case 5:
      do IO<Svc>:
        Wire.stop(Net.out(net))
        IO.pure(Svc, st)
    case _:
      IO.pure(Svc, st)

@unsafe
def Wire.svc.frame(~dispatch: Ctx -> J.Json -> IO(Unit), r: J.PR, st: Svc, net: Net) -> IO(Svc):
  match r:
    case J.PErr{m}:
      do IO<Svc>:
        IO.print_err("pig_sdk: malformed frame from host: " ++ m)
        IO.exit(1)
        IO.pure(Svc, st)
    case J.POk{+env, rest}:
      Wire.svc.env(~dispatch, Wire.kind(J.Json.field_str(env, "type", "")), env, st, net)

@unsafe
def Wire.svc.ask(+id: String, reply: Chan(Bool), st: Svc) -> IO(Svc):
  match st:
    case Svc{next, pend, +live, +dead}:
      do IO<Svc>:
        Wire.put(Bool, reply, Wire.mem(dead, id))
        IO.pure(Svc, Svc{next, pend, live, dead})

@unsafe
def Wire.svc.on(~dispatch: Ctx -> J.Json -> IO(Unit), msg: Msg, st: Svc, +net: Net) -> IO(Svc):
  match msg:
    case MFrame{b}:
      Wire.svc.frame(~dispatch, J.Json.parse(b), st, net)
    case MEof{}:
      do IO<Svc>:
        Wire.stop(Net.out(net))
        IO.pure(Svc, st)
    case MCall{method, args, parent, reply}:
      Wire.svc.call(st, method, args, parent, reply, Net.out(net))
    case MDone{id}:
      IO.pure(Svc, Svc.done(st, id))
    case MAsk{id, reply}:
      Wire.svc.ask(id, reply, st)

@unsafe
def Wire.svc.got(~dispatch: Ctx -> J.Json -> IO(Unit), m: Maybe<&1, Msg>, st: Svc, net: Net) -> IO(Svc):
  match m:
    case None{}:
      IO.pure(Svc, st)
    case Some{msg}:
      Wire.svc.on(~dispatch, msg, st, net)

# handles messages for the life of the connection. Shutdown and EOF close the
# frame queue; the writer sends what is queued and ends the process.
@unsafe
def Wire.svc(~dispatch: Ctx -> J.Json -> IO(Unit), st: Svc, +net: Net) -> IO(Unit):
  do IO<Unit>:
    m : Maybe<&1, Msg> <- Chan.recv(Msg, Net.svc(net))
    st2 : Svc <- Wire.svc.got(~dispatch, m, st, net)
    Wire.svc(~dispatch, st2, net)

# ---- host calls --------------------------------------------------------

def Wire.call.text(+code: String, +message: String) -> String:
  Bool.pick(String, String.is_empty(code), message, code ++ ": " ++ message)

def Wire.call.err(e: Maybe<&2, J.Json>, p: J.Json) -> H(J.Json):
  match e:
    case Some{+err}:
      H.fail(J.Json, J.Json.field_str(err, "code", ""), Wire.call.text(J.Json.field_str(err, "code", ""), J.Json.field_str(err, "message", "")))
    case None{}:
      H.pure(J.Json, Wire.member(p, "result"))

def Wire.call.fin(m: Maybe<&1, J.Json>) -> H(J.Json):
  match m:
    case None{}:
      H.fail(J.Json, "", "extension connection closed")
    case Some{+p}:
      Wire.call.err(J.Json.get(p, "error"), p)

# why a call blocks: "user" for dialogs, else "host_call"
def Wire.call.state(ctx: Ctx, state: String, reason: String) -> IO(Unit):
  match ctx:
    case Ctx{svc, out, req, cid, fl}:
      Wire.send(out, Env.state_why(req, state, reason))

def Wire.call.progress(ctx: Ctx) -> IO(Unit):
  match ctx:
    case Ctx{svc, out, req, cid, fl}:
      Wire.send(out, Env.state(req, "progress"))

# one host call: a call frame, then the host's result. blocked/progress are
# reported around it as the other SDKs do.
def Wire.call_as(+ctx: Ctx, reason: String, method: String, args: J.Json) -> H(J.Json):
  match ctx:
    case Ctx{+svc, +out, +req, cid, fl}:
      do IO<Result<&1, &1, HErr, J.Json>>:
        Wire.send(out, Env.state_why(req, "blocked", reason))
        +reply : Chan(J.Json) <- Chan.new(J.Json, 1)
        Wire.put(Msg, svc, MCall{method, args, req, reply})
        m : Maybe<&1, J.Json> <- Chan.recv(J.Json, reply)
        Wire.send(out, Env.state(req, "progress"))
        Wire.call.fin(m)

def Wire.call(ctx: Ctx, method: String, args: J.Json) -> H(J.Json):
  Wire.call_as(ctx, "host_call", method, args)

# ---- 4. the author-facing API ------------------------------------------

# What the author registers. Handlers are functions of the request's Ctx. The
# extension value is rebuilt from the author's `new_extension` for every
# request (closures in Bend are affine, so each handler runs once per value).

type Tool is Type:
  Tool{name: String, label: String, description: String, schema: String, mode: String, snippet: String, guidelines: List<&2, String>, run: J.Json -> Ctx -> H(J.Json)}

type Cmd is Type:
  Cmd{name: String, description: String, hint: String, run: String -> Ctx -> H(Unit)}

type Short is Type:
  Short{key: String, description: String, run: Ctx -> H(Unit)}

# An event handler. block says the handler may return a blocking result.
# Its result is the JSON the host reads for that event, or None for no result.
type On is Type:
  On{event: String, block: Bool, run: J.Json -> Ctx -> H(Maybe<&2, J.Json>)}

# kind: "boolean" or "string"; default is the JSON default
type Flag is Data:
  Flag{name: String, description: String, kind: String, default: J.Json}

type Ext is Type:
  Ext{name: String, version: String, tools: List<Tool>, cmds: List<Cmd>, shorts: List<Short>, ons: List<On>, flags: List<&2, Flag>}

# A tool: name, description, the parameters as JSON Schema text, and the handler.
def Tool.new(name: String, description: String, schema: String, run: J.Json -> Ctx -> H(J.Json)) -> Tool:
  Tool{name, "", description, schema, "", "", Nil{}, run}

def Tool.label(t: Tool, label: String) -> Tool:
  match t:
    case Tool{name, l, description, schema, mode, snippet, guidelines, run}:
      Tool{name, label, description, schema, mode, snippet, guidelines, run}

# "sequential" or "parallel"
def Tool.mode(t: Tool, mode: String) -> Tool:
  match t:
    case Tool{name, label, description, schema, m, snippet, guidelines, run}:
      Tool{name, label, description, schema, mode, snippet, guidelines, run}

def Tool.snippet(t: Tool, snippet: String) -> Tool:
  match t:
    case Tool{name, label, description, schema, mode, s, guidelines, run}:
      Tool{name, label, description, schema, mode, snippet, guidelines, run}

def Tool.guidelines(t: Tool, guidelines: List<&2, String>) -> Tool:
  match t:
    case Tool{name, label, description, schema, mode, snippet, g, run}:
      Tool{name, label, description, schema, mode, snippet, guidelines, run}

def Cmd.new(name: String, description: String, run: String -> Ctx -> H(Unit)) -> Cmd:
  Cmd{name, description, "", run}

def Cmd.hint(c: Cmd, hint: String) -> Cmd:
  match c:
    case Cmd{name, description, h, run}:
      Cmd{name, description, hint, run}

def Short.new(key: String, description: String, run: Ctx -> H(Unit)) -> Short:
  Short{key, description, run}

def On.new(event: String, run: J.Json -> Ctx -> H(Maybe<&2, J.Json>)) -> On:
  On{event, False{}, run}

def On.blocking(event: String, run: J.Json -> Ctx -> H(Maybe<&2, J.Json>)) -> On:
  On{event, True{}, run}

def Flag.bool(name: String, description: String, default: Bool) -> Flag:
  Flag{name, description, "boolean", J.JBool{default}}

def Flag.string(name: String, description: String, default: String) -> Flag:
  Flag{name, description, "string", J.JStr{default}}

def Ext.new(name: String, version: String) -> Ext:
  Ext{name, version, Nil{}, Nil{}, Nil{}, Nil{}, Nil{}}

def Ext.tools(e: Ext, tools: List<Tool>) -> Ext:
  match e:
    case Ext{name, version, t, cmds, shorts, ons, flags}:
      Ext{name, version, tools, cmds, shorts, ons, flags}

def Ext.cmds(e: Ext, cmds: List<Cmd>) -> Ext:
  match e:
    case Ext{name, version, tools, c, shorts, ons, flags}:
      Ext{name, version, tools, cmds, shorts, ons, flags}

def Ext.shorts(e: Ext, shorts: List<Short>) -> Ext:
  match e:
    case Ext{name, version, tools, cmds, s, ons, flags}:
      Ext{name, version, tools, cmds, shorts, ons, flags}

def Ext.ons(e: Ext, ons: List<On>) -> Ext:
  match e:
    case Ext{name, version, tools, cmds, shorts, o, flags}:
      Ext{name, version, tools, cmds, shorts, ons, flags}

def Ext.flags(e: Ext, flags: List<&2, Flag>) -> Ext:
  match e:
    case Ext{name, version, tools, cmds, shorts, ons, f}:
      Ext{name, version, tools, cmds, shorts, ons, flags}

# ---- results ----

# {"content":[{"type":"text","text":s}]}
def Result.text(s: String) -> J.Json:
  jobj([kv("content", J.JArr{[jobj([kv("type", jstr("text")), kv("text", jstr(s))])]})])

# the same with is_error set: a tool failure the model sees
def Result.error_text(s: String) -> J.Json:
  jobj([kv("content", J.JArr{[jobj([kv("type", jstr("text")), kv("text", jstr(s))])]}), kv("is_error", J.JBool{True{}})])

def Result.with_details(r: J.Json, details: J.Json) -> J.Json:
  match r:
    case J.JObj{kvs}:
      J.JObj{List.append(&2, J.JKV, kvs, [kv("details", details)])}
    case _:
      r

# ---- registration ----

# omits a member whose text is empty
def Reg.opt(+k: String, +v: String, +rest: List<&2, J.JKV>) -> List<&2, J.JKV>:
  Bool.pick(List<&2, J.JKV>, String.is_empty(v), rest, Con{kv(k, jstr(v)), rest})

def Reg.fatal(why: String) -> IO(Unit):
  do IO<Unit>:
    IO.print_err("pig_sdk: " ++ why)
    IO.exit(1)

def Reg.schema.ok(r: J.PR, name: String) -> IO(J.Json):
  match r:
    case J.PErr{m}:
      do IO<J.Json>:
        Reg.fatal("tool " ++ name ++ ": parameters are not valid JSON Schema text: " ++ m)
        IO.pure(J.Json, J.JNull{})
    case J.POk{v, rest}:
      IO.pure(J.Json, v)

def Reg.guide(g: List<&2, String>) -> List<&2, J.JKV>:
  match g:
    case Nil{}:
      Nil{}
    case Con{h, t}:
      [kv("prompt_guidelines", J.Json.of_strs(Con{h, t}))]

def Reg.tool(t: Tool) -> IO(J.Json):
  match t:
    case Tool{+name, label, description, schema, mode, snippet, guidelines, run}:
      do IO<J.Json>:
        +params : J.Json <- Reg.schema.ok(J.Json.parse(U.Utf8.encode(schema)), name)
        IO.pure(J.Json, jobj(Con{kv("name", jstr(name)), Reg.opt("label", label, Con{kv("description", jstr(description)), Con{kv("parameters", params), Reg.opt("execution_mode", mode, Reg.opt("prompt_snippet", snippet, Reg.guide(guidelines)))}})}))

@unsafe
def Reg.tools(ts: List<Tool>, acc: List<&2, J.Json>) -> IO(List<&2, J.Json>):
  match ts:
    case Nil{}:
      IO.pure(List<&2, J.Json>, List.reverse(&2, J.Json, acc))
    case Con{t, rest}:
      do IO<List<&2, J.Json>>:
        +j : J.Json <- Reg.tool(t)
        Reg.tools(rest, Con{j, acc})

def Reg.cmd(c: Cmd) -> J.Json:
  match c:
    case Cmd{+name, +description, hint, run}:
      jobj(Con{kv("name", jstr(name)), Con{kv("description", jstr(description)), Reg.opt("args", hint, Nil{})}})

def Reg.cmds(cs: List<Cmd>, acc: List<&2, J.Json>) -> List<&2, J.Json>:
  match cs:
    case Nil{}:
      List.reverse(&2, J.Json, acc)
    case Con{c, rest}:
      Reg.cmds(rest, Con{Reg.cmd(c), acc})

def Reg.short(s: Short) -> J.Json:
  match s:
    case Short{key, description, run}:
      jobj([kv("key", jstr(key)), kv("description", jstr(description))])

def Reg.shorts(ss: List<Short>, acc: List<&2, J.Json>) -> List<&2, J.Json>:
  match ss:
    case Nil{}:
      List.reverse(&2, J.Json, acc)
    case Con{s, rest}:
      Reg.shorts(rest, Con{Reg.short(s), acc})

def Reg.flag(f: Flag) -> J.Json:
  match f:
    case Flag{+name, description, kind, default}:
      jobj(Con{kv("name", jstr(name)), Reg.opt("description", description, Con{kv("type", jstr(kind)), [kv("default", default)]})})

def Reg.flags(fs: List<&2, Flag>, acc: List<&2, J.Json>) -> List<&2, J.Json>:
  match fs:
    case Nil{}:
      List.reverse(&2, J.Json, acc)
    case Con{f, rest}:
      Reg.flags(rest, Con{Reg.flag(f), acc})

# Events whose results the Go SDK rewrites with host wrappers; the Bend SDK
# does not implement them, so registering one fails at load.
def Reg.event.bad(+e: String) -> Bool:
  Bool.or(Bool.or(String.eq(e, "before_agent_start"), String.eq(e, "agent_before_settle")),
    Bool.or(Bool.or(String.eq(e, "turn_end"), String.eq(e, "user_bash")),
      Bool.or(String.eq(e, "context"), String.eq(e, "context_with_system"))))

def Reg.on.check(bad: Bool, event: String) -> IO(Unit):
  match bad:
    case True{}:
      Reg.fatal("event " ++ event ++ " is not supported by the Bend SDK (see README, unsupported capabilities)")
    case False{}:
      IO.pure(Unit, Unit{})

# handler ids are 1-based registration order, as the wire requires positive ids
@unsafe
def Reg.ons(os: List<On>, +i: U32, acc: List<&2, J.Json>) -> IO(List<&2, J.Json>):
  match os:
    case Nil{}:
      IO.pure(List<&2, J.Json>, List.reverse(&2, J.Json, acc))
    case Con{On{+event, block, run}, rest}:
      do IO<List<&2, J.Json>>:
        Reg.on.check(Reg.event.bad(event), event)
        Reg.ons(rest, (i + 1 : U32), Con{jobj([kv("event", jstr(event)), kv("can_block", J.JBool{block}), kv("handler_id", J.Json.of_num(i))]), acc})

# a member naming a non-empty list
def Reg.list(+k: String, +xs: List<&2, J.Json>, +rest: List<&2, J.JKV>) -> List<&2, J.JKV>:
  match xs:
    case Nil{}:
      rest
    case Con{h, t}:
      Con{kv(k, J.JArr{Con{h, t}}), rest}

def Reg.json(e: Ext) -> IO(J.Json):
  match e:
    case Ext{+name, +version, tools, cmds, shorts, ons, flags}:
      do IO<J.Json>:
        +tj : List<&2, J.Json> <- Reg.tools(tools, Nil{})
        +oj : List<&2, J.Json> <- Reg.ons(ons, 1, Nil{})
        IO.pure(J.Json, jobj(Con{kv("name", jstr(name)), Reg.opt("version", version, Reg.list("tools", tj, Reg.list("commands", Reg.cmds(cmds, Nil{}), Reg.list("shortcuts", Reg.shorts(shorts, Nil{}), Reg.list("handlers", oj, Reg.list("flags", Reg.flags(flags, Nil{}), Nil{}))))))}))

def Ctx.out(c: Ctx) -> Chan(U.Bytes):
  match c:
    case Ctx{svc, out, req, cid, fl}:
      out

def Ctx.req(c: Ctx) -> String:
  match c:
    case Ctx{svc, out, req, cid, fl}:
      req

def Ctx.svc(c: Ctx) -> Chan(Msg):
  match c:
    case Ctx{svc, out, req, cid, fl}:
      svc

def Ctx.call_id(c: Ctx) -> String:
  match c:
    case Ctx{svc, out, req, cid, fl}:
      cid

def Ctx.with_flags(c: Ctx, flags: J.Json) -> Ctx:
  match c:
    case Ctx{svc, out, req, cid, old}:
      Ctx{svc, out, req, cid, flags}

def Flags.go(fs: List<&2, Flag>, acc: List<&2, J.JKV>) -> List<&2, J.JKV>:
  match fs:
    case Nil{}:
      List.reverse(&2, J.JKV, acc)
    case Con{Flag{n, d, k, v}, t}:
      Flags.go(t, Con{J.JKV{n, v}, acc})

# the declared defaults by flag name
def Flags.json(e: Ext) -> J.Json:
  match e:
    case Ext{name, version, tools, cmds, shorts, ons, flags}:
      jobj(Flags.go(flags, Nil{}))

# ---- dispatch ----------------------------------------------------------

def ToolFn() -> Type:
  J.Json -> Ctx -> H(J.Json)

def CmdFn() -> Type:
  String -> Ctx -> H(Unit)

def ShortFn() -> Type:
  Ctx -> H(Unit)

def OnFn() -> Type:
  J.Json -> Ctx -> H(Maybe<&2, J.Json>)

def Find.tool.cons(t: Tool, +name: String, r: Maybe<ToolFn>) -> Maybe<ToolFn>:
  match t:
    case Tool{+n, label, description, schema, mode, snippet, guidelines, run}:
      Bool.pick(Maybe<ToolFn>, String.eq(n, name), Some{run}, r)

def Find.tool(ts: List<Tool>, +name: String) -> Maybe<ToolFn>:
  match ts:
    case Nil{}:
      None{}
    case Con{t, rest}:
      Find.tool.cons(t, name, Find.tool(rest, name))

def Find.cmd.cons(c: Cmd, +name: String, r: Maybe<CmdFn>) -> Maybe<CmdFn>:
  match c:
    case Cmd{+n, description, hint, run}:
      Bool.pick(Maybe<CmdFn>, String.eq(n, name), Some{run}, r)

def Find.cmd(cs: List<Cmd>, +name: String) -> Maybe<CmdFn>:
  match cs:
    case Nil{}:
      None{}
    case Con{c, rest}:
      Find.cmd.cons(c, name, Find.cmd(rest, name))

def Find.short.cons(s: Short, +key: String, r: Maybe<ShortFn>) -> Maybe<ShortFn>:
  match s:
    case Short{+k, description, run}:
      Bool.pick(Maybe<ShortFn>, String.eq(k, key), Some{run}, r)

def Find.short(ss: List<Short>, +key: String) -> Maybe<ShortFn>:
  match ss:
    case Nil{}:
      None{}
    case Con{s, rest}:
      Find.short.cons(s, key, Find.short(rest, key))

def Find.on.cons(hit: Bool, o: On, r: Maybe<OnFn>) -> Maybe<OnFn>:
  match o:
    case On{event, block, run}:
      Bool.pick(Maybe<OnFn>, hit, Some{run}, r)

def Find.on(os: List<On>, +i: U32, +want: U32) -> Maybe<OnFn>:
  match os:
    case Nil{}:
      None{}
    case Con{o, rest}:
      Find.on.cons(U32.is_eq(i, want), o, Find.on(rest, (i + 1 : U32), want))

def Ctx.with_call_id(c: Ctx, id: String) -> Ctx:
  match c:
    case Ctx{svc, out, req, old, fl}:
      Ctx{svc, out, req, id, fl}

def Dispatch.tool.run(m: Maybe<ToolFn>, +name: String, args: J.Json, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match m:
    case None{}:
      H.fail(Maybe<&2, J.Json>, "", "unknown tool: " ++ name)
    case Some{run}:
      do H<Maybe<&2, J.Json>>:
        r : J.Json <- run(args, ctx)
        return Some{r}

def Dispatch.tool(ext: Ext, +name: String, args: J.Json, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match ext:
    case Ext{n, v, tools, cmds, shorts, ons, flags}:
      Dispatch.tool.run(Find.tool(tools, name), name, args, ctx)

def Dispatch.cmd.run(m: Maybe<CmdFn>, +name: String, a: String, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match m:
    case None{}:
      H.fail(Maybe<&2, J.Json>, "", "unknown command: " ++ name)
    case Some{run}:
      do H<Maybe<&2, J.Json>>:
        u : Unit <- run(a, ctx)
        return None{}

def Dispatch.cmd(ext: Ext, +name: String, args: J.Json, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match ext:
    case Ext{n, v, tools, cmds, shorts, ons, flags}:
      Dispatch.cmd.run(Find.cmd(cmds, name), name, J.Json.m.str_or(J.Json.as_str(args), ""), ctx)

def Dispatch.short.run(m: Maybe<ShortFn>, +key: String, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match m:
    case None{}:
      H.fail(Maybe<&2, J.Json>, "", "unknown shortcut: " ++ key)
    case Some{run}:
      do H<Maybe<&2, J.Json>>:
        u : Unit <- run(ctx)
        return None{}

def Dispatch.short(ext: Ext, +key: String, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match ext:
    case Ext{n, v, tools, cmds, shorts, ons, flags}:
      Dispatch.short.run(Find.short(shorts, key), key, ctx)

def Dispatch.event.orn(r: Maybe<&2, J.Json>) -> J.Json:
  match r:
    case None{}:
      J.JNull{}
    case Some{j}:
      j

def Dispatch.event.wrap2(tc: Bool, r: Maybe<&2, J.Json>) -> Maybe<&2, J.Json>:
  match tc:
    case False{}:
      r
    case True{}:
      Some{jobj([kv("_pigToolCallResult", Dispatch.event.orn(r))])}

# a tool_call event reply must be {"_pigToolCallResult": result}
def Dispatch.event.wrap(+event: String, r: Maybe<&2, J.Json>) -> Maybe<&2, J.Json>:
  Dispatch.event.wrap2(String.eq(event, "tool_call"), r)

def Dispatch.event.run(m: Maybe<OnFn>, +event: String, id: String, data: J.Json, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match m:
    case None{}:
      H.fail(Maybe<&2, J.Json>, "", "unknown event handler " ++ id ++ " for " ++ event)
    case Some{run}:
      do H<Maybe<&2, J.Json>>:
        r : Maybe<&2, J.Json> <- run(data, ctx)
        return Dispatch.event.wrap(event, r)

def Dispatch.event.id(m: Maybe<&2, U32>) -> U32:
  match m:
    case None{}:
      0
    case Some{n}:
      n

def Dispatch.event(ext: Ext, +req: J.Json, ctx: Ctx) -> H(Maybe<&2, J.Json>):
  match ext:
    case Ext{n, v, tools, cmds, shorts, ons, flags}:
      Dispatch.event.run(Find.on(ons, 1, Dispatch.event.id(J.Json.as_u32(Wire.member(req, "handler_id")))), J.Json.field_str(req, "event", ""), U32.show(Dispatch.event.id(J.Json.as_u32(Wire.member(req, "handler_id")))), Wire.member(req, "args"), ctx)

def Dispatch.kind(+m: String) -> U32:
  Bool.pick(U32, String.eq(m, "tool_call"), 1,
    Bool.pick(U32, String.eq(m, "command"), 2,
      Bool.pick(U32, String.eq(m, "event"), 3,
        Bool.pick(U32, String.eq(m, "shortcut"), 4, 0))))

def Dispatch.method(~f: Unit -> Ext, k: U32, ctx: Ctx, +req: J.Json) -> H(Maybe<&2, J.Json>):
  match k:
    case 1:
      Dispatch.tool(f(Unit{}), J.Json.field_str(req, "tool", ""), Wire.member(req, "args"), Ctx.with_call_id(ctx, J.Json.field_str(req, "tool_call_id", "")))
    case 2:
      Dispatch.cmd(f(Unit{}), J.Json.field_str(req, "tool", ""), Wire.member(req, "args"), ctx)
    case 3:
      Dispatch.event(f(Unit{}), req, ctx)
    case 4:
      Dispatch.short(f(Unit{}), J.Json.field_str(req, "tool", ""), ctx)
    case _:
      H.fail(Maybe<&2, J.Json>, "", "unknown request method: " ++ J.Json.field_str(req, "method", ""))

def Dispatch.answer.ok(x: Maybe<&2, J.Json>, out: Chan(U.Bytes), id: String) -> IO(Unit):
  match x:
    case None{}:
      Wire.send(out, Env.response_none(id))
    case Some{j}:
      Wire.send(out, Env.response_ok(id, j))

def Dispatch.answer(r: Result<&1, &1, HErr, Maybe<&2, J.Json>>, out: Chan(U.Bytes), id: String) -> IO(Unit):
  match r:
    case Fail{HErr{c, m}}:
      Wire.send(out, Env.response_err(id, m))
    case Done{x}:
      Dispatch.answer.ok(x, out, id)

# completed, then the response: the order every SDK uses
def Dispatch.finish(r: Result<&1, &1, HErr, Maybe<&2, J.Json>>, ctx: Ctx) -> IO(Unit):
  match ctx:
    case Ctx{svc, +out, +id, cid, fl}:
      do IO<Unit>:
        Wire.send(out, Env.state(id, "completed"))
        Dispatch.answer(r, out, id)
        Wire.put(Msg, svc, MDone{id})

# one request: started, the handler, completed, response
def Dispatch.go(~f: Unit -> Ext, +ctx: Ctx, +req: J.Json) -> IO(Unit):
  do IO<Unit>:
    Wire.send(Ctx.out(ctx), Env.state(Ctx.req(ctx), "started"))
    r : Result<&1, &1, HErr, Maybe<&2, J.Json>> <- Dispatch.method(~f, Dispatch.kind(J.Json.field_str(req, "method", "")), Ctx.with_flags(ctx, Flags.json(f(Unit{}))), req)
    Dispatch.finish(r, ctx)

# ---- start up ----------------------------------------------------------

def Run.fatal(-A: Type, why: String) -> IO(A):
  do IO<A>:
    IO.print_err("pig_sdk: " ++ why)
    IO.exit(1)
    IO.die(A, 1, "unreachable")

# reads frames until the host's ready; any other frame before it is dropped
@unsafe
def Run.ready.got(m: Socket & RGot) -> IO(Socket):
  (s, r) = m
  match r:
    case RBytes{b}:
      Run.ready.is(Run.ready.type(J.Json.parse(b)), s)
    case REof{}:
      Run.fatal(Socket, "connection closed before ready")
    case RErr{w}:
      Run.fatal(Socket, "read failed before ready: " ++ w)

@unsafe
def Run.ready.is(isr: Bool, s: Socket) -> IO(Socket):
  match isr:
    case True{}:
      IO.pure(Socket, s)
    case False{}:
      Run.ready(s)

def Run.ready.type(r: J.PR) -> Bool:
  match r:
    case J.PErr{m}:
      False{}
    case J.POk{+v, rest}:
      String.eq(J.Json.field_str(v, "type", ""), "ready")

@unsafe
def Run.ready(s: Socket) -> IO(Socket):
  do IO<Socket>:
    m : Socket & RGot <- Wire.read_frame(s)
    Run.ready.got(m)

def Run.serve(~f: Unit -> Ext, rd: Socket, wr: Socket) -> IO(Unit):
  do IO<Unit>:
    +svc : Chan(Msg) <- Chan.new(Msg, 4096)
    +out : Chan(U.Bytes) <- Chan.new(U.Bytes, 4096)
    IO.spawn(Unit, Wire.reader(rd, svc))
    IO.spawn(Unit, Wire.writer(wr, out))
    Wire.svc(~(ctx => req => Dispatch.go(~f, ctx, req)), Svc.new(), Net{svc, out})

def Run.sent(~f: Unit -> Ext, sm: Socket & Result<&1, &1, (U32 & String) & List<&2, U32>, Unit>, rd: Socket) -> IO(Unit):
  (wr, r) = sm
  match r:
    case Fail{e}:
      Run.fatal(Unit, "could not send register")
    case Done{u}:
      do IO<Unit>:
        rd2 : Socket <- Run.ready(rd)
        Run.serve(~f, rd2, wr)

def Run.register(~f: Unit -> Ext, rd: Socket, wr: Socket) -> IO(Unit):
  do IO<Unit>:
    +reg : J.Json <- Reg.json(f(Unit{}))
    sm : Socket & Result<&1, &1, (U32 & String) & List<&2, U32>, Unit> <- TCP.send_bytes(wr, Wire.frame_of(jobj([kv("type", jstr("register")), kv("register", reg)])))
    Run.sent(~f, sm, rd)

def Run.conn(~f: Unit -> Ext, c: Result<&1, &1, U32 & String, Socket & Socket>) -> IO(Unit):
  match c:
    case Fail{e}:
      Run.fatal(Unit, "connect to host: " ++ Wire.err_text(e))
    case Done{p}:
      (rd, wr) = p
      Run.register(~f, rd, wr)

def Run.env(~f: Unit -> Ext, r: Result<&1, &1, U32 & String, String>) -> IO(Unit):
  match r:
    case Fail{e}:
      Run.fatal(Unit, "PIG_EXT_SOCKET not set: extension must be launched by pig")
    case Done{path}:
      do IO<Unit>:
        c : Result<&1, &1, U32 & String, Socket & Socket> <- X.Unix.connect(path)
        Run.conn(~f, c)

# Serves the extension that `f` builds until the host shuts it down.
def run(~f: Unit -> Ext) -> IO(Unit):
  do IO<Unit>:
    r : Result<&1, &1, U32 & String, String> <- IO.get_env("PIG_EXT_SOCKET")
    Run.env(~f, r)

# ---- host calls: UI ----------------------------------------------------

# a host call whose result is not used
def Host.unit(ctx: Ctx, method: String, args: J.Json) -> H(Unit):
  do H<Unit>:
    r : J.Json <- Wire.call(ctx, method, args)
    return Unit{}

# level: "info", "warning" or "error"
def notify(ctx: Ctx, message: String, level: String) -> H(Unit):
  Host.unit(ctx, "ui.notify", jobj([kv("message", jstr(message)), kv("level", jstr(level))]))

def set_status(ctx: Ctx, key: String, text: String) -> H(Unit):
  Host.unit(ctx, "ui.setStatus", jobj([kv("key", jstr(key)), kv("text", jstr(text))]))

# ---- host reply decoding -----------------------------------------------

def Host.missing(-A: Type, method: String, field: String) -> H(A):
  H.fail(A, "", "host reply to " ++ method ++ " has no \"" ++ field ++ "\"")

def Host.badtype(-A: Type, method: String, field: String, want: String) -> H(A):
  H.fail(A, "", "host reply to " ++ method ++ " field \"" ++ field ++ "\" is not " ++ want)

def Host.field.nn(j: J.Json, +method: String, +field: String) -> H(J.Json):
  match j:
    case J.JNull{}:
      Host.missing(J.Json, method, field)
    case _:
      H.pure(J.Json, j)

def Host.field.of(m: Maybe<&2, J.Json>, +method: String, +field: String) -> H(J.Json):
  match m:
    case None{}:
      Host.missing(J.Json, method, field)
    case Some{j}:
      Host.field.nn(j, method, field)

# a member every reply of the method carries; absent or null is an error
def Host.field(ctx: Ctx, +method: String, args: J.Json, +field: String) -> H(J.Json):
  do H<J.Json>:
    r : J.Json <- Wire.call(ctx, method, args)
    Host.field.of(J.Json.get(r, field), method, field)

# a member that may be absent or null: None then
def Host.optional.of(m: Maybe<&2, J.Json>) -> H(Maybe<&2, J.Json>):
  match m:
    case None{}:
      H.pure(Maybe<&2, J.Json>, None{})
    case Some{J.JNull{}}:
      H.pure(Maybe<&2, J.Json>, None{})
    case Some{j}:
      H.pure(Maybe<&2, J.Json>, Some{j})

def Host.optional(ctx: Ctx, method: String, args: J.Json, field: String) -> H(Maybe<&2, J.Json>):
  do H<Maybe<&2, J.Json>>:
    r : J.Json <- Wire.call(ctx, method, args)
    Host.optional.of(J.Json.get(r, field))

def Host.as_str(j: J.Json, +method: String, +field: String) -> H(String):
  match j:
    case J.JStr{s}:
      H.pure(String, s)
    case _:
      Host.badtype(String, method, field, "a string")

def Host.as_bool(j: J.Json, +method: String, +field: String) -> H(Bool):
  match j:
    case J.JBool{b}:
      H.pure(Bool, b)
    case _:
      Host.badtype(Bool, method, field, "a boolean")

def Host.str_field(ctx: Ctx, +method: String, args: J.Json, +field: String) -> H(String):
  do H<String>:
    j : J.Json <- Host.field(ctx, method, args, field)
    Host.as_str(j, method, field)

def Host.bool_field(ctx: Ctx, +method: String, args: J.Json, +field: String) -> H(Bool):
  do H<Bool>:
    j : J.Json <- Host.field(ctx, method, args, field)
    Host.as_bool(j, method, field)

# the wire carries an empty string for absent state (string | undefined getters)
def Host.opt_str.empty(+s: String) -> Maybe<&2, String>:
  Bool.pick(Maybe<&2, String>, String.is_empty(s), None{}, Some{s})

def Host.opt_str.r(r: Result<&1, &1, HErr, String>) -> H(Maybe<&2, String>):
  match r:
    case Fail{e}:
      IO.pure(Result<&1, &1, HErr, Maybe<&2, String>>, Fail{e})
    case Done{s}:
      H.pure(Maybe<&2, String>, Host.opt_str.empty(s))

def Host.opt_str.s(h: H(String)) -> H(Maybe<&2, String>):
  IO.bind(Result<&1, &1, HErr, String>, Result<&1, &1, HErr, Maybe<&2, String>>, h, r => Host.opt_str.r(r))

def Host.opt_str.of(m: Maybe<&2, J.Json>, +method: String, +field: String) -> H(Maybe<&2, String>):
  match m:
    case None{}:
      H.pure(Maybe<&2, String>, None{})
    case Some{j}:
      Host.opt_str.s(Host.as_str(j, method, field))

def Host.opt_str(ctx: Ctx, +method: String, +field: String) -> H(Maybe<&2, String>):
  do H<Maybe<&2, String>>:
    m : Maybe<&2, J.Json> <- Host.optional(ctx, method, J.JNull{}, field)
    Host.opt_str.of(m, method, field)

# ---- host calls: UI ----------------------------------------------------

def set_title(ctx: Ctx, title: String) -> H(Unit):
  Host.unit(ctx, "ui.setTitle", jobj([kv("title", jstr(title))]))

def set_working_message(ctx: Ctx, message: String) -> H(Unit):
  Host.unit(ctx, "ui.setWorkingMessage", jobj([kv("message", jstr(message))]))

def set_working_visible(ctx: Ctx, visible: Bool) -> H(Unit):
  Host.unit(ctx, "ui.setWorkingVisible", jobj([kv("visible", J.JBool{visible})]))

def set_hidden_thinking_label(ctx: Ctx, label: String) -> H(Unit):
  Host.unit(ctx, "ui.setHiddenThinkingLabel", jobj([kv("label", jstr(label))]))

# options: the upstream working-indicator options object
def set_working_indicator(ctx: Ctx, options: J.Json) -> H(Unit):
  Host.unit(ctx, "ui.setWorkingIndicator", options)

def get_editor_text(ctx: Ctx) -> H(String):
  Host.str_field(ctx, "ui.getEditorText", J.JNull{}, "text")

def set_editor_text(ctx: Ctx, text: String) -> H(Unit):
  Host.unit(ctx, "ui.setEditorText", jobj([kv("text", jstr(text))]))

def paste_to_editor(ctx: Ctx, text: String) -> H(Unit):
  Host.unit(ctx, "ui.pasteToEditor", jobj([kv("text", jstr(text))]))

def get_tools_expanded(ctx: Ctx) -> H(Bool):
  Host.bool_field(ctx, "ui.getToolsExpanded", J.JNull{}, "expanded")

def set_tools_expanded(ctx: Ctx, expanded: Bool) -> H(Unit):
  Host.unit(ctx, "ui.setToolsExpanded", jobj([kv("expanded", J.JBool{expanded})]))

def get_all_themes(ctx: Ctx) -> H(J.Json):
  Host.field(ctx, "ui.getAllThemes", J.JNull{}, "themes")

# the theme named `name`, or None when the host has none
def get_theme(ctx: Ctx, name: String) -> H(Maybe<&2, J.Json>):
  Host.optional(ctx, "ui.getTheme", jobj([kv("name", jstr(name))]), "theme")

# ---- dialogs: each reports blocked:user while it waits ----

def Dlg.pair(s: String, ok: Bool) -> String & Bool:
  (s, ok)

def Dlg.flag(m: Maybe<&2, J.Json>) -> Bool:
  match m:
    case Some{J.JBool{b}}:
      b
    case _:
      False{}

def Dlg.sel(+r: J.Json) -> H(String & Bool):
  do H<String & Bool>:
    s : String <- H.pure(String, J.Json.field_str(r, "selected", ""))
    b : Bool <- H.pure(Bool, Dlg.flag(J.Json.get(r, "ok")))
    return Dlg.pair(s, b)

def Dlg.txt(+r: J.Json) -> H(String & Bool):
  do H<String & Bool>:
    s : String <- H.pure(String, J.Json.field_str(r, "text", ""))
    b : Bool <- H.pure(Bool, Dlg.flag(J.Json.get(r, "ok")))
    return Dlg.pair(s, b)

# (selected, ok); ("", False) when the user cancelled
def select(ctx: Ctx, title: String, options: List<&2, String>) -> H(String & Bool):
  do H<String & Bool>:
    r : J.Json <- Wire.call_as(ctx, "user", "ui.select", jobj([kv("title", jstr(title)), kv("options", J.Json.of_strs(options))]))
    Dlg.sel(r)

def Dlg.confirmed(m: Maybe<&2, J.Json>) -> Bool:
  Dlg.flag(m)

def confirm(ctx: Ctx, title: String, message: String) -> H(Bool):
  do H<Bool>:
    r : J.Json <- Wire.call_as(ctx, "user", "ui.confirm", jobj([kv("title", jstr(title)), kv("message", jstr(message))]))
    H.pure(Bool, Dlg.confirmed(J.Json.get(r, "confirmed")))

# (text, ok)
def input(ctx: Ctx, title: String, placeholder: String) -> H(String & Bool):
  do H<String & Bool>:
    r : J.Json <- Wire.call_as(ctx, "user", "ui.input", jobj([kv("title", jstr(title)), kv("placeholder", jstr(placeholder))]))
    Dlg.txt(r)

def editor(ctx: Ctx, title: String, prefill: String) -> H(String & Bool):
  do H<String & Bool>:
    r : J.Json <- Wire.call_as(ctx, "user", "ui.editor", jobj([kv("title", jstr(title)), kv("prefill", jstr(prefill))]))
    Dlg.txt(r)

# ---- host calls: session and agent -------------------------------------

def Msg.opts.t(t: Maybe<&2, Bool>, +rest: List<&2, J.JKV>) -> List<&2, J.JKV>:
  match t:
    case None{}:
      rest
    case Some{b}:
      Con{kv("triggerTurn", J.JBool{b}), rest}

def Msg.opts(trigger: Maybe<&2, Bool>, +deliver: String) -> J.Json:
  jobj(Msg.opts.t(trigger, Reg.opt("deliverAs", deliver, Nil{})))

# a custom message. deliver_as is "" for the host default.
def send_message(ctx: Ctx, custom_type: String, content: String, display: Bool, trigger_turn: Maybe<&2, Bool>, deliver_as: String) -> H(Unit):
  Host.unit(ctx, "sendMessage", jobj([kv("message", jobj([kv("customType", jstr(custom_type)), kv("content", jstr(content)), kv("display", J.JBool{display})])), kv("options", Msg.opts(trigger_turn, deliver_as))]))

# content: a JSON string or an array of text and image blocks
def send_user_message(ctx: Ctx, content: J.Json, deliver_as: String) -> H(Unit):
  Host.unit(ctx, "sendUserMessage", jobj([kv("content", content), kv("options", jobj([kv("deliverAs", jstr(deliver_as))]))]))

def append_entry(ctx: Ctx, custom_type: String, data: J.Json) -> H(Unit):
  Host.unit(ctx, "appendEntry", jobj([kv("customType", jstr(custom_type)), kv("data", data)]))

def set_session_name(ctx: Ctx, name: String) -> H(Unit):
  Host.unit(ctx, "setSessionName", jobj([kv("name", jstr(name))]))

# None when the session has no name
def get_session_name(ctx: Ctx) -> H(Maybe<&2, String>):
  Host.opt_str(ctx, "getSessionName", "name")

def set_label(ctx: Ctx, entry_id: String, label: String) -> H(Unit):
  Host.unit(ctx, "setLabel", jobj([kv("entryId", jstr(entry_id)), kv("label", jstr(label))]))

def Flag.default(fl: J.Json, +name: String) -> Maybe<&2, J.Json>:
  J.Json.get(fl, name)

def get_flag.of(m: Maybe<&2, J.Json>, fl: J.Json, +name: String) -> Maybe<&2, J.Json>:
  match m:
    case Some{v}:
      Some{v}
    case None{}:
      Flag.default(fl, name)

# The host's value, else the registered default (false and "" are values).
# None is Pi's undefined: no override and no registered default.
def get_flag(ctx: Ctx, +name: String) -> H(Maybe<&2, J.Json>):
  match ctx:
    case Ctx{svc, out, req, cid, +fl}:
      do H<Maybe<&2, J.Json>>:
        m : Maybe<&2, J.Json> <- Host.optional(Ctx{svc, out, req, cid, fl}, "getFlag", jobj([kv("name", jstr(name))]), "value")
        H.pure(Maybe<&2, J.Json>, get_flag.of(m, fl, name))

def get_thinking_level(ctx: Ctx) -> H(String):
  Host.str_field(ctx, "getThinkingLevel", J.JNull{}, "level")

def set_thinking_level(ctx: Ctx, level: String) -> H(Unit):
  Host.unit(ctx, "setThinkingLevel", jobj([kv("level", jstr(level))]))

# the host's {success, error?} reply
def set_model(ctx: Ctx, model: String) -> H(J.Json):
  Wire.call(ctx, "setModel", jobj([kv("model", jstr(model))]))

def get_active_tools(ctx: Ctx) -> H(J.Json):
  Host.field(ctx, "getActiveTools", J.JNull{}, "tools")

def set_active_tools(ctx: Ctx, tools: List<&2, String>) -> H(Unit):
  Host.unit(ctx, "setActiveTools", jobj([kv("tools", J.Json.of_strs(tools))]))

def get_all_tools(ctx: Ctx) -> H(J.Json):
  Host.field(ctx, "getAllTools", J.JNull{}, "tools")

def get_commands(ctx: Ctx) -> H(J.Json):
  Host.field(ctx, "getCommands", J.JNull{}, "commands")

def refresh_tools(ctx: Ctx) -> H(Unit):
  Host.unit(ctx, "refreshTools", J.JNull{})

# {tokens, contextWindow, percent} or null
def get_context_usage(ctx: Ctx) -> H(J.Json):
  Wire.call(ctx, "getContextUsage", J.JNull{})

def get_system_prompt(ctx: Ctx) -> H(String):
  Host.str_field(ctx, "getSystemPrompt", J.JNull{}, "prompt")

def get_system_prompt_options(ctx: Ctx) -> H(J.Json):
  Wire.call(ctx, "getSystemPromptOptions", J.JNull{})

def get_session_id(ctx: Ctx) -> H(String):
  Host.str_field(ctx, "getSessionID", J.JNull{}, "sessionId")

def get_session_file(ctx: Ctx) -> H(Maybe<&2, String>):
  Host.opt_str(ctx, "getSessionFile", "sessionFile")

def get_leaf_id(ctx: Ctx) -> H(Maybe<&2, String>):
  Host.opt_str(ctx, "getLeafID", "leafId")

def is_idle(ctx: Ctx) -> H(Bool):
  Host.bool_field(ctx, "isIdle", J.JNull{}, "idle")

def is_project_trusted(ctx: Ctx) -> H(Bool):
  Host.bool_field(ctx, "isProjectTrusted", J.JNull{}, "trusted")

def has_pending_messages(ctx: Ctx) -> H(Bool):
  Host.bool_field(ctx, "hasPendingMessages", J.JNull{}, "pending")

def abort(ctx: Ctx) -> H(Unit):
  Host.unit(ctx, "abort", J.JNull{})

def shutdown(ctx: Ctx) -> H(Unit):
  Host.unit(ctx, "shutdown", J.JNull{})

def reload(ctx: Ctx) -> H(Unit):
  Host.unit(ctx, "reload", J.JNull{})

def wait_for_idle(ctx: Ctx) -> H(Unit):
  Host.unit(ctx, "waitForIdle", J.JNull{})

# opts: the upstream compact options object, or JNull
def compact(ctx: Ctx, opts: J.Json) -> H(Unit):
  Host.unit(ctx, "compact", opts)

# runs a command through the host's bash executor; the reply is the host's exec result object
def exec(ctx: Ctx, command: String, args: List<&2, String>) -> H(J.Json):
  Wire.call(ctx, "exec", jobj([kv("command", jstr(command)), kv("args", J.Json.of_strs(args))]))

# ---- request facts -----------------------------------------------------

# the tool call id of a tool_call request, "" otherwise
def tool_call_id(ctx: Ctx) -> String:
  Ctx.call_id(ctx)

def Cancel.of(m: Maybe<&1, Bool>) -> Bool:
  match m:
    case Some{b}:
      b
    case None{}:
      False{}

# true once the host cancelled this request. Pure code is not interrupted:
# poll between effects.
def cancelled(ctx: Ctx) -> IO(Bool):
  match ctx:
    case Ctx{svc, out, req, cid, fl}:
      do IO<Bool>:
        +reply : Chan(Bool) <- Chan.new(Bool, 1)
        Wire.put(Msg, svc, MAsk{req, reply})
        m : Maybe<&1, Bool> <- Chan.recv(Bool, reply)
        IO.pure(Bool, Cancel.of(m))
