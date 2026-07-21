#!/usr/bin/env python3
# go-port's Python subprocess/IPC bridge for real bundled/custom pwnagotchi
# plugins. This is NOT a reimplementation of plugins/__init__.py: it imports
# and runs the REAL, unmodified pwnagotchi.plugins module from whatever
# pwnagotchi Python install is on PYTHONPATH (the real system install on a
# production unit; the dev reference venv here), so every bundled plugin
# executes as genuine Python with its real dependencies (RPi.GPIO, dbus,
# requests, ...) — not a mock, not a partial port.
#
# Protocol (newline-delimited JSON on stdin/stdout, one message per line):
#   Go -> bridge (stdin):  {"id": <int>, "event": "<name>", "args": [...]}
#   bridge -> Go (stdout): {"id": <int>, "ok": true}
#                       or {"id": <int>, "ok": false, "error": "<repr>"}
#   bridge -> Go (stdout), once at startup after plugins.load() completes:
#                          {"ready": true, "loaded": ["plugin_a", ...]}
#
# Matches pwnagotchi.plugins.on()'s own real semantics exactly: `on()` only
# enqueues work onto each plugin's per-plugin serial worker thread and
# returns immediately, so the "ok" ack here confirms dispatch, not handler
# completion — a real per-plugin handler exception is caught and logged
# by plugins.py's own process_events loop (see its logging.exception call),
# which this script routes to stderr, not back over the wire, exactly as
# real Python does today (nothing here changes that fire-and-forget shape).
#
# In addition to fire-and-forget "event" messages, the bridge also supports
# synchronous "call" messages for the handful of things go-port/internal/web
# (the ui/web/*.py port) genuinely needs a real answer back for — real
# Python plugins.py state and real plugin webhook responses, not a
# Go-side guess at them:
#   Go -> bridge (stdin):  {"id": <int>, "call": "list_plugins"}
#   bridge -> Go (stdout): {"id": <int>, "result": {"loaded": {name: {...}},
#                           "database": {name: path}, "default_plugins": [...]}}
#
#   Go -> bridge (stdin):  {"id": <int>, "call": "toggle_plugin",
#                           "name": "<plugin>", "enable": true}
#   bridge -> Go (stdout): {"id": <int>, "result": true|false}
#   (calls the real plugins.toggle_plugin(name, enable) — real load/unload,
#   not a Go-side simulation of it)
#
#   Go -> bridge (stdin):  {"id": <int>, "call": "webhook", "name": "<plugin>",
#                           "subpath": "...", "method": "GET", "path": "/...",
#                           "query": "a=b", "headers": {"H": ["v"]},
#                           "body_b64": "..."}
#   bridge -> Go (stdout): {"id": <int>, "result": {"status": 200,
#                           "headers": {...}, "body_b64": "..."}}
#   (constructs a REAL flask.Request via Flask's own test_request_context
#   and calls the plugin's real on_webhook(subpath, flask.request) — the
#   plugin gets the genuine Flask request API it was written against, not a
#   stub, because on_webhook handlers routinely call real methods
#   (request.args, request.form, request.data) that a stub can't honor)
#   any "error" response (for either message kind) means the id had no
#   normal result: {"id": <int>, "error": "<repr>"}.
#
# `agent`/`ui`/`view`/`display` EVENT arguments cannot be marshaled as live
# Python objects across a process boundary: Go substitutes a JSON marker
# {"__goref__": "<name>"} for them, which this script turns into a
# _GoProxyStub whose every attribute access raises NotImplementedError with
# a clear message. A plugin whose on_<event> only accepts and ignores that
# argument (e.g. real pwnagotchi/plugins/default/cache.py) runs completely
# normally; a plugin that actually calls a method on it gets a real,
# clearly-labeled exception instead of silently doing nothing or crashing
# the bridge for other plugins — see docs/known-differences.md.
import base64
import json
import logging
import os
import sys

logging.basicConfig(
    stream=sys.stderr,
    level=logging.DEBUG,
    format="[pyplugin] %(name)s %(levelname)s: %(message)s",
)

import pwnagotchi.plugins as plugins  # noqa: E402  (after sys.path/logging setup)


class _GoProxyStub:
    """Placeholder for a live Go object (Agent/View/Display) passed to a
    plugin callback. See module docstring: any attribute access is a real,
    loud failure, never silent no-op fake success."""

    def __init__(self, name):
        self._go_ref_name = name

    def __getattr__(self, attr):
        if attr.startswith("_"):
            raise AttributeError(attr)
        raise NotImplementedError(
            "go-port pyplugin bridge: %s.%s is not supported — there is no "
            "live Go<->Python RPC proxy for agent/view/display objects yet "
            "(see go-port/docs/known-differences.md)" % (self._go_ref_name, attr)
        )

    def __repr__(self):
        return "<GoProxyStub %s>" % self._go_ref_name


def _resolve_arg(a):
    if isinstance(a, dict) and set(a.keys()) == {"__goref__"}:
        return _GoProxyStub(a["__goref__"])
    if isinstance(a, list):
        return [_resolve_arg(x) for x in a]
    if isinstance(a, dict):
        return {k: _resolve_arg(v) for k, v in a.items()}
    return a


def _plugin_meta(name, instance):
    meta = {}
    for attr in ("__version__", "__author__", "__description__", "__license__"):
        val = getattr(instance, attr, None)
        if val is not None:
            meta[attr.strip("_")] = val
    meta["has_webhook"] = callable(getattr(instance, "on_webhook", None))
    return meta


def _call_list_plugins():
    default_path = os.path.join(os.path.dirname(os.path.realpath(plugins.__file__)), "default")
    default_plugins = [
        name for name, path in plugins.database.items() if path.startswith(default_path)
    ]
    return {
        "loaded": {name: _plugin_meta(name, inst) for name, inst in plugins.loaded.items()},
        "database": dict(plugins.database),
        "default_plugins": default_plugins,
    }


class _ViewRootStub:
    """Real pwnagotchi.plugins.toggle_plugin's enable path unconditionally
    reads `view.ROOT._agent` (plugins/__init__.py: `one(name, 'ready',
    view.ROOT._agent)`) — real Python only works because a live daemon
    always has view.ROOT set to its real View singleton by the time
    anything could call toggle_plugin. This bridge process never runs
    that real View (it lives in the Go process), so without this it would
    always AttributeError on `None._agent`. Providing a plain object whose
    `_agent` is None is the same state a real daemon has before its own
    agent has registered with the view — not a behavior change, just
    supplying the one piece of real daemon state this isolated subprocess
    doesn't otherwise have."""

    _agent = None


def _call_toggle_plugin(params):
    import pwnagotchi.ui.view as view

    if view.ROOT is None:
        view.ROOT = _ViewRootStub()
    return plugins.toggle_plugin(params["name"], bool(params.get("enable", True)))


# A Flask app used ONLY to build real flask.Request objects for on_webhook
# via test_request_context — never actually bound to a socket. This gives
# plugins the genuine Flask request API (request.args/form/data/headers)
# real on_webhook implementations are written against, not a hand-rolled
# stand-in that would silently diverge on edge cases.
_webhook_app = None


def _call_webhook(params):
    global _webhook_app
    import flask
    import pwnagotchi.ui.web as web_pkg

    if _webhook_app is None:
        # Real template_folder, matching pwnagotchi/ui/web/server.py's own
        # Flask app construction exactly: plugin on_webhook handlers that
        # render_template_string(...) with `{% extends "base.html" %}`
        # (e.g. logtail.py, session-stats.py) need the SAME real templates
        # directory a real server would have, or that extends lookup fails.
        web_dir = os.path.dirname(os.path.realpath(web_pkg.__file__))
        _webhook_app = flask.Flask(
            "pyplugin_webhook_bridge",
            template_folder=os.path.join(web_dir, "templates"),
            static_folder=os.path.join(web_dir, "static"),
        )

    name = params["name"]
    if name not in plugins.loaded or not callable(getattr(plugins.loaded[name], "on_webhook", None)):
        raise LookupError("plugin %r has no on_webhook" % name)

    body = base64.b64decode(params.get("body_b64", "")) if params.get("body_b64") else b""
    path = params.get("path") or "/"
    query = params.get("query") or ""
    full_path = path + ("?" + query if query else "")
    headers = params.get("headers") or {}
    flat_headers = {k: v[0] if isinstance(v, list) and v else v for k, v in headers.items()}

    with _webhook_app.test_request_context(
        full_path, method=params.get("method", "GET"), data=body, headers=flat_headers
    ):
        result = plugins.loaded[name].on_webhook(params.get("subpath"), flask.request)
        resp = _webhook_app.make_response(result)
        return {
            "status": resp.status_code,
            "headers": {k: v for k, v in resp.headers.items()},
            "body_b64": base64.b64encode(resp.get_data()).decode("ascii"),
        }


_CALL_HANDLERS = {
    "list_plugins": lambda params: _call_list_plugins(),
    "toggle_plugin": _call_toggle_plugin,
    "webhook": _call_webhook,
}


def main():
    if len(sys.argv) != 2:
        print("usage: bridge.py <config.json>", file=sys.stderr)
        return 2

    with open(sys.argv[1], "r") as f:
        config = json.load(f)

    try:
        plugins.load(config)
    except Exception as e:
        logging.exception("plugins.load failed: %r", e)

    print(json.dumps({"ready": True, "loaded": sorted(plugins.loaded.keys())}), flush=True)

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except Exception as e:
            print(json.dumps({"id": None, "ok": False, "error": "bad json: %r" % e}), flush=True)
            continue

        msg_id = msg.get("id")

        if "call" in msg:
            handler = _CALL_HANDLERS.get(msg["call"])
            if handler is None:
                print(json.dumps({"id": msg_id, "error": "unknown call %r" % msg["call"]}), flush=True)
                continue
            try:
                result = handler(msg)
                print(json.dumps({"id": msg_id, "result": result}), flush=True)
            except Exception as e:
                logging.exception("call %r failed", msg["call"])
                print(json.dumps({"id": msg_id, "error": repr(e)}), flush=True)
            continue

        event = msg.get("event", "")
        args = [_resolve_arg(a) for a in msg.get("args", [])]
        try:
            plugins.on(event, *args)
            print(json.dumps({"id": msg_id, "ok": True}), flush=True)
        except Exception as e:
            logging.exception("dispatching event %r failed", event)
            print(json.dumps({"id": msg_id, "ok": False, "error": repr(e)}), flush=True)

    return 0


if __name__ == "__main__":
    sys.exit(main())
