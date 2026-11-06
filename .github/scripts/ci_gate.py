#!/usr/bin/env python3
"""Aggregate CI gate: validates needs results against the classified scope."""
import argparse
import json

PROVENANCE = "signed GitHub CI provenance"
MAIN_REF = "refs/heads/main"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--scope", required=True)
    parser.add_argument("--event", required=True)
    parser.add_argument("--ref", required=True)
    parser.add_argument("--needs-json", required=True)
    args = parser.parse_args()

    try:
        needs = json.loads(args.needs_json)
    except ValueError as exc:
        raise SystemExit(f"malformed needs JSON: {exc}")
    if not isinstance(needs, dict):
        raise SystemExit("needs JSON must be an object")
    for name, job in needs.items():
        if not isinstance(job, dict) or not isinstance(job.get("result"), str):
            raise SystemExit(f"malformed needs entry: {name}")

    if args.scope == "docs-only":
        print("CI-SCOPE docs-only")
        raise SystemExit(0)
    if args.scope != "code":
        raise SystemExit("unknown scope")
    if not needs:
        raise SystemExit("no needed jobs reported")
    for name, job in needs.items():
        result = job.get("result")
        allowed_provenance_skip = (
            name == PROVENANCE and result == "skipped" and args.ref != MAIN_REF
        )
        if result != "success" and not allowed_provenance_skip:
            raise SystemExit(f"required job {name}: {result}")
    print("CI-SCOPE code")


if __name__ == "__main__":
    main()
