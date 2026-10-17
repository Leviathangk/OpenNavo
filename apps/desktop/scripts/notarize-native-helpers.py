#!/usr/bin/env python3
"""Submit a prepared native helper ZIP. Requires explicit notary credentials."""
import argparse
import json
import os
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--helpers', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--keychain-profile')
    args = parser.parse_args()
    credentials = ['--keychain-profile', args.keychain_profile] if args.keychain_profile else []
    if not credentials:
        for name, flag in [('APPLE_API_KEY_PATH', '--key'), ('APPLE_API_KEY', '--key-id'),
                           ('APPLE_API_ISSUER', '--issuer')]:
            value = os.environ.get(name)
            if not value:
                parser.error('Provide --keychain-profile or the existing Apple API signing environment')
            credentials += [flag, value]
    manifest = json.loads((args.helpers / 'build.json').read_text())
    if not manifest['developerSigned']:
        parser.error('Developer ID signatures are required')
    for relative in manifest['binaries']:
        path = (args.helpers / relative).resolve()
        if not path.is_relative_to(args.helpers.resolve()):
            parser.error('Binary path escaped staging directory')
        subprocess.run(['codesign', '--verify', '--strict', str(path)], check=True)
    args.out.mkdir(parents=True, exist_ok=True)
    archive = args.out / 'opennavo-native-helpers.zip'
    subprocess.run(['ditto', '-c', '-k', '--keepParent', str(args.helpers), str(archive)], check=True)
    result = subprocess.run(['xcrun', 'notarytool', 'submit', str(archive), *credentials,
                             '--wait', '--output-format', 'json'], capture_output=True, text=True)
    (args.out / 'notary-result.json').write_text(result.stdout)
    if result.returncode:
        raise SystemExit(result.stderr)
    response = json.loads(result.stdout)
    if response.get('status') != 'Accepted':
        log = subprocess.run(['xcrun', 'notarytool', 'log', response['id'], *credentials],
                             capture_output=True, text=True, check=True)
        (args.out / 'notary-log.json').write_text(log.stdout)
        raise SystemExit('Notarization was not accepted; see notary-log.json')
    print('Native helper ZIP accepted. Containing application/DMG still needs its own notarization.')


if __name__ == '__main__':
    main()
