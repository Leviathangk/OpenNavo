"""Attach and validate an Apple ticket before creating distribution archives."""

import subprocess
import sys
import time


def staple_and_validate(path, attempts=5):
    for attempt in range(attempts):
        for operation in ("staple", "validate"):
            result = subprocess.run(
                ["xcrun", "stapler", operation, path],
                capture_output=True,
                text=True,
                timeout=120,
            )
            print(result.stdout, end="", flush=True)
            print(result.stderr, end="", file=sys.stderr, flush=True)
            if result.returncode == 0:
                continue
            diagnostic = result.stdout + result.stderr
            network_error = result.returncode == 68 and (
                "NSURLErrorDomain" in diagnostic or "CloudKit" in diagnostic
            )
            missing_ticket = (
                operation == "validate"
                and result.returncode == 65
                and "does not have a ticket stapled" in diagnostic
            )
            if not (network_error or missing_ticket) or attempt + 1 == attempts:
                raise subprocess.CalledProcessError(result.returncode, result.args)
            print(f"Apple ticket unavailable; retrying in 15 seconds ({attempt + 1}/{attempts}).", flush=True)
            time.sleep(15)
            break
        else:
            return


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("Usage: staple-and-validate.py <app-or-dmg>")
    staple_and_validate(sys.argv[1])
