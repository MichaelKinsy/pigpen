#!/usr/bin/env bend
import Base
import ../pig_sdk/pig.bend as Pig
import ../pig_sdk/json.bend as J

# Registers an event the Bend SDK does not implement. The SDK refuses at load
# with one stderr line and exit 1, before it registers anything.

def seen(data: J.Json, ctx: Pig.Ctx) -> Pig.H(Maybe<&2, J.Json>):
  Pig.H.pure(Maybe<&2, J.Json>, None{})

def new_extension() -> Pig.Ext:
  Pig.Ext.ons(Pig.Ext.new("bend-gap", "0.1.0"), [Pig.On.new("before_agent_start", seen)])

def main() -> IO(Unit):
  Pig.run(~(u => new_extension()))
