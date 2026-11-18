#!/usr/bin/env python3
"""Bind a hand-reviewed build-failure claim to an exact finding and revision."""

import argparse
import hashlib
import json


def finding_digest(head, path, line, title, rationale):
    raw = json.dumps([head, path, line, title, rationale], ensure_ascii=False, separators=(",", ":"))
    for literal, escaped in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        raw = raw.replace(literal, escaped)
    return hashlib.sha256(raw.encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("head", "path", "title", "rationale"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--line", type=int, required=True)
    parser.add_argument("--check-index", type=int, default=0)
    args = parser.parse_args()
    print(json.dumps({"finding_sha256": finding_digest(args.head, args.path, args.line, args.title, args.rationale),
                      "head": args.head, "path": args.path, "kind": "go_build_failure", "check_index": args.check_index}, indent=2))


if __name__ == "__main__":
    main()
