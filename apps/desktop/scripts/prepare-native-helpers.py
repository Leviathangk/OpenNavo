#!/usr/bin/env python3
"""Build/stage M0 native helpers. Never run, install or register them."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

DESKTOP = Path(__file__).resolve().parents[1]


def run(args):
    subprocess.run(list(map(str, args)), check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--clamav-dir', type=Path)
    parser.add_argument('--target', choices=['arm64', 'x86_64', 'universal'], default='universal')
    parser.add_argument('--allow-unsigned', action='store_true')
    args = parser.parse_args()
    identity = os.environ.get('APPLE_SIGNING_IDENTITY')
    if identity in (None, '', '-') and not args.allow_unsigned:
        parser.error('APPLE_SIGNING_IDENTITY is required (or --allow-unsigned for local build checks)')
    arches = ['arm64', 'x86_64'] if args.target == 'universal' else [args.target]
    work, out = args.work.resolve(), args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    command = ['swift', 'build', '--package-path', DESKTOP / 'native/store',
               '--scratch-path', work / 'store', '--configuration', 'release']
    for arch in arches:
        command += ['--arch', arch]
    run(command)
    path = subprocess.check_output(list(map(str, [*command, '--show-bin-path'])), text=True).strip()
    shutil.copy2(Path(path) / 'opennavo-store', out / 'opennavo-store')
    binaries = [out / 'opennavo-store']
    if args.clamav_dir:
        dest = out / 'clamav'
        dest.mkdir(exist_ok=True)
        for name in ['clamd', 'freshclam', 'clamscan']:
            shutil.copy2(args.clamav_dir / name, dest / name)
            binaries.append(dest / name)
        shutil.copy2(args.clamav_dir / 'clamav.crt', dest / 'clamav.crt')
    for binary in binaries:
        actual = subprocess.check_output(['lipo', '-archs', str(binary)], text=True).split()
        if set(actual) != set(arches):
            raise ValueError(f'Wrong architectures: {binary}: {actual}')
        if identity and identity != '-':
            run(['codesign', '--force', '--options', 'runtime', '--timestamp', '--identifier',
                 f'com.opennavo.desktop.{binary.name}', '--sign', identity, binary])
        else:
            run(['codesign', '--force', '--options', 'runtime', '--sign', '-', binary])
        run(['codesign', '--verify', '--strict', binary])
    (out / 'build.json').write_text(json.dumps({'target': args.target,
        'developerSigned': bool(identity and identity != '-'), 'notarized': False,
        'binaries': [str(binary.relative_to(out)) for binary in binaries]}, indent=2) + '\n')
    print(f'Staged helpers at {out}. No installation, database download or notarization performed.')


if __name__ == '__main__':
    main()
