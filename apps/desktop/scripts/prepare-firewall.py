#!/usr/bin/env python3
"""Opt-in firewall bundle preparation. Never installs, registers, activates or runs it.

Without both profiles and a Developer ID identity, produce an empty overlay and
skip the entire extension build. Profile material is staged outside the repository.
"""
import argparse
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import stat
import subprocess
import tempfile

DESKTOP = Path(__file__).resolve().parents[1]
REPO = DESKTOP.parents[1]
SOURCE = DESKTOP / 'native/firewall'
TAURI = DESKTOP / 'src-tauri'
NE_KEY = 'com.apple.developer.networking.networkextension'
NE_VALUE = 'content-filter-provider-systemextension'
GROUP_KEY = 'com.apple.security.application-groups'
INSTALL_KEY = 'com.apple.developer.system-extension.install'


def run(command, **kwargs):
    return subprocess.run(command, check=True, **kwargs)


def private_write(path, data):
    if isinstance(data, str):
        data = data.encode()
    path.write_bytes(data)
    path.chmod(0o600)


def bundle_entries(bundle):
    """Inspect only the distributable tree, never its private staging parents."""
    if bundle.is_symlink() or not bundle.is_dir():
        raise ValueError('Expected a real bundle directory')
    root = bundle.resolve()
    entries = []
    for path in [bundle, *bundle.rglob('*')]:
        mode = path.lstat().st_mode
        if stat.S_ISLNK(mode):
            # Framework version links are allowed, but must resolve inside the
            # bundle. Their targets are checked separately during the tree walk.
            try:
                target = path.resolve(strict=True)
            except (OSError, RuntimeError) as error:
                raise ValueError('Broken or cyclic bundle symlink') from error
            if not target.is_relative_to(root):
                raise ValueError('Bundle symlink escapes the distributable tree')
            continue
        if not (stat.S_ISDIR(mode) or stat.S_ISREG(mode)):
            raise ValueError('Unexpected special file in bundle')
        expected = 0o755 if stat.S_ISDIR(mode) or mode & 0o111 else 0o644
        entries.append((path, stat.S_IMODE(mode), expected))
    return entries


def normalize_bundle_permissions(bundle):
    # Validate every path before mutating anything; never follow external links.
    for path, _, expected in bundle_entries(bundle):
        path.chmod(expected)


def verify_bundle_permissions(bundle):
    entries = bundle_entries(bundle)
    for path, mode, expected in entries:
        if mode != expected:
            raise ValueError(f'Bundle permission mismatch: {path.relative_to(bundle)} '
                             f'is {mode:04o}, expected {expected:04o}')
    return len(entries)


def validate_bundle_version(version):
    if not isinstance(version, str) or not re.fullmatch(r'[0-9A-Za-z.-]+', version):
        raise ValueError('CFBundleVersion contains characters outside [0-9A-Za-z.-]')
    return version


def mach_service_name(group, version):
    return group + '.firewall.' + validate_bundle_version(version)


def verify_extension_service(info, group, expected_version):
    version = validate_bundle_version(info.get('CFBundleVersion'))
    app_id = info.get('OpenNavoAppIdentifier')
    if not isinstance(app_id, str) or not re.fullmatch(r'[A-Z0-9]{10}\.' + re.escape(app_id), group):
        raise ValueError('Mach service App Group does not match the host identifier')
    if info.get('CFBundleIdentifier') != app_id + '.firewall' or version != expected_version:
        raise ValueError('Extension identity/build version differs from the app')
    if info.get('NetworkExtension', {}).get('NEMachServiceName') != mach_service_name(group, version):
        raise ValueError('Mach service must use the App Group prefix and CFBundleVersion suffix')


def verify_bundle_services(bundle):
    """Check final signed app metadata without loading or activating its code."""
    host = plistlib.loads((bundle / 'Contents/Info.plist').read_bytes())
    extensions = bundle / 'Contents/Library/SystemExtensions'
    count = 0
    for extension in extensions.glob('*.systemextension'):
        info = plistlib.loads((extension / 'Contents/Info.plist').read_bytes())
        if info.get('OpenNavoAppIdentifier') != host.get('CFBundleIdentifier'):
            raise ValueError('Embedded firewall belongs to another app')
        entitlements = plistlib.loads(run(['codesign', '-d', '--entitlements', ':-', str(extension)], capture_output=True).stdout)
        groups = entitlements.get(GROUP_KEY, [])
        if len(groups) != 1:
            raise ValueError('Expected exactly one firewall App Group')
        verify_extension_service(info, groups[0], host.get('CFBundleVersion'))
        count += 1
    return count


def profile_value(env, name):
    return env.get(name) or env.get(name + '_B64')


def configured(env):
    return bool(profile_value(env, 'OPENNAVO_FIREWALL_APP_PROFILE')
                and profile_value(env, 'OPENNAVO_FIREWALL_EXTENSION_PROFILE')
                and env.get('APPLE_SIGNING_IDENTITY') not in (None, '', '-'))


def validate_profile(profile, app_id, role, now=None):
    now = now or datetime.datetime.now(datetime.timezone.utc)
    expiration = profile.get('ExpirationDate')
    if not isinstance(expiration, datetime.datetime):
        raise ValueError('Profile has no expiration date')
    if expiration.replace(tzinfo=datetime.timezone.utc) <= now:
        raise ValueError('Profile has expired')
    teams = profile.get('TeamIdentifier', [])
    if len(teams) != 1 or not re.fullmatch(r'[A-Z0-9]{10}', teams[0]):
        raise ValueError('Profile must have one valid team identifier')
    team = teams[0]
    entitlements = profile.get('Entitlements', {})
    identifier = entitlements.get('com.apple.application-identifier', entitlements.get('application-identifier'))
    if identifier != f'{team}.{app_id}' or entitlements.get('com.apple.developer.team-identifier') != team:
        raise ValueError('Profile does not match the requested explicit App ID')
    if NE_VALUE not in entitlements.get(NE_KEY, []):
        raise ValueError('Profile lacks the system-extension content-filter provider capability')
    if role == 'app' and entitlements.get(INSTALL_KEY) is not True:
        raise ValueError('App profile lacks System Extension installation capability')
    if entitlements.get('get-task-allow') or entitlements.get('com.apple.security.get-task-allow'):
        raise ValueError('Use Developer ID distribution profiles, not development profiles')
    if not profile.get('ProvisionsAllDevices'):
        raise ValueError('Profile must be a Developer ID profile for all devices')
    if not profile.get('DeveloperCertificates'):
        raise ValueError('Profile has no signing certificates')
    return team


def read_profile(env, name, destination):
    if env.get(name):
        source = Path(env[name]).expanduser().resolve()
        if source.is_relative_to(REPO):
            raise ValueError('Keep real provisioning profiles outside the repository')
        raw = source.read_bytes()
    else:
        raw = base64.b64decode(env[name + '_B64'], validate=True)
    private_write(destination, raw)
    decoded = run(['security', 'cms', '-D', '-i', str(destination)], capture_output=True).stdout
    return plistlib.loads(decoded)


def identity_fingerprint(identity):
    text = run(['security', 'find-identity', '-v', '-p', 'codesigning'], capture_output=True, text=True).stdout
    identities = re.findall(r'\b([A-Fa-f0-9]{40}) "([^"]+)"', text)
    matches = [fingerprint.upper() for fingerprint, label in identities
               if (identity.upper() == fingerprint.upper() or identity == label) and label.startswith('Developer ID Application:')]
    if len(matches) != 1:
        raise ValueError('APPLE_SIGNING_IDENTITY must select one installed Developer ID identity')
    return matches[0]


def entitlements_for(profile, team, app_id, group, app=False):
    result = {
        'com.apple.application-identifier': f'{team}.{app_id}',
        'com.apple.developer.team-identifier': team,
        NE_KEY: [NE_VALUE], GROUP_KEY: [group],
    }
    if app:
        result = {**plistlib.loads((TAURI / 'entitlements.plist').read_bytes()), **result, INSTALL_KEY: True}
    # Do not copy unrelated capabilities or get-task-allow from a profile.
    return result


def build_binary(work, target, release=True):
    triples = ['aarch64-apple-darwin', 'x86_64-apple-darwin'] if target == 'universal-apple-darwin' else [target]
    outputs = []
    for triple in triples:
        arch = 'arm64' if triple.startswith('aarch64') else 'x86_64'
        destination = work / arch
        destination.mkdir(parents=True, exist_ok=True)
        cargo_target = work / 'cargo'
        command = ['cargo', 'build', '--locked', '--manifest-path', str(SOURCE / 'core/Cargo.toml'), '--target', triple]
        if release:
            command.append('--release')
        run(command, cwd=SOURCE / 'core', env={**os.environ, 'CARGO_TARGET_DIR': str(cargo_target), 'MACOSX_DEPLOYMENT_TARGET': '13.0'})
        library = cargo_target / triple / ('release' if release else 'debug') / 'libopennavo_firewall_core.a'
        binary = destination / 'OpenNavoFirewall'
        run(['xcrun', 'clang', '-arch', arch, '-mmacosx-version-min=13.0', '-fobjc-arc', '-fblocks',
             '-Wall', '-Wextra', '-Werror', '-O2', str(SOURCE / 'main.m'), str(SOURCE / 'FilterDataProvider.m'),
             str(SOURCE / 'FWIdentity.m'), str(library), '-framework', 'Foundation', '-framework', 'Security',
             '-framework', 'NetworkExtension', '-framework', 'AppKit', '-lbsm', '-liconv', '-lresolv', '-o', str(binary)])
        outputs.append(binary)
    output = work / 'OpenNavoFirewall'
    if len(outputs) == 1:
        shutil.copy2(outputs[0], output)
    else:
        run(['xcrun', 'lipo', '-create', *map(str, outputs), '-output', str(output)])
    return output


def safe_directory(path):
    # Reject symlinks before resolving; all generated secrets stay private.
    for parent in [path, *path.parents]:
        if parent.is_symlink():
            raise ValueError('Refusing a symlink in firewall staging')
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.stat().st_uid != os.getuid():
        raise ValueError('Firewall staging must be owned by the current user')
    path.chmod(0o700)


def source_digest():
    digest = hashlib.sha256()
    files = [p for p in SOURCE.rglob('*') if p.is_file() and 'target' not in p.relative_to(SOURCE).parts]
    files.append(Path(__file__).resolve())
    for path in sorted(files):
        digest.update(str(path.relative_to(DESKTOP)).encode())
        digest.update(path.read_bytes())
    return digest.hexdigest()


def empty_overlay():
    # Remove only this feature's stale generated directory. It never contains user files.
    stage = DESKTOP / 'native/staged/firewall/SystemExtensions'
    if any(p.is_symlink() for p in [stage, *stage.parents]):
        raise ValueError('Refusing a symlink at the firewall staging directory')
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True)
    return {'bundle': {'macOS': {'entitlements': str(TAURI / 'entitlements.plist'), 'infoPlist': None, 'files': {'Library/SystemExtensions': str(stage)}}}}


def prepare(args, env):
    build_version = getattr(args, 'bundle_version', None)
    build_version = validate_bundle_version(args.version.split('-')[0] if build_version is None else build_version)
    if not configured(env):
        overlay = empty_overlay()
        if args.hook and env.get('OPENNAVO_FIREWALL_CONFIG'):
            previous = json.loads(Path(env['OPENNAVO_FIREWALL_CONFIG']).read_text())
            if previous.get('bundle', {}).get('macOS', {}).get('files', {}).get('embedded.provisionprofile'):
                raise ValueError('Signed overlay remains selected without profiles; regenerate the overlay first')
        print('Firewall not bundled: both Developer ID profiles and signing identity are required; native build skipped.')
        return overlay
    if args.hook:
        config = env.get('OPENNAVO_FIREWALL_CONFIG')
        if not config or not Path(config).is_file():
            raise ValueError('Prepare --config-out first and pass that overlay to tauri build via --config')
        overlay = json.loads(Path(config).read_text())
        app_id = overlay.get('identifier', args.app_id)
        stage = Path(overlay['bundle']['macOS']['files']['Library/SystemExtensions'])
        if stage.resolve().is_relative_to(REPO):
            raise ValueError('Signed firewall staging must be outside the repository')
        manifest = json.loads((stage.parent / 'prepared.json').read_text())
        if manifest['appId'] != app_id or manifest['source'] != source_digest():
            raise ValueError('Prepared firewall is stale; run preparation again')
        if env.get('TAURI_ENV_TARGET_TRIPLE') and manifest['target'] != env['TAURI_ENV_TARGET_TRIPLE']:
            raise ValueError('Prepared firewall architecture differs from the bundle target')
        if env.get('VERSION') and manifest['version'] != env['VERSION']:
            raise ValueError('Prepared firewall version differs from the app')
        bundle = stage / f'{app_id}.firewall.systemextension'
        for name, expected in manifest['files'].items():
            if hashlib.sha256((stage.parent / name).read_bytes()).hexdigest() != expected:
                raise ValueError('Prepared firewall signing inputs changed')
        entitlements = plistlib.loads((stage.parent / 'extension.entitlements.plist').read_bytes())
        info = plistlib.loads((bundle / 'Contents/Info.plist').read_bytes())
        verify_extension_service(info, entitlements[GROUP_KEY][0], manifest['bundleVersion'])
        if overlay['bundle']['macOS'].get('bundleVersion') != manifest['bundleVersion']:
            raise ValueError('Prepared host/extension build versions differ')
        verify_bundle_permissions(stage)
        embedded = Path(overlay['bundle']['macOS']['files']['embedded.provisionprofile'])
        if embedded.is_symlink() or stat.S_IMODE(embedded.stat().st_mode) != 0o644:
            raise ValueError('Embedded app profile must be a readable distribution copy')
        run(['codesign', '--verify', '--deep', '--strict', str(bundle)], capture_output=True)
        print('Prepared firewall bundle retained; no activation or registration.')
        return overlay
    stage_root = Path(env.get('OPENNAVO_FIREWALL_STAGE') or tempfile.mkdtemp(prefix='opennavo-firewall-build-')).absolute()
    if stage_root.is_symlink():
        raise ValueError('Refusing a symlink at the staging root')
    stage_root = stage_root.resolve()
    if stage_root.resolve().is_relative_to(REPO) or stage_root == Path('/'):
        raise ValueError('Signed firewall staging must be outside the repository')
    safe_directory(stage_root)
    work = stage_root / 'opennavo-firewall'
    safe_directory(work)
    # Reject pre-existing symlinks before writing profiles or entitlements.
    if any(p.is_symlink() for p in work.rglob('*')):
        raise ValueError('Refusing symlinked signing inputs')
    main_profile = work / 'app.provisionprofile'
    ext_profile = work / 'extension.provisionprofile'
    app = read_profile(env, 'OPENNAVO_FIREWALL_APP_PROFILE', main_profile)
    extension = read_profile(env, 'OPENNAVO_FIREWALL_EXTENSION_PROFILE', ext_profile)
    ext_id = args.app_id + '.firewall'
    team = validate_profile(app, args.app_id, 'app')
    if validate_profile(extension, ext_id, 'extension') != team:
        raise ValueError('App and extension profiles must belong to the same team')
    fingerprint = identity_fingerprint(env['APPLE_SIGNING_IDENTITY'])
    for profile in (app, extension):
        if fingerprint not in [hashlib.sha1(cert).hexdigest().upper() for cert in profile['DeveloperCertificates']]:
            raise ValueError('Signing identity is not included in both provisioning profiles')
    # macOS team-prefixed groups are self-authenticating; no portal App Group registration is needed.
    group = f'{team}.{args.app_id}'
    binary = build_binary(work / 'build', args.target)
    systems = work / 'SystemExtensions'
    if systems.exists():
        shutil.rmtree(systems)
    bundle = systems / f'{ext_id}.systemextension'
    contents = bundle / 'Contents'
    (contents / 'MacOS').mkdir(parents=True)
    shutil.copy2(binary, contents / 'MacOS/OpenNavoFirewall')
    (contents / 'MacOS/OpenNavoFirewall').chmod(0o755)
    shutil.copy2(ext_profile, contents / 'embedded.provisionprofile')
    shutil.copytree(SOURCE / 'Resources', contents / 'Resources')
    usage = 'Asks which apps may connect to the network, then allows or blocks connections by your rules.'
    info = {
        'CFBundleIdentifier': ext_id, 'CFBundleExecutable': 'OpenNavoFirewall',
        'CFBundleName': 'OpenNavo', 'CFBundleDisplayName': 'OpenNavo', 'CFBundlePackageType': 'SYSX',
        'CFBundleInfoDictionaryVersion': '6.0', 'CFBundleDevelopmentRegion': 'en',
        'CFBundleShortVersionString': args.version, 'CFBundleVersion': build_version,
        'LSMinimumSystemVersion': '13.0', 'NSSystemExtensionUsageDescription': usage,
        'OpenNavoAppIdentifier': args.app_id,
        'NetworkExtension': {'NEMachServiceName': mach_service_name(group, build_version),
                             'NEProviderClasses': {'com.apple.networkextension.filter-data': 'OpenNavoFilterDataProvider'}},
    }
    verify_extension_service(info, group, build_version)
    (contents / 'Info.plist').write_bytes(plistlib.dumps(info))
    ext_entitlements = work / 'extension.entitlements.plist'
    private_write(ext_entitlements, plistlib.dumps(entitlements_for(extension, team, ext_id, group)))
    app_entitlements = work / 'app.entitlements.plist'
    private_write(app_entitlements, plistlib.dumps(entitlements_for(app, team, args.app_id, group, app=True)))
    app_info = work / 'app-info.plist'
    private_write(app_info, plistlib.dumps({'NSSystemExtensionUsageDescription': usage}))
    # copy2 preserves 0600 on private inputs. Only distribution copies become
    # public-readable; the original profiles and staging roots remain private.
    embedded_app_profile = work / 'embedded-app.provisionprofile'
    shutil.copyfile(main_profile, embedded_app_profile)
    embedded_app_profile.chmod(0o644)
    normalize_bundle_permissions(systems)
    run(['codesign', '--force', '--options', 'runtime', '--timestamp', '--sign', fingerprint,
         '--entitlements', str(ext_entitlements), str(bundle)], umask=0o022)
    # Include signature resources created by codesign, even under a 077 caller.
    normalize_bundle_permissions(systems)
    verify_bundle_permissions(systems)
    requirement = f'anchor apple generic and identifier "{ext_id}" and certificate leaf[subject.OU] = "{team}" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists'
    run(['codesign', '--verify', '--deep', '--strict', '-R', '=' + requirement, str(bundle)], capture_output=True)
    tracked = [main_profile, ext_profile, embedded_app_profile, app_entitlements, ext_entitlements, app_info]
    manifest = {'appId': args.app_id, 'target': args.target, 'version': args.version, 'bundleVersion': build_version, 'source': source_digest(),
                'files': {str(p.relative_to(work)): hashlib.sha256(p.read_bytes()).hexdigest() for p in tracked}}
    private_write(work / 'prepared.json', json.dumps(manifest, indent=2) + '\n')
    print('Developer ID firewall extension prepared. Outer app must use the generated overlay and normal Tauri signing/notarization.')
    return {'bundle': {'macOS': {'bundleVersion': build_version, 'entitlements': str(app_entitlements), 'infoPlist': str(app_info),
            'files': {'Library/SystemExtensions': str(systems), 'embedded.provisionprofile': str(embedded_app_profile)}}}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', default=os.environ.get('TAURI_ENV_TARGET_TRIPLE', 'aarch64-apple-darwin'),
                        choices=['aarch64-apple-darwin', 'x86_64-apple-darwin', 'universal-apple-darwin'])
    parser.add_argument('--app-id', default=os.environ.get('OPENNAVO_FIREWALL_APP_ID', 'com.opennavo.desktop'))
    parser.add_argument('--version', default=os.environ.get('VERSION', '0.0.0'))
    parser.add_argument('--bundle-version', default=os.environ.get('OPENNAVO_FIREWALL_BUNDLE_VERSION'),
                        help='Unique CFBundleVersion for both bundles; defaults to the numeric VERSION prefix')
    parser.add_argument('--config-out', type=Path)
    parser.add_argument('--hook', action='store_true')
    parser.add_argument('--compile-only', type=Path, help='Compile/link a plain test executable; no bundle, profiles or signing')
    parser.add_argument('--verify-bundle-permissions', type=Path, nargs='+',
                        help='Read-only check of final App/bundle permissions, including nested code')
    parser.add_argument('--verify-bundle-services', type=Path, nargs='+',
                        help='Read-only check of versioned Mach service names in final Apps')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)+', args.app_id):
        parser.error('Invalid App ID')
    if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[A-Za-z0-9.]+)?', args.version):
        parser.error('Invalid version')
    try:
        if args.verify_bundle_services:
            for bundle in args.verify_bundle_services:
                count = verify_bundle_services(bundle)
                print(f'Versioned firewall services verified: {count} in {bundle.name}')
            return
        if args.verify_bundle_permissions:
            for bundle in args.verify_bundle_permissions:
                count = verify_bundle_permissions(bundle)
                print(f'Bundle permissions verified: {count} entries in {bundle.name}')
            return
        if args.compile_only:
            build_binary(args.compile_only.resolve(), args.target, release=False)
            print('Plain extension executable compiled and linked; never run or registered.')
            return
        overlay = prepare(args, os.environ)
        overlay['identifier'] = args.app_id
        if args.app_id != 'com.opennavo.desktop':
            overlay['productName'] = 'OpenNavo Firewall Test'
            overlay['plugins'] = {'deep-link': {'desktop': {'schemes': ['opennavo-firewall-test']}}, 'updater': {'endpoints': []}}
            overlay['bundle']['createUpdaterArtifacts'] = False
            scope = json.loads((TAURI / 'tauri.conf.json').read_text())['app']['security']['assetProtocol']['scope']
            overlay['app'] = {'security': {'assetProtocol': {'scope': [
                *scope, f'$CACHE/{args.app_id}/icons/**',
            ]}}}
        if args.config_out:
            args.config_out.parent.mkdir(parents=True, exist_ok=True)
            private_write(args.config_out, json.dumps(overlay, indent=2) + '\n')
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        # Never print decoded profiles, certificate data or signing command output.
        parser.exit(1, f'Firewall preparation failed: {type(error).__name__}: '
                    f'{error if isinstance(error, ValueError) else "see preceding build diagnostics"}\n')


if __name__ == '__main__':
    main()
