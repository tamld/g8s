#!/usr/bin/env python3
"""
g8s Pilot Record Validator
Validates pilot record against assets/pilot-record.schema.json
Usage: python validate_g8s_pilot.py <pilot-record.json>
"""

import json
import sys
import jsonschema
from pathlib import Path

SCHEMA_PATH = Path(__file__).parent.parent / "assets" / "pilot-record.schema.json"


def load_schema():
    with open(SCHEMA_PATH) as f:
        return json.load(f)


def validate(pilot_record_path: str) -> tuple[bool, list[str]]:
    schema = load_schema()
    with open(pilot_record_path) as f:
        record = json.load(f)

    validator = jsonschema.Draft7Validator(schema)
    errors = list(validator.iter_errors(record))

    if errors:
        messages = []
        for err in errors:
            path = " -> ".join(str(p) for p in err.absolute_path)
            messages.append(f"{path}: {err.message}")
        return False, messages
    return True, []


def main():
    if len(sys.argv) != 2:
        print("Usage: python validate_g8s_pilot.py <pilot-record.json>", file=sys.stderr)
        sys.exit(1)

    path = sys.argv[1]
    if not Path(path).exists():
        print(f"File not found: {path}", file=sys.stderr)
        sys.exit(1)

    ok, errors = validate(path)
    if ok:
        print("✅ Pilot record VALID")
        sys.exit(0)
    else:
        print("❌ Pilot record INVALID", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()