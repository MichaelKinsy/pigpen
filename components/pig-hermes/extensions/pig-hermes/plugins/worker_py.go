package plugins

// workerScript is a self-contained Python 3 shim that loads a Hermes plugin's
// __init__.py (or custom entry), invokes register(ctx) with a PluginContext
// compatible with hermes_cli/plugins_context.py, and serves bidirectional
// NDJSON JSON-RPC over stdin/stdout (matching hermes_cli/plugin_host_wire.py).
const workerScript = `import asyncio
import importlib.util
import inspect
import json
import os
import re
import sys
import types
from pathlib import Path

# Duplicate stdin/stdout FDs for NDJSON framing and redirect fd 0 -> /dev/null
# and fd 1 -> stderr so stray print() calls in plugins never corrupt the wire.
_in_fd = os.dup(0)
_out_fd = os.dup(1)
try:
    _devnull = os.open(os.devnull, os.O_RDONLY)
    os.dup2(_devnull, 0)
    os.close(_devnull)
except OSError:
    pass
try:
    os.dup2(2, 1)
except OSError:
    pass

_wire_in = os.fdopen(_in_fd, "r", encoding="utf-8", buffering=1)
_wire_out = os.fdopen(_out_fd, "w", encoding="utf-8", buffering=1)


def _send(obj):
    _wire_out.write(json.dumps(obj, default=str) + "\n")
    _wire_out.flush()


def _run_callable(fn, *args, **kwargs):
    res = fn(*args, **kwargs)
    if inspect.isawaitable(res):
        try:
            loop = asyncio.get_event_loop()
            if loop.is_closed():
                loop = asyncio.new_event_loop()
                asyncio.set_event_loop(loop)
        except RuntimeError:
            loop = asyncio.new_event_loop()
            asyncio.set_event_loop(loop)
        return loop.run_until_complete(res)
    return res


def _call_with_narrowed_kwargs(fn, payload):
    if not isinstance(payload, dict):
        return _run_callable(fn)
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return _run_callable(fn, **payload)
    params = sig.parameters
    has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params.values())
    if has_var_kw:
        return _run_callable(fn, **payload)
    filtered = {}
    for name, p in params.items():
        if p.kind in (inspect.Parameter.POSITIONAL_OR_KEYWORD, inspect.Parameter.KEYWORD_ONLY):
            if name in payload:
                filtered[name] = payload[name]
    return _run_callable(fn, **filtered)


class _PluginState:
    def __init__(self, data_dir):
        self.data_dir = Path(data_dir)
        self._state_file = self.data_dir / "state.json"
        self._data = {}
        self._load()

    def _load(self):
        try:
            if self._state_file.is_file():
                raw = json.loads(self._state_file.read_text(encoding="utf-8"))
                if isinstance(raw, dict):
                    self._data = raw
        except Exception:
            self._data = {}

    def _save(self):
        self.data_dir.mkdir(parents=True, exist_ok=True)
        tmp = self._state_file.with_suffix(".json.tmp")
        tmp.write_text(json.dumps(self._data, indent=2, default=str), encoding="utf-8")
        os.replace(tmp, self._state_file)

    def get(self, key, default=None):
        return self._data.get(key, default)

    def set(self, key, value):
        self._data[str(key)] = value
        self._save()

    def delete(self, key):
        if str(key) in self._data:
            del self._data[str(key)]
            self._save()

    def keys(self):
        return list(self._data.keys())


class _PluginContext:
    def __init__(self, plugin_key, manifest, settings, state_dir, profile_name):
        self.plugin_id = plugin_key
        self.profile_name = profile_name or "default"
        self.manifest = types.SimpleNamespace(**(manifest or {}))
        self._settings = dict(settings or {})
        self.state = _PluginState(state_dir)

        self.tools = {}
        self.tool_defs = []
        self.hooks = {}
        self.hook_defs = []
        self.commands = {}
        self.command_defs = []
        self.prompt_sections = {}
        self.prompt_section_defs = []
        self.unload_callbacks = []

    def get_setting(self, key, default=None):
        return self._settings.get(key, default)

    def get_config(self, key, default=None):
        return self._settings.get(key, default)

    def set_config(self, key, value):
        self._settings[str(key)] = value

    def has_plugin(self, plugin_id):
        return plugin_id == self.plugin_id

    def register_tool(
        self,
        name,
        toolset="plugins",
        schema=None,
        handler=None,
        check_fn=None,
        requires_env=None,
        is_async=False,
        description="",
        emoji="",
        override=False,
    ):
        # Support both Hermes signature (name, toolset, schema, handler) and
        # keyword/short forms (name=..., schema=..., handler=...).
        if callable(toolset) and handler is None:
            handler = toolset
            toolset = "plugins"
        if callable(schema) and handler is None:
            handler = schema
            schema = {}
        if handler is None or not callable(handler):
            raise TypeError(f"register_tool({name!r}): handler must be callable")
        norm_schema = schema if isinstance(schema, dict) else {}
        desc = description or (norm_schema.get("description") if isinstance(norm_schema.get("description"), str) else "")
        params = norm_schema.get("parameters") if isinstance(norm_schema.get("parameters"), dict) else norm_schema
        if not params or "type" not in params:
            params = {"type": "object", "properties": params.get("properties", {}) if isinstance(params, dict) else {}}
        self.tools[name] = handler
        self.tool_defs.append({
            "name": str(name),
            "toolset": str(toolset or "plugins"),
            "description": str(desc or f"Plugin tool {name}"),
            "parameters": params,
            "emoji": str(emoji or ""),
            "override": bool(override),
        })

    def register_hook(self, hook_name, callback, *, hold_stream=False):
        if not callable(callback):
            raise TypeError(f"register_hook({hook_name!r}): callback must be callable")
        hook_name = str(hook_name)
        ref_id = f"hook_{len(self.hook_defs) + 1}"
        self.hooks[ref_id] = callback
        self.hook_defs.append({
            "ref_id": ref_id,
            "hook_name": hook_name,
            "hold_stream": bool(hold_stream),
        })

    def register_command(self, name, handler, description="", args_hint="", argument_mode=None):
        if not callable(handler):
            raise TypeError(f"register_command({name!r}): handler must be callable")
        clean_name = str(name).lstrip("/")
        self.commands[clean_name] = handler
        self.command_defs.append({
            "name": clean_name,
            "description": str(description or f"Plugin command /{clean_name}"),
            "args_hint": str(args_hint or ""),
        })

    def register_system_prompt_section(self, id, content, *, position="after_memory", max_chars=4000):
        sec_id = str(id)
        entry = {
            "id": sec_id,
            "position": str(position or "after_memory"),
            "max_chars": int(max_chars or 4000),
            "dynamic": callable(content),
        }
        if callable(content):
            self.prompt_sections[sec_id] = content
        else:
            entry["content"] = str(content or "")
            self.prompt_sections[sec_id] = str(content or "")
        self.prompt_section_defs.append(entry)

    def on_unload(self, callback):
        if callable(callback):
            self.unload_callbacks.append(callback)

    def register_cli_command(self, *args, **kwargs):
        return None

    def register_middleware(self, *args, **kwargs):
        return None

    def emit(self, event, payload=None):
        return 0

    def subscribe(self, event, callback):
        return None

    def post_message(self, text):
        return None

    def session_cwd(self):
        return os.getcwd()


_ctx = None


def _load_plugin(params):
    global _ctx
    plugin_key = params.get("key") or "plugin"
    plugin_dir = params.get("dir") or ""
    entry_path = params.get("entry") or os.path.join(plugin_dir, "__init__.py")
    manifest = params.get("manifest") or {}
    settings = params.get("settings") or {}
    state_dir = params.get("state_dir") or os.path.join(plugin_dir, ".state")
    profile_name = params.get("profile_name") or "default"

    if plugin_dir and plugin_dir not in sys.path:
        sys.path.insert(0, plugin_dir)

    sanitized = re.sub(r"[^a-zA-Z0-9_]", "_", plugin_key)
    pkg_name = f"hermes_plugin_{sanitized}"
    spec = importlib.util.spec_from_file_location(
        pkg_name,
        entry_path,
        submodule_search_locations=[plugin_dir] if plugin_dir else None,
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load plugin spec from {entry_path}")

    module = importlib.util.module_from_spec(spec)
    sys.modules[pkg_name] = module
    spec.loader.exec_module(module)

    ctx = _PluginContext(plugin_key, manifest, settings, state_dir, profile_name)
    reg_fn = getattr(module, "register", None)
    if callable(reg_fn):
        _run_callable(reg_fn, ctx)
    _ctx = ctx

    return {
        "key": plugin_key,
        "tools": ctx.tool_defs,
        "hooks": ctx.hook_defs,
        "commands": ctx.command_defs,
        "prompt_sections": ctx.prompt_section_defs,
    }


def _call_tool(params):
    if _ctx is None:
        raise RuntimeError("plugin not loaded")
    name = params.get("name")
    args = params.get("args") or {}
    handler = _ctx.tools.get(name)
    if handler is None:
        raise KeyError(f"unknown plugin tool: {name}")
    sig = inspect.signature(handler)
    # Hermes tool handlers accept (args, **kwargs) or (**args)
    param_list = list(sig.parameters.values())
    if len(param_list) >= 1 and param_list[0].name in ("args", "params", "arguments"):
        res = _run_callable(handler, args)
    elif len(param_list) == 0:
        res = _run_callable(handler)
    else:
        res = _call_with_narrowed_kwargs(handler, args)
    return {"result": res}


def _call_hook(params):
    if _ctx is None:
        raise RuntimeError("plugin not loaded")
    ref_id = params.get("ref_id")
    payload = params.get("payload") or {}
    cb = _ctx.hooks.get(ref_id)
    if cb is None:
        raise KeyError(f"unknown plugin hook ref: {ref_id}")
    res = _call_with_narrowed_kwargs(cb, payload)
    return {"result": res}


def _call_command(params):
    if _ctx is None:
        raise RuntimeError("plugin not loaded")
    name = params.get("name")
    raw_args = params.get("args") or ""
    cb = _ctx.commands.get(name)
    if cb is None:
        raise KeyError(f"unknown plugin command: {name}")
    sig = inspect.signature(cb)
    if len(sig.parameters) == 0:
        res = _run_callable(cb)
    else:
        res = _run_callable(cb, raw_args)
    return {"result": res}


def _render_prompt_section(params):
    if _ctx is None:
        raise RuntimeError("plugin not loaded")
    sec_id = params.get("id")
    context = params.get("context") or {}
    item = _ctx.prompt_sections.get(sec_id)
    if item is None:
        return {"content": ""}
    if callable(item):
        sig = inspect.signature(item)
        if len(sig.parameters) == 0:
            res = _run_callable(item)
        else:
            res = _run_callable(item, context)
        return {"content": "" if res is None else str(res)}
    return {"content": str(item)}


def _unload(_params):
    if _ctx is not None:
        for cb in list(_ctx.unload_callbacks):
            try:
                _run_callable(cb)
            except Exception:
                pass
    return {"ok": True}


_DISPATCH = {
    "load_plugin": _load_plugin,
    "call_tool": _call_tool,
    "call_hook": _call_hook,
    "call_command": _call_command,
    "render_prompt_section": _render_prompt_section,
    "unload": _unload,
}


def main():
    for raw_line in _wire_in:
        line = raw_line.strip()
        if not line:
            continue
        req_id = None
        try:
            msg = json.loads(line)
            req_id = msg.get("id")
            method = msg.get("method")
            params = msg.get("params") or {}
            if method == "shutdown":
                _unload(params)
                _send({"id": req_id, "result": {"ok": True}})
                break
            fn = _DISPATCH.get(method)
            if fn is None:
                raise KeyError(f"unknown method: {method}")
            result = fn(params)
            _send({"id": req_id, "result": result})
        except Exception as exc:
            _send({
                "id": req_id,
                "error": {
                    "type": type(exc).__name__,
                    "message": str(exc),
                },
            })


if __name__ == "__main__":
    main()
`
