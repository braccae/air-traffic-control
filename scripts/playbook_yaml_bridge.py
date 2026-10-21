#!/usr/bin/env python3
"""Playbook YAML <-> JSON bridge for the visual playbook editor.

The coordinator shells out to the Ansible virtualenv's Python (which always has
PyYAML, a hard dependency of ansible-core) and speaks one JSON object per line
on stdin/stdout. Parsing playbook YAML with a real YAML implementation is what
keeps the visual editor's view of a playbook identical to what ansible-playbook
will load -- no hand-rolled approximation of the format.

Protocol:
  request : {"action": "parse", "content": "<yaml text>"}
  response: {"success": true, "document": <parsed value>}
          | {"success": false, "error": "...", "line": n, "column": n,
             "problem": "...", "context": "..."}

Exit status is always 0 for handled requests; transport failures (bad JSON,
missing PyYAML) exit non-zero so the caller can report them verbatim.
"""

import json
import sys


def fail(message, **extra):
    payload = {"success": False, "error": message}
    payload.update({k: v for k, v in extra.items() if v is not None})
    json.dump(payload, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()
    return 0


def main():
    raw = sys.stdin.read()
    try:
        request = json.loads(raw) if raw.strip() else {}
    except ValueError as exc:
        json.dump({"success": False, "error": "Malformed bridge request: %s" % exc}, sys.stdout)
        sys.stdout.write("\n")
        return 2

    action = request.get("action", "parse")
    if action != "parse":
        return fail("Unsupported bridge action: %r" % (action,))

    content = request.get("content")
    if not isinstance(content, str):
        return fail("'content' must be a string")

    try:
        import yaml  # noqa: WPS433 - resolved from the Ansible virtualenv
    except Exception as exc:  # pragma: no cover - environment failure
        return fail("PyYAML is unavailable in this interpreter: %s" % exc)

    try:
        document = yaml.safe_load(content)
    except yaml.YAMLError as exc:
        mark = getattr(exc, "problem_mark", None)
        problem = getattr(exc, "problem", None)
        context = getattr(exc, "context", None)
        text = "%s%s" % (("%s " % context) if context else "", problem or exc)
        return fail(
            text.strip(),
            line=(mark.line + 1) if mark is not None else None,
            column=(mark.column + 1) if mark is not None else None,
            problem=problem,
            context=context,
        )

    json.dump({"success": True, "document": document}, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
