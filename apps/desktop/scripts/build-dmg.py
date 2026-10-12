#!/usr/bin/env python3
"""Package OpenNavo.app into the installer DMG. The Finder layout is written straight into .DS_Store, so no GUI session or Finder automation is needed."""
import argparse
from pathlib import Path
import subprocess

import dmgbuild

DESKTOP = Path(__file__).resolve().parents[1]
# Background size in points; packaging/dmg/background.svg draws the arrow between these icon centres.
CANVAS = (660, 400)
ICONS = {"OpenNavo.app": (180, 190), "Applications": (480, 190)}
# Finder window bounds include the title bar (28 pt through macOS 15, a few points taller since) above the background.
WINDOW = ((400, 400), (CANVAS[0], CANVAS[1] + 28))
# Finder lists dot-files when hidden files are shown; park them below the window instead of inside the layout.
HIDDEN = {".background.tiff": (180, 560), ".VolumeIcon.icns": (480, 560)}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True, help="built OpenNavo.app")
    parser.add_argument("--out", type=Path, required=True, help="output .dmg path")
    parser.add_argument("--sign", metavar="IDENTITY", help="Developer ID identity for the disk image; omit for unsigned builds")
    args = parser.parse_args()
    if args.app.name != "OpenNavo.app" or not (args.app / "Contents/Info.plist").is_file():
        parser.error("--app must point to a built OpenNavo.app")
    if args.out.suffix != ".dmg":
        parser.error("--out must end with .dmg")
    args.out.parent.mkdir(parents=True, exist_ok=True)
    dmgbuild.build_dmg(str(args.out), "OpenNavo", settings={
        "files": [str(args.app)],
        "symlinks": {"Applications": "/Applications"},
        "icon": str(DESKTOP / "src-tauri/icons/icon.icns"),
        # background@2x.png beside it is merged into a Retina TIFF by dmgbuild.
        "background": str(DESKTOP / "packaging/dmg/background.png"),
        "window_rect": WINDOW,
        "icon_size": 128,
        "text_size": 13,
        "icon_locations": {**ICONS, **HIDDEN},
        "format": "UDZO",
        "compression_level": 9,
    })
    if args.sign:
        subprocess.run(["codesign", "--force", "--timestamp", "--sign", args.sign, str(args.out)], check=True)
        subprocess.run(["codesign", "--verify", "--strict", str(args.out)], check=True)
    print(f"{args.out.name}: built{' and signed' if args.sign else ' unsigned'}")


if __name__ == "__main__":
    main()
