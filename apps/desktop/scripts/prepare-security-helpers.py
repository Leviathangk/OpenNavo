#!/usr/bin/env python3
"""Stage independent ClamAV processes for Tauri. Never runs the engine or updater."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

DESKTOP = Path(__file__).resolve().parents[1]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=os.environ.get('OPENNAVO_CLAMAV_DIR'))
    parser.add_argument('--out', type=Path, default=DESKTOP / 'native/staged/clamav')
    parser.add_argument('--target', default=os.environ.get('TAURI_ENV_TARGET_TRIPLE', 'universal-apple-darwin'))
    parser.add_argument('--allow-unsigned', action='store_true')
    args = parser.parse_args()
    if args.source is None:
        parser.error('OPENNAVO_CLAMAV_DIR or --source must point to the matching standalone ClamAV build')
    source = Path(args.source).resolve()
    out = args.out.resolve()
    arches = {'aarch64-apple-darwin': ['arm64'], 'x86_64-apple-darwin': ['x86_64'],
              'universal-apple-darwin': ['arm64', 'x86_64']}[args.target]
    identity = os.environ.get('APPLE_SIGNING_IDENTITY')
    if not identity and not args.allow_unsigned:
        parser.error('APPLE_SIGNING_IDENTITY is required')
    out.mkdir(parents=True, exist_ok=True)
    for name in ['clamd', 'freshclam', 'clamscan', 'clamav.crt']:
        shutil.copy2(source / name, out / name)
        if name.endswith('.crt'):
            continue
        binary = out / name
        actual = subprocess.check_output(['lipo', '-archs', str(binary)], text=True).split()
        if not set(arches).issubset(actual):
            raise ValueError(f'Wrong ClamAV architectures: {name}: {actual}')
        if len(arches) == 1 and len(actual) > 1:
            thin = out / f'{name}.thin'
            subprocess.run(['lipo', str(binary), '-thin', arches[0], '-output', str(thin)], check=True)
            thin.replace(binary)
        command = ['codesign', '--force', '--options', 'runtime', '--sign', identity or '-']
        if identity and identity != '-':
            command += ['--timestamp']
        subprocess.run([*command, str(binary)], check=True)
        subprocess.run(['codesign', '--verify', '--strict', str(binary)], check=True)
    print(json.dumps({'staged': str(out), 'architectures': arches, 'databaseDownloaded': False}))

if __name__ == '__main__':
    main()
