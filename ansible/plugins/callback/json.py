# -*- coding: utf-8 -*-
"""Structured JSON stdout callback for Air Traffic Control.

ansible-core removed the bundled ``json`` stdout callback (it is absent from
2.19 onwards), but the coordinator's queue runner and the web UI both depend on
its output shape to parse per-host results and render the task accordion.

This module re-implements that contract (``plays[].tasks[].hosts[host]`` plus a
``stats`` map) so ``ANSIBLE_STDOUT_CALLBACK=json`` keeps working — the plugin
directory is put on ``ANSIBLE_CALLBACK_PLUGINS`` by the Go runner.

Based on the historical ansible.builtin json callback (MIT).
"""

from __future__ import annotations

import json
import time

from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    """Collect task results per host and emit one JSON document at the end."""

    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "stdout"
    CALLBACK_NAME = "json"
    CALLBACK_NEEDS_ENABLED = False

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.plays = []
        self.stats = {}
        self._current_play = None
        self._current_task = None

    # -- playbook lifecycle -------------------------------------------------

    def v2_playbook_on_play_start(self, play):
        self._current_play = {
            "play": {
                "name": play.get_name().strip() or "unnamed play",
                "id": str(getattr(play, "_uuid", "")),
                "hosts": {},
            },
            "tasks": [],
        }
        self.plays.append(self._current_play)
        self._current_task = None

    def v2_playbook_on_task_start(self, task, is_conditional):
        if self._current_play is None:
            self._current_play = {"play": {"name": "unnamed play", "id": ""}, "tasks": []}
            self.plays.append(self._current_play)
        self._current_task = {
            "task": {
                "name": task.get_name().strip() or "unnamed task",
                "id": str(getattr(task, "_uuid", "")),
                "duration": {"start": time.time()},
            },
            "hosts": {},
        }
        self._current_play["tasks"].append(self._current_task)

    # -- per-host results ---------------------------------------------------

    def _record(self, result, status, extra=None):
        if self._current_task is None:
            return
        host = result._host.get_name()
        entry = dict(result._result or {})
        entry["host"] = host
        entry["status"] = status
        if extra:
            entry.update(extra)
        entry.setdefault("changed", bool(entry.get("changed", False)))
        self._current_task["hosts"][host] = entry

    def v2_runner_on_ok(self, result):
        self._record(result, "ok")

    def v2_runner_on_failed(self, result, ignore_errors=False):
        # `failed: true` keeps the failure visible; `ignored` tells the
        # coordinator this step must not halt the queue.
        self._record(result, "failed", {"failed": True, "ignored": bool(ignore_errors)})

    def v2_runner_on_skipped(self, result):
        self._record(result, "skipped")

    def v2_runner_on_unreachable(self, result):
        self._record(result, "unreachable", {"unreachable": True, "failed": True})

    def v2_runner_on_async_failed(self, result, **kwargs):
        self._record(result, "failed", {"failed": True})

    # -- final document -----------------------------------------------------

    def v2_playbook_on_stats(self, stats):
        for host in sorted(stats.processed.keys()):
            summary = stats.summarize(host)
            self.stats[host] = {
                "ok": summary.get("ok", 0),
                "changed": summary.get("changed", 0),
                "unreachable": summary.get("unreachable", 0),
                "skipped": summary.get("skipped", 0),
                "failures": summary.get("failures", 0),
                "rescued": summary.get("rescued", 0),
                "ignored": summary.get("ignored", 0),
            }
        self._display_results()

    def _display_results(self):
        document = {
            "plays": self.plays,
            "stats": self.stats,
            "custom_stats": {},
        }
        # One line, flushed: the coordinator parses the whole stdout as JSON.
        self._display.display(json.dumps(document, default=str))
