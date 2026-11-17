#!/usr/bin/env python3
"""Build and stage the web app helper used by apps created in OpenNavo. Never launches it."""
import argparse
import os
import platform
from pathlib import Path
import sys
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1] / "src-tauri"
SOURCE = ROOT / "../native/webapp-helper/main.swift"
STAGED = ROOT.parent / "native/staged/opennavo-webapp-helper"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--target",
        default=os.environ.get("TAURI_ENV_TARGET_TRIPLE"),
        help="aarch64-apple-darwin, x86_64-apple-darwin or universal-apple-darwin",
    )
    parser.add_argument("--allow-unsigned", action="store_true", help="stage a local-only artifact")
    args = parser.parse_args()
    if sys.platform != "darwin" or os.environ.get("TAURI_ENV_PLATFORM") not in (None, "macos", "darwin"):
        print("web app helper packaging is macOS-only")
        return
    identity = os.environ.get("APPLE_SIGNING_IDENTITY")
    if (not identity or identity == "-") and not args.allow_unsigned:
        parser.error("APPLE_SIGNING_IDENTITY must be a developer identity; use --allow-unsigned for local builds")
    host = "aarch64-apple-darwin" if platform.machine() == "arm64" else "x86_64-apple-darwin"
    target = args.target or host
    if target == "universal-apple-darwin":
        arches = ["arm64", "x86_64"]
    elif target.startswith("aarch64"):
        arches = ["arm64"]
    elif target.startswith("x86_64"):
        arches = ["x86_64"]
    else:
        parser.error(f"unsupported macOS target: {target}")

    work = ROOT / "target/webapp-helper"
    cache = work / "module-cache"
    work.mkdir(parents=True, exist_ok=True)
    cache.mkdir(parents=True, exist_ok=True)
    built = []
    for arch in arches:
        output = work / f"opennavo-webapp-helper-{arch}"
        subprocess.run(
            [
                "xcrun",
                "swiftc",
                "-O",
                "-swift-version",
                "5",
                "-module-cache-path",
                str(cache),
                "-target",
                f"{arch}-apple-macos13.0",
                "-framework",
                "AppKit",
                "-framework",
                "WebKit",
                str(SOURCE),
                "-o",
                str(output),
            ],
            check=True,
        )
        built.append(output)
    STAGED.parent.mkdir(parents=True, exist_ok=True)
    if len(built) == 2:
        subprocess.run(["xcrun", "lipo", "-create", *map(str, built), "-output", str(STAGED)], check=True)
    else:
        shutil.copy2(built[0], STAGED)
    if identity and identity != "-":
        subprocess.run(
            [
                "codesign",
                "--force",
                "--options",
                "runtime",
                "--timestamp",
                "--identifier",
                "com.opennavo.desktop.webapp-helper",
                "--sign",
                identity,
                str(STAGED),
            ],
            check=True,
        )
    else:
        subprocess.run(
            ["codesign", "--force", "--options", "runtime", "--sign", "-", str(STAGED)],
            check=True,
        )
    subprocess.run(["codesign", "--verify", "--strict", str(STAGED)], check=True)
    print(f"Staged {STAGED}")


if __name__ == "__main__":
    main()
