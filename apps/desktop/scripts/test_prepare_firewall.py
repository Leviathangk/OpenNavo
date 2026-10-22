"""Offline build-boundary tests; no signing identity, profile, or Apple account access."""
import argparse
import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('firewall_prepare', Path(__file__).with_name('prepare-firewall.py'))
fw = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fw)
TEAM = 'EXAMPLE123'
APP = 'org.example.firewalltest'


def profile(app_id=APP, host=True):
    return {'ExpirationDate': datetime.datetime(2099, 1, 1), 'TeamIdentifier': [TEAM],
            'ProvisionsAllDevices': True, 'DeveloperCertificates': [b'public-test-fixture'],
            'Entitlements': {'com.apple.application-identifier': f'{TEAM}.{app_id}',
                             'com.apple.developer.team-identifier': TEAM,
                             fw.NE_KEY: [fw.NE_VALUE], **({fw.INSTALL_KEY: True} if host else {})}}


class PreparationTests(unittest.TestCase):
    @unittest.skipUnless(sys.platform == 'darwin', 'macOS identity APIs')
    def test_own_signing_identity_is_cached_without_caching_api_failures(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-own-team-') as folder:
            binary = Path(folder) / 'own-team-test'
            subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/own_team.m'), '-framework', 'Foundation',
                            '-framework', 'Security', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)
            subprocess.run([str(binary), '--unsigned'], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS SystemExtensions APIs')
    def test_native_missing_service_detection_without_system_operations(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-missing-') as folder:
            binary = Path(folder) / 'missing-test'
            subprocess.run(['xcrun', 'clang', '-mmacosx-version-min=13.0', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/service_missing.m'), str(fw.SOURCE / 'FWIdentity.m'),
                            '-framework', 'Foundation', '-framework', 'Security', '-framework', 'AppKit',
                            '-framework', 'NetworkExtension', '-framework', 'SystemExtensions', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS identity APIs')
    def test_authorization_permission_diagnostics(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-authorization-') as folder:
            binary = Path(folder) / 'authorization-test'
            subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/authorization.m'), '-framework', 'Foundation',
                            '-framework', 'Security', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS identity APIs')
    def test_native_baseline_and_external_helper_ownership(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-ownership-') as folder:
            binary = Path(folder) / 'ownership-test'
            subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/ownership.m'), '-framework', 'Foundation',
                            '-framework', 'Security', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS SystemExtensions APIs')
    def test_host_extension_replacement_state(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-host-') as folder:
            binary = Path(folder) / 'host-test'
            subprocess.run(['xcrun', 'clang', '-mmacosx-version-min=13.0', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/host_state.m'), str(fw.SOURCE / 'FWIdentity.m'),
                            '-framework', 'Foundation', '-framework', 'Security', '-framework', 'AppKit',
                            '-framework', 'NetworkExtension', '-framework', 'SystemExtensions', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS NetworkExtension APIs')
    def test_expired_flow_burst_skips_slow_identity_work(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-flow-') as folder:
            binary = Path(folder) / 'flow-test'
            subprocess.run(['xcrun', 'clang', '-mmacosx-version-min=13.0', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/flow_queue.m'), str(fw.SOURCE / 'FWIdentity.m'),
                            '-framework', 'Foundation', '-framework', 'Security', '-framework', 'AppKit',
                            '-framework', 'NetworkExtension', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS identity APIs')
    def test_native_identity_and_lazy_process_context(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-identity-') as folder:
            binary = Path(folder) / 'identity-test'
            subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/identity.m'), '-framework', 'Foundation',
                            '-framework', 'Security', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS identity APIs')
    def test_live_multi_architecture_apple_identity(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-live-identity-') as folder:
            binary = Path(folder) / 'live-identity-test'
            subprocess.run(['xcrun', 'clang', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/identity_live.m'), str(fw.SOURCE / 'FWIdentity.m'),
                            '-framework', 'Foundation', '-framework', 'Security', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS NetworkExtension APIs')
    def test_offline_flows_reach_policy_before_watchdog_release(self):
        with tempfile.TemporaryDirectory(prefix='opennavo-firewall-offline-') as folder:
            binary = Path(folder) / 'offline-policy-test'
            subprocess.run(['xcrun', 'clang', '-mmacosx-version-min=13.0', '-fobjc-arc', '-fblocks', '-Wall', '-Wextra', '-Werror',
                            str(fw.SOURCE / 'tests/offline_policy.m'), str(fw.SOURCE / 'FWIdentity.m'),
                            '-framework', 'Foundation', '-framework', 'Security', '-framework', 'AppKit',
                            '-framework', 'NetworkExtension', '-lbsm', '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True, timeout=15)

    def args(self, **overrides):
        return argparse.Namespace(**{'hook': False, 'app_id': APP, 'target': 'aarch64-apple-darwin', 'version': '1.2.3-beta.1', **overrides})

    def test_each_missing_secret_skips_build_and_clears_stale_extension(self):
        env = {'APPLE_SIGNING_IDENTITY': 'Developer ID Application: Example',
               'OPENNAVO_FIREWALL_APP_PROFILE_B64': 'fixture', 'OPENNAVO_FIREWALL_EXTENSION_PROFILE_B64': 'fixture'}
        with tempfile.TemporaryDirectory() as folder, patch.object(fw, 'DESKTOP', Path(folder).resolve()), patch.object(fw, 'build_binary') as build:
            for missing in env:
                stale = Path(folder) / 'native/staged/firewall/SystemExtensions/Old.systemextension'
                stale.mkdir(parents=True)
                overlay = fw.prepare(self.args(), {k: v for k, v in env.items() if k != missing})
                self.assertFalse(stale.exists())
                self.assertNotIn('embedded.provisionprofile', overlay['bundle']['macOS']['files'])
            build.assert_not_called()

    def test_profile_validation_rejects_wrong_identity_capability_and_distribution(self):
        self.assertEqual(fw.validate_profile(profile(), APP, 'app'), TEAM)
        cases = [('ExpirationDate', datetime.datetime(2000, 1, 1)), ('ProvisionsAllDevices', False), ('DeveloperCertificates', [])]
        for key, value in cases:
            item = profile(); item[key] = value
            with self.assertRaises(ValueError): fw.validate_profile(item, APP, 'app')
        for key, value in [(fw.NE_KEY, []), (fw.INSTALL_KEY, False), ('get-task-allow', True),
                           ('com.apple.application-identifier', TEAM + '.*'), ('com.apple.developer.team-identifier', 'DIFFERENT')]:
            item = profile(); item['Entitlements'][key] = value
            with self.assertRaises(ValueError): fw.validate_profile(item, APP, 'app')

    def test_entitlements_are_minimal_and_group_is_injected_for_both_targets(self):
        p = profile(); p['Entitlements']['unrelated'] = True
        ext = fw.entitlements_for(p, TEAM, APP + '.firewall', TEAM + '.' + APP)
        app = fw.entitlements_for(p, TEAM, APP, TEAM + '.' + APP, app=True)
        self.assertEqual(ext[fw.GROUP_KEY], app[fw.GROUP_KEY])
        self.assertNotIn(fw.INSTALL_KEY, ext)
        self.assertNotIn('unrelated', app)
        self.assertEqual(app[fw.INSTALL_KEY], True)
        self.assertEqual(app[fw.NE_KEY], [fw.NE_VALUE])

    def test_versioned_services_are_distinct_and_reject_unsafe_versions(self):
        group = TEAM + '.' + APP
        self.assertNotEqual(fw.mach_service_name(group, '0.0.3'), fw.mach_service_name(group, '0.0.4'))
        for version in ['0.0.4', '1.2.3b2', 'A-9.z']:
            self.assertEqual(fw.mach_service_name(group, version), group + '.firewall.' + version)
        for version in ['', '1/2', '1_2', '1+2', '1 2', '1\n', '１.２.３', None]:
            with self.subTest(version=version), self.assertRaises(ValueError):
                fw.mach_service_name(group, version)
        with patch.object(fw, 'build_binary') as build, patch.object(fw, 'read_profile') as read:
            with self.assertRaises(ValueError): fw.prepare(self.args(bundle_version='bad/version'), {})
            build.assert_not_called(); read.assert_not_called()

    def test_final_service_verification_rejects_old_names_prefixes_and_version_mismatch(self):
        group = TEAM + '.' + APP
        with tempfile.TemporaryDirectory() as folder:
            app = Path(folder) / 'Test.app'
            extension = app / 'Contents/Library/SystemExtensions' / (APP + '.firewall.systemextension')
            (extension / 'Contents').mkdir(parents=True)
            (app / 'Contents/Info.plist').write_bytes(plistlib.dumps({'CFBundleIdentifier': APP, 'CFBundleVersion': '0.0.4'}))
            info = {'CFBundleIdentifier': APP + '.firewall', 'OpenNavoAppIdentifier': APP, 'CFBundleVersion': '0.0.4',
                    'NetworkExtension': {'NEMachServiceName': group + '.firewall.0.0.4'}}
            path = extension / 'Contents/Info.plist'
            entitlements = subprocess.CompletedProcess([], 0, plistlib.dumps({fw.GROUP_KEY: [group]}))
            with patch.object(fw, 'run', return_value=entitlements):
                path.write_bytes(plistlib.dumps(info)); self.assertEqual(fw.verify_bundle_services(app), 1)
                for name in [group + '.firewall', group + '.firewall.0.0.3', 'OTHER12345.' + APP + '.firewall.0.0.4']:
                    info['NetworkExtension']['NEMachServiceName'] = name
                    path.write_bytes(plistlib.dumps(info))
                    with self.assertRaises(ValueError): fw.verify_bundle_services(app)
                info['CFBundleVersion'] = '0.0.3'; info['NetworkExtension']['NEMachServiceName'] = group + '.firewall.0.0.3'
                path.write_bytes(plistlib.dumps(info))
                with self.assertRaises(ValueError): fw.verify_bundle_services(app)

    def test_signed_branch_layout_and_hook_integrity_without_real_signing(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            binary = root / 'binary'; binary.write_bytes(b'not-executable-test-fixture'); binary.chmod(0o700)
            env = {'APPLE_SIGNING_IDENTITY': 'fixture', 'OPENNAVO_FIREWALL_APP_PROFILE_B64': 'fixture',
                   'OPENNAVO_FIREWALL_EXTENSION_PROFILE_B64': 'fixture', 'OPENNAVO_FIREWALL_STAGE': str(root)}
            def read(env, name, destination):
                fw.private_write(destination, b'public-test-profile-fixture')
                return profile(APP, True) if name.endswith('APP_PROFILE') else profile(APP + '.firewall', False)
            def signing(command, **kwargs):
                if '--sign' in command:
                    self.assertEqual(kwargs['umask'], 0o022)
                    seal = Path(command[-1]) / 'Contents/_CodeSignature'
                    seal.mkdir()
                    (seal / 'CodeResources').write_bytes(b'fixture seal')
                    # Also cover a signing tool that creates private resources.
                    self.assertEqual(seal.stat().st_mode & 0o777, 0o700)
            with patch.object(fw, 'read_profile', side_effect=read), patch.object(fw, 'identity_fingerprint', return_value=hashlib.sha1(b'public-test-fixture').hexdigest().upper()), patch.object(fw, 'build_binary', return_value=binary), patch.object(fw, 'run', side_effect=signing) as run:
                previous = os.umask(0o077)
                try:
                    overlay = fw.prepare(self.args(), env); overlay['identifier'] = APP
                finally:
                    os.umask(previous)
                stage = Path(overlay['bundle']['macOS']['files']['Library/SystemExtensions'])
                contents = stage / f'{APP}.firewall.systemextension/Contents'
                info = plistlib.loads((contents / 'Info.plist').read_bytes())
                self.assertEqual(info['CFBundleVersion'], '1.2.3')
                self.assertEqual(info['NetworkExtension']['NEMachServiceName'], TEAM + '.' + APP + '.firewall.1.2.3')
                self.assertEqual(overlay['bundle']['macOS']['bundleVersion'], info['CFBundleVersion'])
                self.assertTrue((contents / 'MacOS/OpenNavoFirewall').is_file())
                self.assertGreater(fw.verify_bundle_permissions(stage), 10)
                for path in [root, stage.parent]:
                    self.assertEqual(path.stat().st_mode & 0o777, 0o700)
                for name in ['app.provisionprofile', 'extension.provisionprofile', 'app.entitlements.plist',
                             'extension.entitlements.plist', 'app-info.plist', 'prepared.json']:
                    self.assertEqual((stage.parent / name).stat().st_mode & 0o777, 0o600)
                embedded_app = Path(overlay['bundle']['macOS']['files']['embedded.provisionprofile'])
                self.assertEqual(embedded_app.stat().st_mode & 0o777, 0o644)
                self.assertEqual((contents / 'embedded.provisionprofile').stat().st_mode & 0o777, 0o644)
                self.assertEqual((contents / 'MacOS/OpenNavoFirewall').stat().st_mode & 0o777, 0o755)
                self.assertEqual(len(list((contents / 'Resources').glob('*.lproj'))), 6)
                self.assertTrue(any('--strict' in c.args[0] for c in run.call_args_list))
                verification = next(c.args[0] for c in run.call_args_list if '-R' in c.args[0])
                self.assertTrue(verification[verification.index('-R') + 1].startswith('='))
                config = root / 'config.json'; config.write_text(json.dumps(overlay)); env['OPENNAVO_FIREWALL_CONFIG'] = str(config)
                args = self.args(); args.hook = True
                fw.prepare(args, env)
                info['NetworkExtension']['NEMachServiceName'] = TEAM + '.' + APP + '.firewall'
                (contents / 'Info.plist').write_bytes(plistlib.dumps(info))
                with self.assertRaisesRegex(ValueError, 'Mach service'): fw.prepare(args, env)
                info['NetworkExtension']['NEMachServiceName'] += '.1.2.3'
                (contents / 'Info.plist').write_bytes(plistlib.dumps(info))
                # Mode changes don't affect the signing-input hashes or seal.
                for path, proper in [(contents / 'MacOS', 0o755), (contents / 'MacOS/OpenNavoFirewall', 0o755),
                                     (contents / 'embedded.provisionprofile', 0o644), (embedded_app, 0o644),
                                     (contents / '_CodeSignature/CodeResources', 0o644)]:
                    path.chmod(0o700 if path.is_dir() or proper == 0o755 else 0o600)
                    with self.assertRaisesRegex(ValueError, 'permission|readable'): fw.prepare(args, env)
                    path.chmod(proper)
                env['TAURI_ENV_TARGET_TRIPLE'] = 'x86_64-apple-darwin'
                with self.assertRaises(ValueError): fw.prepare(args, env)
                del env['TAURI_ENV_TARGET_TRIPLE']
                Path(overlay['bundle']['macOS']['entitlements']).write_text('changed')
                with self.assertRaises(ValueError): fw.prepare(args, env)

    def test_final_bundle_permissions_and_framework_links(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            app = root / 'Example.app'; versions = app / 'Contents/Frameworks/Example.framework/Versions'
            version = versions / 'A'; version.mkdir(parents=True)
            binary = version / 'Example'; binary.write_bytes(b'fixture'); binary.chmod(0o700)
            (versions / 'Current').symlink_to('A', target_is_directory=True)
            (versions.parent / 'Example').symlink_to('Versions/Current/Example')
            resource = app / 'Contents/Info.plist'; resource.write_text('fixture'); resource.chmod(0o600)
            with self.assertRaises(ValueError): fw.verify_bundle_permissions(app)
            fw.normalize_bundle_permissions(app)
            self.assertGreater(fw.verify_bundle_permissions(app), 5)
            subprocess.run([sys.executable, str(Path(fw.__file__)), '--verify-bundle-permissions', str(app)], check=True)
            resource.chmod(0o600)
            rejected = subprocess.run([sys.executable, str(Path(fw.__file__)), '--verify-bundle-permissions', str(app)], capture_output=True)
            self.assertNotEqual(rejected.returncode, 0)
            # Do not normalize or accept links to private files outside the App.
            secret = root / 'private'; secret.write_bytes(b'fixture'); secret.chmod(0o600)
            link = app / 'escape'; link.symlink_to(secret)
            with self.assertRaises(ValueError): fw.normalize_bundle_permissions(app)
            self.assertEqual(secret.stat().st_mode & 0o777, 0o600)
            self.assertEqual(resource.stat().st_mode & 0o777, 0o600)
            link.unlink(); link.symlink_to('missing')
            with self.assertRaises(ValueError): fw.verify_bundle_permissions(app)

    def test_staging_cannot_follow_symlinks(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve(); target = root / 'target'; target.mkdir(); link = root / 'link'; link.symlink_to(target)
            with self.assertRaises(ValueError): fw.safe_directory(link)

    def test_core_lock_has_only_already_inventoried_host_dependencies(self):
        import tomllib
        host = tomllib.loads((fw.TAURI / 'Cargo.lock').read_text())['package']
        core = tomllib.loads((fw.SOURCE / 'core/Cargo.lock').read_text())['package']
        keys = {(p['name'], p['version'], p.get('checksum')) for p in host if p.get('source')}
        self.assertTrue(all((p['name'], p['version'], p.get('checksum')) in keys for p in core if p.get('source')))


if __name__ == '__main__':
    unittest.main()
