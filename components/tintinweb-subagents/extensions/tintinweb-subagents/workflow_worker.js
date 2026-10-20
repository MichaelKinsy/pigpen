/*
 * workflow_worker.js — the JavaScript that runs a workflow script's side of a run.
 *
 * upstream: src/workflow/worker-source.ts (WORKER_SOURCE). The original runs this in a Node worker thread with a `vm`
 * context; this port runs it in an in-process goja realm (workflow_engine.go), so there is no `require`, no
 * `workerData` and no `postMessage`: the Go side passes a `host` object and the run parameters. Everything the script
 * can reach is the same: agent(), parallel(), pipeline(), phase(), log(), workflow(), budget, console, meta, args.
 *
 * The function this file evaluates to is called as `bootstrap(host, params)` and returns { main, response }.
 *   host.call(callId, method, payload)   post a call to the host (an agent run, a workflow lookup)
 *   host.progress(entries)               post a batch of progress entries
 *   host.complete(resultJson)            the script finished
 *   host.error(message, stack)           the script failed
 *   host.defer(fn) / host.cancel(id)     a macrotask, the port of setTimeout(fn, 0) / clearTimeout
 *   host.run(source, name)               compile a script source and run it, returning its value
 *   host.compile(source, name)           compile a source that evaluates to a function and return it
 */
(function (host, params) {
  "use strict";

  const ITEM_CAP = params.itemCap;
  const NESTED_CAP = params.nestedCap;
  const PRELUDE = params.prelude;

  // Captured before the script can touch them.
  const ObjectPrototype = Object.prototype;
  const realmParse = JSON.parse;
  const realmStringify = JSON.stringify;
  const isArray = Array.isArray;
  // The script shares this realm (the original gives it a vm context of its own), so what the runtime calls on arrays,
  // promises and objects is captured here, before the script can patch Array.prototype or Promise: a patched push or
  // catch must break the script, never the runtime.
  const uncurry = Function.prototype.bind.bind(Function.prototype.call);
  const hasOwn = uncurry(Object.prototype.hasOwnProperty);
  const arrPush = uncurry(Array.prototype.push);
  const arrJoin = uncurry(Array.prototype.join);
  const arrIndexOf = uncurry(Array.prototype.indexOf);
  const arrMap = uncurry(Array.prototype.map);
  const promiseCatch = uncurry(Promise.prototype.catch);
  const NewPromise = Promise;
  const getOwnPropertySymbols = Object.getOwnPropertySymbols;
  const getPrototypeOf = Object.getPrototypeOf;
  const objectKeys = Object.keys;
  const isFinite_ = Number.isFinite;
  const PromiseAll = Promise.all.bind(Promise);
  const NewMap = Map;
  const NewSet = Set;
  const NewError = Error;

  /* ------------------------------------------------------------------ *
   * RPC to the host
   * ------------------------------------------------------------------ */

  let nextCallId = 1;
  const pendingCalls = new NewMap();

  /**
   * Output tokens this run has spent, as last reported by the host: a mirror, not a tally. Every response carries the
   * host's current total, so there is exactly one counter and it cannot drift.
   */
  let spentOutput = 0;

  function callHost(method, payload) {
    // Drain first, so the phase() that named this agent reaches the host ahead of the agent entry.
    flushProgress();
    return new NewPromise(function (resolve, reject) {
      const callId = nextCallId++;
      pendingCalls.set(callId, { resolve: resolve, reject: reject });
      host.call(callId, method, payload);
    });
  }

  function onResponse(message) {
    if (typeof message.spent === "number") spentOutput = message.spent;
    const waiter = pendingCalls.get(message.callId);
    if (!waiter) return;
    pendingCalls.delete(message.callId);
    if (message.ok) {
      waiter.resolve(message.value);
      return;
    }
    const error = new NewError(message.error || "The workflow host rejected the call.");
    // Fatal errors are the run's, not the item's: parallel() and pipeline() swallow ordinary failures into null, and
    // a cap breach must not be silently absorbed that way.
    if (message.fatal) error.workflowFatal = true;
    waiter.reject(error);
  }

  function isFatal(error) {
    return !!(error && typeof error === "object" && error.workflowFatal === true);
  }

  /* ------------------------------------------------------------------ *
   * Progress entries
   * ------------------------------------------------------------------ */

  let progressQueue = [];
  let flushTimer = null;

  function emit(entry) {
    arrPush(progressQueue, entry);
    // Batched on a macrotask: a fan-out emits a burst of phase/log entries in one turn, and the host renders once per
    // batch rather than once per entry.
    if (flushTimer === null) flushTimer = host.defer(flushProgress);
  }

  function flushProgress() {
    if (flushTimer !== null) {
      host.cancel(flushTimer);
      flushTimer = null;
    }
    if (progressQueue.length === 0) return;
    const batch = progressQueue;
    progressQueue = [];
    host.progress(batch);
  }

  /* ------------------------------------------------------------------ *
   * The JSON boundary
   * ------------------------------------------------------------------ */

  let nestedCount = 0;

  function boundaryError(what, path) {
    return new NewError("Cannot pass " + what + " across the workflow VM boundary (at " + path + ").");
  }

  function assertBoundary(value, path, seen) {
    if (value === null) return;
    const kind = typeof value;
    if (kind === "string" || kind === "boolean") return;
    if (kind === "number") {
      if (!isFinite_(value)) throw boundaryError("a non-finite number", path);
      return;
    }
    if (kind === "undefined") {
      if (path === "the workflow result") return;
      throw boundaryError("undefined", path);
    }
    if (kind === "bigint") throw boundaryError("a BigInt", path);
    if (kind === "symbol") throw boundaryError("a symbol", path);
    if (kind === "function") throw boundaryError("a function", path);
    if (kind !== "object") throw boundaryError("a " + kind, path);

    if (seen.has(value)) throw boundaryError("a circular structure", path);
    seen.add(value);

    if (getOwnPropertySymbols(value).length > 0) {
      throw boundaryError("an object with symbol keys", path);
    }

    if (isArray(value)) {
      const length = value.length;
      for (let i = 0; i < length; i++) {
        // A sparse array round-trips through JSON as nulls, which silently changes the data. Reject instead.
        if (!hasOwn(value, i)) {
          throw boundaryError("a sparse array", path + "[" + i + "]");
        }
        assertBoundary(value[i], path + "[" + i + "]", seen);
      }
      seen.delete(value);
      return;
    }

    const prototype = getPrototypeOf(value);
    // Everything but a plain object (Map, Set, Date, a class instance) loses meaning here.
    if (prototype !== null && prototype !== ObjectPrototype) {
      throw boundaryError("a non-plain object", path);
    }

    const keys = objectKeys(value);
    for (let i = 0; i < keys.length; i++) {
      assertBoundary(value[keys[i]], path + "." + keys[i], seen);
    }
    seen.delete(value);
  }

  function checkBoundary(value, path) {
    assertBoundary(value, path, new NewSet());
    return value;
  }

  /* ------------------------------------------------------------------ *
   * Helpers
   * ------------------------------------------------------------------ */

  function toList(value, what) {
    if (!isArray(value)) throw new NewError(what + " expects an array.");
    const length = value.length >>> 0;
    if (length > ITEM_CAP) {
      throw new NewError(what + " was given " + length + " items, over the limit of " + ITEM_CAP + ".");
    }
    const out = [];
    for (let i = 0; i < length; i++) arrPush(out, value[i]);
    return out;
  }

  function requireText(value, what) {
    if (typeof value !== "string" || value.trim() === "") {
      throw new NewError(what + " requires a non-empty string.");
    }
    return value;
  }

  function optionalText(value, what) {
    if (value === undefined || value === null) return undefined;
    return requireText(value, what);
  }

  /** Reasoning effort a child may be spawned under: pi's ThinkingLevel, plus Claude Code's `max`. */
  const EFFORT_LEVELS = ["minimal", "low", "medium", "high", "xhigh", "max"];

  /**
   * Every option agent() understands. Checked rather than ignored: quietly dropping `schema` hands the script the
   * agent's raw text where it expected a validated object, and a typo behaves the same way.
   */
  const AGENT_OPTIONS = ["label", "phase", "model", "agentType", "isolation", "gate", "resume", "effort", "schema"];

  /** Claude Code options this runtime does not have, and why. */
  const UNSUPPORTED_AGENT_OPTIONS = {};

  /* ------------------------------------------------------------------ *
   * Script globals
   * ------------------------------------------------------------------ */

  /**
   * Phase indices are allocated once for the whole run, so parent and child never collide. What is per-scope is the
   * title to index map: a child's phase("Scan") must not resolve to the parent's "Scan".
   */
  let nextPhaseIndex = 0;

  function makeScope(name, depth) {
    const scope = {
      name: name,
      depth: depth,
      // Prefixed into every phase title the child defines: how a nested run reads as its own group in the progress tree.
      prefix: name === undefined ? "" : "\u25b8 " + name,
      ambientPhaseIndex: undefined,
      ambientPhaseTitle: undefined,
      phaseIndexByTitle: new NewMap(),
    };
    scope.agent = function (prompt, opts) {
      return agentIn(scope, prompt, opts);
    };
    scope.phase = function (title) {
      return phaseIn(scope, title);
    };
    scope.log = function (message) {
      return logIn(scope, message);
    };
    scope.workflow = function (ref, args) {
      return workflowIn(scope, ref, args);
    };
    scope.console = makeConsole(scope);
    return scope;
  }

  /** A scope's title for a phase: the child's own group, or the parent's bare title. */
  function scopedTitle(scope, title) {
    if (scope.prefix === "") return title;
    return title === undefined ? scope.prefix : scope.prefix + " \u203a " + title;
  }

  function definePhaseIn(scope, title) {
    let index = scope.phaseIndexByTitle.get(title);
    if (index !== undefined) return index;
    index = nextPhaseIndex++;
    scope.phaseIndexByTitle.set(title, index);
    emit({ type: "workflow_phase", index: index, title: scopedTitle(scope, title) });
    return index;
  }

  function phaseIn(scope, title) {
    const text = requireText(title, "phase(title)");
    scope.ambientPhaseIndex = definePhaseIn(scope, text);
    scope.ambientPhaseTitle = scopedTitle(scope, text);
  }

  function describe(value) {
    if (typeof value === "string") return value;
    // Duck-typed, not `instanceof Error`: an error thrown by the script need not be this realm's Error.
    if (value && typeof value === "object" && typeof value.message === "string" && typeof value.stack === "string") {
      return value.message;
    }
    try {
      const json = realmStringify(value);
      if (json !== undefined) return json;
    } catch (e) {
      /* cycles and BigInt fall through to String() */
    }
    return String(value);
  }

  /** Attribute a line to the child that wrote it; logs carry no phase of their own. */
  function logPrefix(scope) {
    return scope.prefix === "" ? "" : scope.prefix + ": ";
  }

  function logIn(scope, message) {
    emit({ type: "workflow_log", message: logPrefix(scope) + describe(message) });
  }

  function makeConsole(scope) {
    const write = function () {
      const parts = [];
      for (let i = 0; i < arguments.length; i++) arrPush(parts, describe(arguments[i]));
      emit({ type: "workflow_log", message: logPrefix(scope) + arrJoin(parts, " ") });
    };
    return { log: write, info: write, warn: write, error: write, debug: write };
  }

  async function agentIn(scope, prompt, opts) {
    const text = requireText(prompt, "agent(prompt)");
    const options = opts === undefined || opts === null ? {} : opts;
    if (typeof options !== "object" || isArray(options)) {
      throw new NewError("agent(prompt, opts) expects opts to be an object.");
    }

    const optionKeys = objectKeys(options);
    for (let k = 0; k < optionKeys.length; k++) {
      const key = optionKeys[k];
      if (arrIndexOf(AGENT_OPTIONS, key) !== -1) continue;
      const why = UNSUPPORTED_AGENT_OPTIONS[key];
      throw new NewError(
        why !== undefined
          ? "agent() opts." + key + " is not supported here: " + why
          : "agent() opts." + key + " is not a recognised option. Supported: " + arrJoin(AGENT_OPTIONS, ", ") + "."
      );
    }

    const label = optionalText(options.label, "agent() opts.label");
    const phaseName = optionalText(options.phase, "agent() opts.phase");
    const model = optionalText(options.model, "agent() opts.model");
    const agentType = optionalText(options.agentType, "agent() opts.agentType");
    const isolation = optionalText(options.isolation, "agent() opts.isolation");
    if (isolation !== undefined && isolation !== "worktree") {
      throw new NewError('agent() opts.isolation must be "worktree".');
    }
    const gate = optionalText(options.gate, "agent() opts.gate");
    const resume = optionalText(options.resume, "agent() opts.resume");
    const effort = optionalText(options.effort, "agent() opts.effort");
    const schema = options.schema;
    if (schema !== undefined) {
      if (typeof schema !== "object" || schema === null || isArray(schema)) {
        throw new NewError("agent() opts.schema must be a JSON Schema object.");
      }
      // A Map or a cycle would survive neither the journal key nor the tool's parameters: same check the return value gets.
      checkBoundary(schema, "agent() opts.schema");
    }
    if (effort !== undefined && arrIndexOf(EFFORT_LEVELS, effort) === -1) {
      throw new NewError("agent() opts.effort must be one of: " + arrJoin(EFFORT_LEVELS, ", ") + ".");
    }

    // resume revives a child that already exists, so anything describing how to *start* one is not a thing this call
    // gets to decide. Rejecting is the point: silently ignoring these opts would look like they applied.
    if (resume !== undefined) {
      if (agentType !== undefined) {
        throw new NewError(
          "agent() opts.resume and opts.agentType are mutually exclusive: a resumed agent keeps the agent type it was started with."
        );
      }
      if (model !== undefined) {
        throw new NewError(
          "agent() opts.resume and opts.model are mutually exclusive: a resumed agent keeps the model it was started with."
        );
      }
      if (isolation !== undefined) {
        throw new NewError(
          "agent() opts.resume and opts.isolation are mutually exclusive: a resumed agent keeps the working tree it was started in."
        );
      }
      if (effort !== undefined) {
        throw new NewError(
          "agent() opts.resume and opts.effort are mutually exclusive: a resumed agent keeps the reasoning effort it was started with."
        );
      }
      if (schema !== undefined) {
        throw new NewError(
          "agent() opts.resume and opts.schema are mutually exclusive: a resumed child re-prompts the session it "
            + "already had, whose tool set was fixed when it started — it has no StructuredOutput tool to answer through."
        );
      }
      if (gate !== undefined) {
        throw new NewError("agent() opts.gate cannot be combined with opts.resume.");
      }
    }

    // An explicit opts.phase files this agent under that phase without moving the ambient one.
    const phaseIndex = phaseName !== undefined ? definePhaseIn(scope, phaseName) : scope.ambientPhaseIndex;
    const phaseTitle = phaseName !== undefined ? scopedTitle(scope, phaseName) : scope.ambientPhaseTitle;

    const result = await callHost("agent", {
      prompt: text,
      label: label,
      model: model,
      agentType: agentType,
      isolation: isolation,
      phaseIndex: phaseIndex,
      phaseTitle: phaseTitle,
      gate: gate,
      resume: resume,
      effort: effort,
      // Serialized here, inside the realm, so the key order the script wrote is what the journal key hashes.
      schemaJson: schema === undefined ? undefined : realmStringify(schema),
    });
    if (result === undefined || result === null) return null;
    if (schema === undefined) return result;
    try {
      return realmParse(result);
    } catch (error) {
      logIn(scope, "agent(): the host returned a structured result that is not JSON");
      return null;
    }
  }

  /**
   * A barrier: every thunk starts now, and nothing past the await runs until all of them have settled. A thunk that
   * throws resolves to null rather than failing its siblings.
   */
  async function parallel(thunks) {
    const list = toList(thunks, "parallel(thunks)");
    for (let i = 0; i < list.length; i++) {
      if (typeof list[i] !== "function") {
        throw new NewError("parallel(thunks) expects an array of functions; item " + i + " is not one.");
      }
    }
    return await PromiseAll(
      arrMap(list, async function (thunk) {
        try {
          return await thunk();
        } catch (error) {
          if (isFatal(error)) throw error;
          return null;
        }
      })
    );
  }

  /**
   * No barrier between stages. Each item walks its own chain; a stage that throws drops that item to null and skips
   * its remaining stages. Every stage sees (previousResult, originalItem, index).
   */
  async function pipeline(items, ...stages) {
    const list = toList(items, "pipeline(items, ...stages)");
    for (let i = 0; i < stages.length; i++) {
      if (typeof stages[i] !== "function") {
        throw new NewError("pipeline(items, ...stages) expects stages to be functions; stage " + i + " is not one.");
      }
    }
    return await PromiseAll(
      arrMap(list, async function (item, index) {
        let value = item;
        for (let s = 0; s < stages.length; s++) {
          try {
            value = await stages[s](value, item, index);
          } catch (error) {
            if (isFatal(error)) throw error;
            return null;
          }
        }
        return value;
      })
    );
  }

  /**
   * The workflow(nameOrRef, args?) global: runs another workflow inline, in this same realm, as a function whose
   * parameters shadow the globals. One level only, as in Claude Code.
   */
  async function workflowIn(scope, nameOrRef, args) {
    if (scope.depth > 0) {
      throw new NewError(
        "workflow() cannot be nested more than one level deep — you are already inside the workflow '" +
          scope.name + "'. Call the agents inline instead."
      );
    }

    let ref;
    if (typeof nameOrRef === "string") {
      if (nameOrRef.trim() === "") throw new NewError("workflow(nameOrRef) expects a non-empty name.");
      ref = { name: nameOrRef };
    } else if (nameOrRef && typeof nameOrRef === "object" && !isArray(nameOrRef)) {
      const scriptPath = optionalText(nameOrRef.scriptPath, "workflow() scriptPath");
      const name = optionalText(nameOrRef.name, "workflow() name");
      if (scriptPath === undefined && name === undefined) {
        throw new NewError("workflow({ ... }) expects a `name` or a `scriptPath`.");
      }
      ref = { name: name, scriptPath: scriptPath };
    } else {
      throw new NewError("workflow(nameOrRef) expects a saved workflow name or { scriptPath }.");
    }

    const label = ref.name !== undefined ? ref.name : ref.scriptPath;
    if (args !== undefined) checkBoundary(args, 'workflow("' + label + '") args');

    if (nestedCount >= NESTED_CAP) {
      // Fatal, like the agent cap: a limit that silently drops work would be worse than no limit.
      const error = new NewError("Workflow exceeded its cap of " + NESTED_CAP + " nested workflow() calls.");
      error.workflowFatal = true;
      throw error;
    }
    nestedCount++;

    let loaded;
    try {
      loaded = await callHost("workflow", ref);
    } catch (error) {
      // Resolution failures are the script's to handle: workflow() throws on an unknown name so a script can catch it.
      if (isFatal(error)) throw error;
      throw new NewError('workflow("' + label + '"): ' + describe(error));
    }

    const child = makeScope(loaded.name, scope.depth + 1);
    // The child's own group, defined before its first agent so a child that never calls phase() still reads as its own section.
    child.ambientPhaseIndex = definePhaseIn(child, undefined);
    child.ambientPhaseTitle = scopedTitle(child, undefined);

    let run;
    try {
      // `meta` is deliberately not a parameter: the body still opens with its own `const meta = { ... }`.
      run = host.compile(
        "(async (agent, phase, log, workflow, console, args) => {" + PRELUDE + loaded.body + "\n})",
        "workflow:" + loaded.name + ".js"
      );
    } catch (error) {
      throw new NewError('workflow("' + label + '"): ' + describe(error));
    }

    const value = await run(child.agent, child.phase, child.log, child.workflow, child.console, args);
    checkBoundary(value, 'the result of workflow("' + label + '")');
    return value;
  }

  /**
   * The budget global. `total` is permanently null, and that is the honest answer rather than a stub: pi has no
   * "+500k" directive. `spent()` counts this run's agents' output tokens.
   */
  function makeBudget() {
    return {
      total: null,
      spent: function () {
        return spentOutput;
      },
      remaining: function () {
        // Infinity, not a number, because there is no target to subtract from.
        return Infinity;
      },
    };
  }

  /* ------------------------------------------------------------------ *
   * Code generation from strings is off, as in the original's vm context
   * (`codeGeneration: { strings: false }`): a determinism boundary, not a security one.
   * ------------------------------------------------------------------ */

  function banCodeGeneration() {
    const EvalErr = EvalError;
    const banned = function () {
      throw new EvalErr("Code generation from strings disallowed for this context");
    };
    const FunctionPrototype = Function.prototype;
    banned.prototype = FunctionPrototype;
    const define = function (target, name, value) {
      Object.defineProperty(target, name, { value: value, writable: true, configurable: true, enumerable: false });
    };
    define(globalThis, "eval", banned);
    define(globalThis, "Function", banned);
    define(FunctionPrototype, "constructor", banned);
    const kinds = [async function () {}, function* () {}];
    for (let i = 0; i < kinds.length; i++) {
      define(getPrototypeOf(kinds[i]), "constructor", banned);
    }
  }

  /* ------------------------------------------------------------------ *
   * Run
   * ------------------------------------------------------------------ */

  async function main() {
    const rootScope = makeScope(undefined, 0);
    globalThis.agent = rootScope.agent;
    globalThis.parallel = parallel;
    globalThis.pipeline = pipeline;
    globalThis.phase = rootScope.phase;
    globalThis.log = rootScope.log;
    globalThis.workflow = rootScope.workflow;
    globalThis.budget = makeBudget();
    globalThis.console = rootScope.console;
    // meta and args are materialised as objects of this realm, so the script sees its own Object.prototype.
    globalThis.meta = realmParse(params.metaJson);
    globalThis.args = params.argsJson === undefined ? undefined : realmParse(params.argsJson);

    banCodeGeneration();

    const value = await host.run("(async () => {" + PRELUDE + params.body + "\n})()", "workflow.js");
    checkBoundary(value, "the workflow result");
    flushProgress();
    host.complete(value === undefined ? undefined : realmStringify(value));
  }

  return {
    main: function () {
      promiseCatch(main(), function (error) {
        flushProgress();
        host.error(
          error && error.message ? String(error.message) : String(error),
          error && error.stack ? String(error.stack) : undefined
        );
      });
    },
    response: onResponse,
  };
})
