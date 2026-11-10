#!/usr/bin/env python3
"""Sign the bundled Hermes runtime before Tauri signs and notarizes the app.

TCC permissions for cua-driver still need validation on a clean signed Mac;
this script only handles the nested Mach-O signatures.
"""
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

identity = os.environ.get("APPLE_SIGNING_IDENTITY") or os.environ.get("CODESIGN_IDENTITY")
if not identity:
    print("prepare-hermes-bundle: no signing identity; leaving the Hermes runtime unsigned (development only).")
    sys.exit(0)

root = Path(__file__).resolve().parent.parent / "src-tauri" / "resources" / "hermes"
if not root.exists():
    print("prepare-hermes-bundle: no Hermes runtime staged.")
    sys.exit(0)

mach_o = []
for path in root.rglob("*"):
    if not path.is_file():
        continue
    if "CuaDriver.app" in path.parts:
        continue
    if path.suffix in {".so", ".dylib"} or os.access(path, os.X_OK):
        mach_o.append(path)
# Nested libraries first; the wrapper and Python binary after them.
mach_o.sort(key=lambda path: len(path.parts), reverse=True)
for path in mach_o:
    result = subprocess.run(
        ["codesign", "--force", "--options", "runtime", "--timestamp", "--sign", identity, str(path)],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print(f"prepare-hermes-bundle: codesign failed for {path}: {result.stderr.strip()}", file=sys.stderr)
        sys.exit(result.returncode)
print(f"prepare-hermes-bundle: signed {len(mach_o)} runtime files.")
