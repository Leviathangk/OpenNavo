#!/usr/bin/env python3
"""Build and sign the user-session App Store helper; never execute it."""
import argparse
import os
from pathlib import Path
import subprocess
import sys

desktop = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--allow-unsigned', action='store_true')
parser.add_argument('--target', default=os.environ.get('TAURI_ENV_TARGET_TRIPLE', 'universal-apple-darwin'))
parser.add_argument('--work', type=Path, default=desktop / 'native/store/.build/bundle')
parser.add_argument('--out', type=Path, default=desktop / 'native/staged')
args = parser.parse_args()
targets = {'aarch64-apple-darwin': 'arm64', 'x86_64-apple-darwin': 'x86_64',
           'universal-apple-darwin': 'universal'}
if args.target not in targets:
    parser.error('unsupported macOS target')
command = [sys.executable, str(desktop / 'scripts/prepare-native-helpers.py'),
           '--work', str(args.work), '--out', str(args.out), '--target', targets[args.target]]
if args.allow_unsigned:
    command.append('--allow-unsigned')
subprocess.run(command, check=True)
