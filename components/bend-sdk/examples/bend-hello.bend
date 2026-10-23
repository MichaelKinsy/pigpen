#!/usr/bin/env bend
import Base
import ../pig_sdk/pig.bend as Pig
import ../pig_sdk/json.bend as J

# A tool named echo_bridge: PiG's test-faux provider calls it for the prompt
# "Run: extension echo hello". It makes two host calls (ui.notify, then getSessionID) and answers with the id the host returned.

def schema() -> String:
  "{\"type\":\"object\",\"required\":[\"text\"],\"properties\":{\"text\":{\"type\":\"string\"}}}"

# A handler is a plain function: a `+` parameter would make its type a different one (Bend WONTFIX #1111),
# so the reused text is taken by a helper.
def echo.go(+text: String, +ctx: Pig.Ctx) -> Pig.H(J.Json):
  do Pig.H<J.Json>:
    Pig.notify(ctx, "bend saw: " ++ text, "info")
    sid : String <- Pig.get_session_id(ctx)
    return Pig.Result.text("bend-echo: " ++ text ++ " session=" ++ sid)

def echo(args: J.Json, ctx: Pig.Ctx) -> Pig.H(J.Json):
  echo.go(J.Json.field_str(args, "text", ""), ctx)

def new_extension() -> Pig.Ext:
  Pig.Ext.tools(Pig.Ext.new("bend-hello", "0.1.0"), [Pig.Tool.new("echo_bridge", "Echo the text from a Bend extension", schema(), echo)])

def main() -> IO(Unit):
  Pig.run(~(u => new_extension()))
