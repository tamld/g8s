#!/usr/bin/env python3
"""
g8s Binary Pin Verifier
Verifies .g8s-pin.json against actual binary and freshness policy
Usage: python verify_g8s_pin.py --pin .g8s-pin.json [--strict]
"""

import argparse
import hashlib
import json
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()


def run_g8s_version(bin_path: Path) -> dict:
    try:
        result = subprocess.run([str(bin_path), "version", "--json"], capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            return {"error": f"version --json failed: {result.stderr}"}
        return json.loads(result.stdout)
    except Exception as e:
        return {"error": str(e)}


def run_g8s_doctor(bin_path: Path) -> dict:
    try:
        result = subprocess.run([str(bin_path), "doctor", "--json"], capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            return {"error": f"doctor --json failed: {result.stderr}"}
        return json.loads(result.stdout)
    except Exception as e:
        return {"error": str(e)}


def verify_pin(pin_path: Path, strict: bool = False) -> tuple[bool, list[str]]:
    with open(pin_path) as f:
        pin = json.load(f)

    errors = []
    warnings = []

    # 1. Verify canonical path exists
    bin_path = Path(pin["canonical_path"])
    if not bin_path.exists():
        errors.append(f"Binary not found at canonical_path: {bin_path}")
        return False, errors

    # 2. Verify SHA-256
    actual_sha256 = sha256_file(bin_path)
    expected_sha256 = pin["sha256"].lower()
    if actual_sha256 != expected_sha256:
        errors.append(f"SHA-256 mismatch: expected {expected_sha256}, got {actual_sha256}")
    else:
        print(f"✅ SHA-256 matches: {actual_sha256[:16]}...")

    # 3. Verify version.json matches provenance
    version_info = run_g8s_version(bin_path)
    if "error" in version_info:
        errors.append(f"Cannot get version: {version_info['error']}")
    else:
        prov = pin["provenance"]
        if version_info.get("git_commit") != prov.get("git_commit"):
            errors.append(f"Git commit mismatch: pin={prov.get('git_commit')}, binary={version_info.get('git_commit')}")
        else:
            print(f"✅ Git commit matches: {prov.get('git_commit')[:8]}")

    # 4. Verify doctor capability evidence
    doctor_info = run_g8s_doctor(bin_path)
    if "error" in doctor_info:
        errors.append(f"Cannot run doctor: {doctor_info['error']}")
    else:
        print(f"✅ Doctor check passed")

    # 5. Check freshness expiry
    expires_at = pin.get("expires_at")
    if expires_at:
        expiry = datetime.fromisoformat(expires_at.replace("Z", "+00:00"))
        now = datetime.now(timezone.utc)
        if now > expiry:
            if strict:
                errors.append(f"Pin expired at {expires_at}")
            else:
                warnings.append(f"Pin expired at {expires_at} (non-strict mode)")
        else:
            print(f"✅ Pin valid until {expires_at}")

    # 6. Freshness policy check
    policy = pin.get("freshness_policy", "pinned")
    if policy not in ("pinned", "latest-released", "latest-build"):
        warnings.append(f"Unknown freshness_policy: {policy}")

    if warnings:
        for w in warnings:
            print(f"⚠️  {w}")

    return len(errors) == 0, errors


def main():
    parser = argparse.ArgumentParser(description="Verify g8s binary pin")
    parser.add_argument("--pin", required=True, help="Path to .g8s-pin.json")
    parser.add_argument("--strict", action="store_true", help="Treat expiry as error")
    args = parser.parse_args()

    pin_path = Path(args.pin)
    if not pin_path.exists():
        print(f"Pin file not found: {pin_path}", file=sys.stderr)
        sys.exit(1)

    ok, errors = verify_pin(pin_path, args.strict)
    if ok:
        print("\n✅ PIN VERIFICATION PASSED")
        sys.exit(0)
    else:
        print("\n❌ PIN VERIFICATION FAILED", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()