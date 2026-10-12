#!/usr/bin/env python3
"""Build and stage a nested helper for Tauri's opt-in macOS bundle overlay. Never run/register it."""
import argparse
import os
from pathlib import Path
import plistlib
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1] / "src-tauri"
HELPER_ID = "org.opennavo.desktop.FanHelper"


def validate() -> None:
    with (ROOT / "fan-helper" / f"{HELPER_ID}.plist").open("rb") as source:
        job = plistlib.load(source)
    assert job["Label"] == HELPER_ID
    assert job["BundleProgram"] == "Contents/Library/Helpers/opennavo-fan-helper"
    assert job["MachServices"] == {HELPER_ID: True}
    assert "ProgramArguments" not in job and "EnvironmentVariables" not in job
    with (ROOT / "fan-helper/Info.plist").open("rb") as source:
        assert plistlib.load(source)["CFBundleIdentifier"] == HELPER_ID


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", default=os.environ.get("TAURI_ENV_TARGET_TRIPLE"), help="aarch64-apple-darwin, x86_64-apple-darwin or universal-apple-darwin")
    parser.add_argument("--allow-unsigned",action="store_true",help="stage a read-only unsigned artifact; production IPC still rejects it")
    parser.add_argument("--validate-only", action="store_true")
    args = parser.parse_args()
    validate()
    if os.environ.get("TAURI_ENV_PLATFORM") not in (None,"macos","darwin"):
        print("fan helper packaging is macOS-only")
        return
    if args.validate_only:
        print("helper bundle layout validated; no build, signing, registration or SMC access")
        return
    if args.target not in (None, "aarch64-apple-darwin", "x86_64-apple-darwin", "universal-apple-darwin"):
        parser.error("unsupported macOS target")
    identity = os.environ.get("APPLE_SIGNING_IDENTITY")
    if (not identity or identity == "-") and not args.allow_unsigned:
        parser.error("APPLE_SIGNING_IDENTITY must be a developer identity; use --allow-unsigned only for a read-only artifact")
    targets = ["aarch64-apple-darwin", "x86_64-apple-darwin"] if args.target == "universal-apple-darwin" else [args.target]
    profile = "debug" if os.environ.get("TAURI_ENV_DEBUG") == "true" else "release"
    built = []
    for arch in targets:
        command = ["cargo", "build", "--bin", "opennavo-fan-helper", "--features", "fan-helper"]
        if profile == "release":
            command += ["--release"]
        if arch:
            command += ["--target", arch]
        subprocess.run(command,cwd=ROOT,check=True,env={**os.environ,"MACOSX_DEPLOYMENT_TARGET":os.environ.get("MACOSX_DEPLOYMENT_TARGET","13.0")})
        target = Path(os.environ.get("CARGO_TARGET_DIR", str(ROOT / "target")))
        if not target.is_absolute():
            target = ROOT / target
        if arch:
            target /= arch
        built.append(target / profile / "opennavo-fan-helper")
    staged = ROOT / "fan-helper/staged/opennavo-fan-helper"
    if len(built) == 2:
        subprocess.run(["xcrun", "lipo", "-create", *map(str,built), "-output", str(staged)],check=True)
    else:
        shutil.copy2(built[0], staged)
    if identity and identity != "-":
        # Nested executable is signed before the outer app, with the same identity.
        subprocess.run(["codesign", "--force", "--options", "runtime", "--timestamp", "--identifier", HELPER_ID, "--sign", identity, str(staged)], check=True)
        subprocess.run(["codesign", "--verify", "--strict", str(staged)], check=True)
        print("developer-signed helper staged for the containing Tauri app")
    else:
        print("unsigned helper staged; audit-token IPC and root daemon entry remain disabled")


if __name__ == "__main__":
    main()
