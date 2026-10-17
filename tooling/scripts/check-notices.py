"""Offline gate for inventory freshness, complete bundles, and toolchain identity."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tomllib

ROOT = Path(__file__).resolve().parents[2]
BUNDLES = ['docs/legal/third-party', 'apps/server/third-party', 'apps/mcp/third-party',
           'apps/desktop/src-tauri/resources/third-party']


def require(condition, message):
    if not condition:
        raise ValueError(message)


def index_modules(modules, label):
    indexed = {}
    for module in modules:
        key = tuple(module[field] for field in ('ecosystem', 'name', 'version'))
        require(key not in indexed, f'Duplicate dependency record in {label}: {key}')
        indexed[key] = module
    return indexed


def selected(module, folder):
    if folder == 'docs/legal/third-party':
        return True
    if module['scope'] in ('external', 'development'):
        return False
    if folder == 'apps/server/third-party':
        return (module['ecosystem'] == 'Go' or module['name'] == 'Go standard library' or
                (module['ecosystem'] == 'Native' and 'server' in module['evidence'].get('products', [])))
    if folder == 'apps/mcp/third-party':
        return module['ecosystem'] == 'Python'
    if module['ecosystem'] == 'Native' and module['evidence'].get('products'):
        return 'desktop' in module['evidence']['products']
    return module['ecosystem'] in ('npm', 'Rust', 'Platform', 'Native') and module['name'] != 'Go standard library'


def check_toolchain(canonical, check_compiler):
    pin = tomllib.loads((ROOT / 'apps/desktop/src-tauri/rust-toolchain.toml').read_text())['toolchain']['channel']
    require(re.fullmatch(r'\d+\.\d+\.\d+', pin), 'Rust toolchain must be an exact release, not a moving channel')
    records = [m for m in canonical['modules']
               if m['ecosystem'] == 'Platform' and m['name'] == 'Rust standard library']
    require(len(records) == 1, 'Expected exactly one Rust standard-library notice record')
    record = records[0]
    require(record['version'] == pin and record['evidence'].get('toolchain') == pin,
            'Rust toolchain pin does not match standard-library notice evidence; regenerate notices')
    for name in ('ci-frontend.yml', 'release-desktop.yml'):
        workflow = (ROOT / '.github/workflows' / name).read_text()
        installers = re.findall(r'^\s*-\s*uses:\s*dtolnay/rust-toolchain@([^\s#]+)', workflow, re.MULTILINE)
        require(installers == [pin], f'Workflow Rust toolchain differs from the notice pin: {name}')
    if check_compiler:
        version = subprocess.check_output(['rustc', '--version'], cwd=ROOT / 'apps/desktop/src-tauri', text=True).split()[1]
        require(version == pin, f'Actual Rust compiler {version} differs from notice toolchain {pin}')


def check(check_compiler=False):
    canonical = json.loads((ROOT / 'docs/legal/third-party/inventory.json').read_text())
    canonical_records = index_modules(canonical['modules'], 'canonical inventory')
    require(canonical.get('lockDigests'), 'Regenerate inventory with lock-file fingerprints')
    for name, digest in canonical['lockDigests'].items():
        require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest,
                f'Stale dependency inventory: {name}')
    check_toolchain(canonical, check_compiler)
    for folder in BUNDLES:
        root = ROOT / folder
        inventory = json.loads((root / 'inventory.json').read_text())
        require(inventory['lockDigests'] == canonical['lockDigests'], f'Stale bundle: {folder}')
        expected = {key: module for key, module in canonical_records.items() if selected(module, folder)}
        actual = index_modules(inventory['modules'], folder)
        require(actual.keys() == expected.keys(),
                f'Incorrect dependency selection in {folder}: '
                f'missing={sorted(expected.keys() - actual.keys())}, '
                f'unexpected={sorted(actual.keys() - expected.keys())}')
        for key, module in actual.items():
            require(module == expected[key], f'Dependency record differs from canonical inventory in {folder}: {key}')
        for name in ['LICENSE', 'NOTICE']:
            require((root / name).read_bytes() == (ROOT / name).read_bytes(), f'Stale project notice: {folder}/{name}')
        for module in inventory['modules']:
            require(module['license'] not in ('NOASSERTION', 'Unknown', ''), f'Unknown license: {module["name"]}')
            if module['scope'] == 'external':
                continue
            require(module['licenseFiles'], f'Missing license evidence: {module["name"]}')
            for notice in module['licenseFiles']:
                target = root / notice['path']
                require(target.resolve().is_relative_to(root.resolve()), 'Notice path escapes bundle')
                require(hashlib.sha256(target.read_bytes()).hexdigest() == notice['sha256'], f'Altered license text: {target}')
    print('Dependency selection, fingerprints, notice hashes, and toolchain identity: passed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check-rust-toolchain', action='store_true',
                        help='Also verify the installed rustc used to build the artifact')
    args = parser.parse_args()
    try:
        check(args.check_rust_toolchain)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'Notice validation failed: {error}\n')


if __name__ == '__main__':
    main()
