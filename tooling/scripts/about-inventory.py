"""Generate versioned dependency evidence, verbatim notices, and distribution bundles.

Run with apps/mcp/.venv/bin/python -I tooling/scripts/about-inventory.py. Exact-version registry
metadata fills missing cross-platform dependencies; committed evidence is reused on
subsequent runs. No database, Homebrew, or AI service is accessed.
"""
import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import hashlib
import importlib.metadata
import io
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tomllib
import urllib.parse
import urllib.request
import zipfile
import yaml

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'docs/legal/third-party'

def run(args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True)

def objects(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        yield item
        raw = raw.lstrip()[end:]

def fetch(url):
    request = urllib.request.Request(url, headers={'User-Agent': 'OpenNavo-license-inventory/1'})
    with urllib.request.urlopen(request, timeout=90) as response:
        return response.read()

def license_name(text):
    lower = ' '.join(text.lower().split())
    if 'permission to use, copy, modify, and/or distribute' in lower or 'permission to use, copy, modify, and distribute' in lower:
        return 'ISC'
    if 'permission is hereby granted, free of charge' in lower:
        return 'MIT'
    if 'apache license' in lower and 'version 2.0' in lower:
        return 'Apache-2.0'
    if 'redistribution and use in source and binary forms' in lower:
        return 'BSD-3-Clause' if 'neither the name' in lower else 'BSD-2-Clause'
    if 'mozilla public license' in lower and '2.0' in lower:
        return 'MPL-2.0'
    if 'isc license' in lower:
        return 'ISC'
    if 'gnu general public license' in lower and 'version 3, 29 june 2007' in lower:
        return 'GPL-3.0-only'
    if "this software is provided 'as-is'" in lower and 'altered source versions must be plainly marked' in lower:
        return 'Zlib'
    if 'cc0 1.0 universal' in lower or 'creative commons zero' in lower or 'creativecommons.org/publicdomain/zero/1.0' in lower:
        return 'CC0-1.0'
    if 'this is free and unencumbered software released into the public domain' in lower:
        return 'Unlicense'
    return 'NOASSERTION'

def is_notice(name):
    return bool(re.match(r'(?i)^(licen[cs]e|copying|notice|copyright|authors)([.\-_]|$)', Path(name).name))

def texts_in(directory):
    directory = Path(directory)
    if not directory.exists():
        return []
    # Include bundled upstream notices without walking nested installed packages.
    return [(str(p.relative_to(directory)), p.read_bytes()) for p in sorted(directory.rglob('*'))
            if p.is_file() and is_notice(p.name) and 'node_modules' not in p.relative_to(directory).parts]

def save_texts(texts):
    result = []
    for source, raw in texts:
        digest = hashlib.sha256(raw).hexdigest()
        path = OUT / 'licenses' / (digest + '.txt')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)  # Preserve upstream bytes, including copyright notices.
        result.append({'source': source, 'path': 'licenses/' + digest + '.txt', 'sha256': digest})
    return sorted(result, key=lambda x: (x['source'], x['sha256']))

def normalized(value):
    if isinstance(value, dict):
        value = value.get('type')
    if isinstance(value, list):
        value = ' OR '.join(normalized(x) for x in value)
    return value if value and value.lower() not in ('unknown', 'noassertion') else 'NOASSERTION'

def server_go_targets(dockerfile):
    # Only the application's build stage uses apps/server/go.mod. External
    # monitor/helper stages have their own modules and ship separate notices.
    stages = re.split(r'(?im)^FROM\s+', dockerfile)
    for stage in stages[1:]:
        header, _, body = stage.partition('\n')
        if re.search(r'(?i)\sAS\s+build\s*$', header):
            targets = sorted(set(re.findall(r'\./cmd/[a-z0-9-]+', body)))
            if targets:
                return targets
    raise ValueError('Application Go build stage has no command targets')

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--refresh', action='store_true', help='Refresh cached evidence from exact-version sources')
    parser.add_argument('--about-output', type=Path, default=ROOT / 'apps/server/seeds/about.json')
    args = parser.parse_args()
    rust_version = run(['rustc', '--version'], ROOT / 'apps/desktop/src-tauri').strip().split()[1]
    rust_pin = tomllib.loads((ROOT / 'apps/desktop/src-tauri/rust-toolchain.toml').read_text())['toolchain']['channel']
    if rust_version != rust_pin:
        raise SystemExit(f'Installed Rust {rust_version} differs from pinned notice toolchain {rust_pin}')
    OUT.mkdir(parents=True, exist_ok=True)
    previous = {}
    if (OUT / 'inventory.json').exists() and not args.refresh:
        previous = {(m['ecosystem'], m['name'], m['version']): m for m in json.loads((OUT / 'inventory.json').read_text())['modules']}
    modules = []

    def cached(eco, name, version, scope):
        item = previous.get((eco, name, version))
        if item and all((OUT / t['path']).exists() and hashlib.sha256((OUT / t['path']).read_bytes()).hexdigest() == t['sha256'] for t in item['licenseFiles']):
            return {**item, 'scope': scope}
        return None

    def record(eco, name, version, license, url, scope, evidence, texts):
        license = normalized(license)
        if license == 'NOASSERTION':
            license = license_name('\n'.join(raw.decode(errors='replace') for _, raw in texts))
        return dict(ecosystem=eco, name=name, version=version, license=license,
                    url=url, scope=scope, evidence=evidence, licenseFiles=save_texts(texts))

    lock = yaml.safe_load((ROOT / 'pnpm-lock.yaml').read_text())
    local_npm = {}
    for entries in json.loads(run(['pnpm', 'licenses', 'list', '--json'])).values():
        for item in entries:
            for version, path in zip(item['versions'], item['paths']):
                local_npm[item['name'], version] = Path(path)
    prod = set()
    def walk_npm(node):
        for group in ('dependencies', 'optionalDependencies'):
            for name, value in node.get(group, {}).items():
                prod.add((value.get('name', name), value.get('version', '').split('(')[0]))
                walk_npm(value)
    for project in json.loads(run(['pnpm', 'list', '-r', '--prod', '--depth', 'Infinity', '--json'])):
        walk_npm(project)

    def npm_record(key):
        name, version = key.rsplit('@', 1)
        scope = 'runtime-asset' if name in ('@iconify-json/lucide', '@fontsource-variable/inter') else ('runtime-candidate' if (name, version) in prod else 'development')
        old = cached('npm', name, version, scope)
        if old and old['evidence'].get('lockIntegrity') == lock['packages'][key].get('resolution', {}).get('integrity'):
            return old
        url = 'https://registry.npmjs.org/' + urllib.parse.quote(name, safe='') + '/' + version
        directory = local_npm.get((name, version))
        texts = texts_in(directory) if directory else []
        if directory and (directory / 'package.json').exists():
            metadata = json.loads((directory / 'package.json').read_text())
            evidence = {'source': 'npm package.json', 'url': url, 'lockIntegrity': lock['packages'][key].get('resolution', {}).get('integrity')}
        else:
            metadata = json.loads(fetch(url))
            archive = fetch(metadata['dist']['tarball'])
            integrity = lock['packages'][key].get('resolution', {}).get('integrity')
            if integrity:
                algorithm, expected = integrity.split('-', 1)
                assert base64.b64encode(hashlib.new(algorithm, archive).digest()).decode() == expected, key
            with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as tar:
                texts = [(m.name, tar.extractfile(m).read()) for m in tar if m.isfile() and is_notice(m.name)]
            evidence = {'source': 'npm exact-version registry and integrity-verified tarball', 'url': url, 'lockIntegrity': integrity}
        return record('npm', name, version, metadata.get('license') or metadata.get('licenses'),
                      'https://www.npmjs.com/package/' + name + '/v/' + version, scope, evidence, texts)
    with ThreadPoolExecutor(max_workers=12) as pool:
        modules.extend(pool.map(npm_record, lock['packages']))
    print('npm evidence collected', flush=True)

    # Query actual command imports, not all modules retained for tools/tests.
    go_targets = server_go_targets((ROOT / 'apps/server/Dockerfile').read_text())
    runtime_go = {p['Module']['Path'] for p in objects(run(['go', 'list', '-deps', '-json', *go_targets], ROOT / 'apps/server')) if p.get('Module')}
    for item in objects(run(['go', 'list', '-m', '-json', 'all'], ROOT / 'apps/server')):
        if item.get('Main'):
            continue
        name, version = item['Path'], item['Version']
        scope = 'runtime' if name in runtime_go else 'development'
        old = cached('Go', name, version, scope)
        if old:
            modules.append(old)
            continue
        download = json.loads(run(['go', 'mod', 'download', '-json', name + '@' + version], ROOT / 'apps/server'))
        texts = texts_in(download['Dir'])
        # A module can vendor several license texts; identify its root license only.
        root_texts = [raw for source, raw in texts if '/' not in source and re.match(r'(?i)^(license|copying)', source)]
        modules.append(record('Go', name, version, license_name('\n'.join(t.decode(errors='replace') for t in root_texts)),
            'https://pkg.go.dev/' + name + '@' + version, scope,
            {'source': 'Go module download verified by go.sum / checksum database', 'sum': download.get('Sum')}, texts))
    print('Go evidence collected', flush=True)

    cargo = json.loads(run(['cargo', 'metadata', '--locked', '--format-version', '1', '--manifest-path', 'apps/desktop/src-tauri/Cargo.toml']))
    nodes = {n['id']: n for n in cargo['resolve']['nodes']}
    runtime_rust = set()
    def walk_cargo(key):
        if key in runtime_rust:
            return
        runtime_rust.add(key)
        for dep in nodes[key]['deps']:
            if any(k['kind'] is None for k in dep['dep_kinds']):
                walk_cargo(dep['pkg'])
    walk_cargo(cargo['resolve']['root'])
    for item in cargo['packages']:
        if not item['source']:
            continue
        scope = 'runtime-candidate' if item['id'] in runtime_rust else 'development'
        old = cached('Rust', item['name'], item['version'], scope)
        if old:
            modules.append(old)
            continue
        directory = Path(item['manifest_path']).parent
        texts = texts_in(directory)
        if item.get('license_file'):
            path = directory / item['license_file']
            if path.exists() and not any(source == item['license_file'] for source, _ in texts):
                texts.append((item['license_file'], path.read_bytes()))
        modules.append(record('Rust', item['name'], item['version'], item['license'],
            f"https://crates.io/crates/{item['name']}/{item['version']}", scope,
            {'source': 'Cargo.lock and downloaded crate manifest', 'registry': item['source']}, texts))
    print('Rust evidence collected', flush=True)

    uvlock = tomllib.loads((ROOT / 'apps/mcp/uv.lock').read_text())
    py_packages = {p['name']: p for p in uvlock['package']}
    requirements = run(['uv', 'export', '--frozen', '--no-dev', '--no-emit-project', '--format', 'requirements-txt'], ROOT / 'apps/mcp')
    runtime_py = {re.sub(r'[-_.]+', '-', line.split('==')[0]).lower() for line in requirements.splitlines() if '==' in line and not line.startswith((' ', '#'))}
    def py_record(item):
        name, version = item['name'], item['version']
        scope = 'runtime-candidate' if name in runtime_py else 'development'
        old = cached('Python', name, version, scope)
        if old:
            return old
        url = f'https://pypi.org/pypi/{name}/{version}/json'
        metadata = json.loads(fetch(url))
        info = metadata['info']
        license = info.get('license_expression') or info.get('license')
        if not license or len(license) > 100:
            license = license_name(license or '')
        texts = []
        try:
            dist = importlib.metadata.distribution(name)
            if dist.version == version:
                texts = [(str(p), Path(dist.locate_file(p)).read_bytes()) for p in dist.files or [] if is_notice(str(p))]
        except importlib.metadata.PackageNotFoundError:
            pass
        if not texts:
            candidates = sorted(metadata['urls'], key=lambda a: (a['packagetype'] != 'bdist_wheel', a['filename']))
            artifact = candidates[0]
            raw = fetch(artifact['url'])
            assert hashlib.sha256(raw).hexdigest() == artifact['digests']['sha256']
            if artifact['filename'].endswith('.whl'):
                with zipfile.ZipFile(io.BytesIO(raw)) as archive:
                    texts = [(n, archive.read(n)) for n in archive.namelist() if is_notice(n) and not n.endswith('/')]
            else:
                with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
                    texts = [(m.name, archive.extractfile(m).read()) for m in archive if m.isfile() and is_notice(m.name)]
        return record('Python', name, version, license, f'https://pypi.org/project/{name}/{version}/', scope,
                      {'source': 'PyPI exact-version metadata and distribution license files', 'url': url}, texts)
    with ThreadPoolExecutor(max_workers=8) as pool:
        modules.extend(pool.map(py_record, [p for p in uvlock['package'] if 'registry' in p.get('source', {})]))
    print('Python evidence collected', flush=True)

    # Independently installed services are not linked application dependencies.
    for name, version, license, url in [
        ('Homebrew', 'external', 'BSD-2-Clause', 'https://github.com/Homebrew/brew'),
        ('Homebrew Cask', 'external', 'BSD-2-Clause', 'https://github.com/Homebrew/homebrew-cask'),
        ('PostgreSQL', '18', 'PostgreSQL', 'https://www.postgresql.org/about/licence/'),
        ('Redis', '8', 'AGPL-3.0-only OR RSALv2 OR SSPL-1.0', 'https://github.com/redis/redis'),
        ('MinIO', 'RELEASE.2025-10-15T17-29-55Z', 'AGPL-3.0-only', 'https://github.com/minio/minio'),
        ('Caddy', '2.11.6', 'Apache-2.0', 'https://github.com/caddyserver/caddy'),
    ]:
        modules.append(record('Platform', name, version, license, url, 'external', {'source': 'Separately installed upstream; see its distribution notices'}, []))
    modules.append(record('Platform', 'soybean-admin', '2.2.0', 'MIT', 'https://github.com/soybeanjs/soybean-admin/tree/v2.2.0',
                          'runtime', {'source': 'Retained upstream template license'}, [('LICENSE', (ROOT / 'apps/admin/LICENSE').read_bytes())]))
    # Statically linked standard libraries are not represented in package locks.
    goroot = Path(run(['go', 'env', 'GOROOT'], ROOT / 'apps/server').strip())
    go_version = run(['go', 'env', 'GOVERSION'], ROOT / 'apps/server').strip()
    go_license = next(path for path in (goroot / 'LICENSE', goroot.parent / 'LICENSE') if path.is_file())
    modules.append(record('Platform', 'Go standard library', go_version, 'BSD-3-Clause',
        'https://go.dev/LICENSE', 'runtime', {'source': 'Installed Go toolchain LICENSE', 'toolchain': go_version},
        [('LICENSE', go_license.read_bytes())]))
    rustroot = Path(run(['rustc', '--print', 'sysroot'], ROOT / 'apps/desktop/src-tauri').strip()) / 'share/doc/rust'
    rust_texts = [('COPYRIGHT-library.html', (rustroot / 'COPYRIGHT-library.html').read_bytes())]
    rust_texts += [(str(path.relative_to(rustroot)), path.read_bytes()) for path in sorted((rustroot / 'licenses').glob('*.txt'))]
    modules.append(record('Platform', 'Rust standard library', rust_version, 'MIT OR Apache-2.0',
        'https://www.rust-lang.org/policies/licenses', 'runtime', {'source': 'Installed Rust standard-library copyright report and license texts', 'toolchain': rust_version}, rust_texts))
    # Source ports and separately built helpers have no package-manager lock.
    # Their own source locks and verbatim licenses are committed review inputs.
    native_manifest = ROOT / 'docs/legal/native/manifest.json'
    native_records = json.loads(native_manifest.read_text())['modules']
    for item in native_records:
        source = ROOT / item['source']
        modules.append(record('Native', item['name'], item['version'], item['license'],
            item['url'], item['scope'], {'source': item['source'], 'integration': item['kind'],
                'sourceLockSha256': hashlib.sha256(source.read_bytes()).hexdigest(),
                **({'products': item['products']} if 'products' in item else {})},
            [(path, (ROOT / path).read_bytes()) for path in item['licensePaths']]))
    modules.sort(key=lambda m: (m['ecosystem'], m['name'].lower(), m['version']))
    inventory = {'formatVersion': 1, 'lockDigests': {path: hashlib.sha256((ROOT / path).read_bytes()).hexdigest() for path in ['pnpm-lock.yaml', 'apps/server/go.mod', 'apps/server/go.sum', 'apps/mcp/uv.lock', 'apps/desktop/src-tauri/Cargo.lock', 'apps/desktop/native/firewall/core/Cargo.lock', 'docs/legal/native/manifest.json', 'apps/desktop/native/store/upstream.json', 'apps/desktop/native/clamav/sources.lock.json']}, 'scopeNote': 'runtime-candidate includes platform-conditional and bundled/build-time dependencies; conservative notices, not proof of runtime reachability', 'modules': modules}
    for item in native_records:
        for path in [item['source'], *item['licensePaths']]:
            inventory['lockDigests'][path] = hashlib.sha256((ROOT / path).read_bytes()).hexdigest()
    write_json(OUT / 'inventory.json', inventory)
    shutil.copyfile(ROOT / 'LICENSE', OUT / 'LICENSE')
    shutil.copyfile(ROOT / 'NOTICE', OUT / 'NOTICE')
    (OUT / 'README.md').write_text('# Generated third-party notices\n\nSee ../THIRD_PARTY.md for provenance, scope, regeneration, and redistribution requirements.\n')
    about = json.loads((ROOT / 'apps/server/seeds/about.json').read_text())
    about['modules'] = [{k: m[k] for k in ('ecosystem', 'name', 'version', 'license', 'url')} for m in modules]
    write_json(args.about_output, about)
    # Context-local copies make both image and wheel builds self-contained.
    for destination, ecos in [(ROOT / 'apps/server/third-party', {'Go'}), (ROOT / 'apps/mcp/third-party', {'Python'}),
                               (ROOT / 'apps/desktop/src-tauri/resources/third-party', {'npm', 'Rust', 'Platform', 'Native'})]:
        selected = [m for m in modules if (m['ecosystem'] in ecos or ('Go' in ecos and (m['name'] == 'Go standard library' or (m['ecosystem'] == 'Native' and 'server' in m['evidence'].get('products', []))))) and m['scope'] not in ('external', 'development') and not ('Rust' in ecos and (m['name'] == 'Go standard library' or (m['ecosystem'] == 'Native' and m['evidence'].get('products') and 'desktop' not in m['evidence']['products'])))]
        bundle(destination, selected, inventory)
    referenced = {f['path'] for m in modules for f in m['licenseFiles']}
    for stale in (OUT / 'licenses').glob('*.txt'):
        if str(stale.relative_to(OUT)) not in referenced:
            stale.unlink()
    unknown = [f"{m['ecosystem']}:{m['name']}@{m['version']}" for m in modules if m['license'] == 'NOASSERTION']
    print(json.dumps({'modules': len(modules), 'unverified': unknown, 'missingTexts': sum(not m['licenseFiles'] and m['scope'] != 'external' for m in modules)}, indent=2))
    if unknown:
        raise SystemExit(1)

def write_json(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')

def bundle(destination, selected, inventory):
    destination.mkdir(parents=True, exist_ok=True)
    expected = {f['path'] for item in selected for f in item['licenseFiles']}
    for stale in (destination / 'licenses').glob('*.txt'):
        if str(stale.relative_to(destination)) not in expected:
            stale.unlink()
    for name in ('LICENSE', 'NOTICE'):
        shutil.copyfile(ROOT / name, destination / name)
    write_json(destination / 'inventory.json', {**inventory, 'modules': selected})
    for item in selected:
        for notice in item['licenseFiles']:
            target = destination / notice['path']
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(OUT / notice['path'], target)

if __name__ == '__main__':
    main()
