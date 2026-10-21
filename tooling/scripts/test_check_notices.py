"""Exercise the notice CLI against isolated inventories, never the working tree."""
import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('check-notices.py')
BUNDLES = ['docs/legal/third-party', 'apps/server/third-party', 'apps/mcp/third-party',
           'apps/desktop/src-tauri/resources/third-party']
WORKFLOWS = ['ci-frontend.yml', 'release-desktop.yml']


class NoticeGateTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / 'tooling/scripts').mkdir(parents=True)
        (self.root / 'apps/desktop/src-tauri').mkdir(parents=True)
        shutil.copyfile(SCRIPT, self.root / 'tooling/scripts/check-notices.py')
        for name in ['LICENSE', 'NOTICE', 'lock.txt']:
            (self.root / name).write_text(name + '\n')
        (self.root / 'apps/desktop/src-tauri/rust-toolchain.toml').write_text('[toolchain]\nchannel = "1.96.0"\n')
        workflows = self.root / '.github/workflows'
        workflows.mkdir(parents=True)
        for name in WORKFLOWS:
            (workflows / name).write_text('steps:\n  - uses: dtolnay/rust-toolchain@1.96.0\n')
        text = b'Synthetic upstream notice\n'
        digest = hashlib.sha256(text).hexdigest()

        def module(eco, name, scope='runtime', version='1.0.0'):
            return dict(ecosystem=eco, name=name, version=version, scope=scope, license='MIT',
                        evidence={'toolchain': version}, url='https://example.invalid',
                        licenseFiles=[{'source': 'LICENSE', 'path': f'licenses/{digest}.txt', 'sha256': digest}])

        go = module('Go', 'server-lib')
        go_std = module('Platform', 'Go standard library')
        python = [module('Python', 'first', 'runtime-candidate'), module('Python', 'second', 'runtime-candidate')]
        rust = module('Rust', 'desktop-lib', 'runtime-candidate')
        rust_std = module('Platform', 'Rust standard library', version='1.96.0')
        npm = module('npm', 'browser-lib', 'runtime-candidate')
        asset = module('npm', 'icons', 'runtime-asset')
        template = module('Platform', 'soybean-admin')
        native = module('Native', 'standalone-helper')
        self.dev = module('Python', 'test-only', 'development')
        external = module('Platform', 'Redis', 'external')
        self.canonical = dict(formatVersion=1, lockDigests={'lock.txt': hashlib.sha256(b'lock.txt\n').hexdigest()},
                              modules=[go, go_std, *python, rust, rust_std, npm, asset, template, native, self.dev, external])
        selections = [self.canonical['modules'], [go, go_std], python, [rust, rust_std, npm, asset, template, native]]
        for folder, selected in zip(BUNDLES, selections):
            path = self.root / folder
            (path / 'licenses').mkdir(parents=True)
            (path / f'licenses/{digest}.txt').write_bytes(text)
            for name in ['LICENSE', 'NOTICE']:
                shutil.copyfile(self.root / name, path / name)
            self.write_inventory(folder, {**self.canonical, 'modules': selected})

    def write_inventory(self, folder, inventory):
        (self.root / folder / 'inventory.json').write_text(json.dumps(inventory))

    def mutate(self, folder, callback):
        path = self.root / folder / 'inventory.json'
        inventory = json.loads(path.read_text())
        callback(inventory)
        self.write_inventory(folder, inventory)

    def check(self, expected=0, *args, env=None):
        result = subprocess.run([sys.executable, str(self.root / 'tooling/scripts/check-notices.py'), *args],
                                capture_output=True, text=True, env=env)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

    def test_valid_inventory_and_reordered_bundle_pass(self):
        self.mutate(BUNDLES[3], lambda i: i['modules'].reverse())
        self.check()

    def test_server_native_data_carries_its_notice_in_server_bundle(self):
        native = copy.deepcopy(next(m for m in self.canonical['modules'] if m['ecosystem'] == 'Native'))
        native['name'] = 'vendor-data'
        native['evidence']['products'] = ['server']
        self.mutate(BUNDLES[0], lambda i: i['modules'].append(native))
        self.check(1)
        self.mutate(BUNDLES[1], lambda i: i['modules'].append(native))
        self.check()
        self.mutate(BUNDLES[3], lambda i: i['modules'].append(native))
        self.check(1)

    def test_empty_runtime_bundle_fails_for_each_distribution(self):
        for folder in BUNDLES[1:]:
            with self.subTest(folder=folder):
                original = (self.root / folder / 'inventory.json').read_text()
                self.mutate(folder, lambda i: i.update(modules=[]))
                self.check(1)
                (self.root / folder / 'inventory.json').write_text(original)

    def test_single_omission_fails(self):
        self.mutate(BUNDLES[2], lambda i: i['modules'].pop())
        self.check(1)

    def test_project_relicense_requires_every_bundle_to_be_regenerated(self):
        # A license change must carry the grant and section 7 notice together.
        for name, text in [('LICENSE', 'GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n'),
                           ('NOTICE', 'GPL-3.0-only; additional terms under sections 7(c) and 7(e)\n')]:
            (self.root / name).write_text(text)
            for folder in BUNDLES:
                self.check(1)
                shutil.copyfile(self.root / name, self.root / folder / name)
            self.check()

    def test_duplicate_bundle_record_fails(self):
        self.mutate(BUNDLES[2], lambda i: i['modules'].append(copy.deepcopy(i['modules'][0])))
        self.check(1)

    def test_duplicate_canonical_record_fails(self):
        self.mutate(BUNDLES[0], lambda i: i['modules'].append(copy.deepcopy(i['modules'][0])))
        self.check(1)

    def test_changed_record_content_fails_even_with_valid_notice_hashes(self):
        self.mutate(BUNDLES[2], lambda i: i['modules'][0].update(license='BSD-2-Clause'))
        self.check(1)

    def test_unexpected_development_record_fails(self):
        self.mutate(BUNDLES[2], lambda i: i['modules'].append(self.dev))
        self.check(1)

    def test_changed_toolchain_pin_fails(self):
        (self.root / 'apps/desktop/src-tauri/rust-toolchain.toml').write_text('[toolchain]\nchannel = "1.97.0"\n')
        self.check(1)

    def test_moving_workflow_toolchain_fails(self):
        (self.root / '.github/workflows/release-desktop.yml').write_text('steps:\n  - uses: dtolnay/rust-toolchain@stable\n')
        self.check(1)

    def test_parallel_jobs_may_each_install_the_pinned_toolchain(self):
        workflow = self.root / '.github/workflows/ci-frontend.yml'
        workflow.write_text('steps:\n  - uses: dtolnay/rust-toolchain@1.96.0\n  - uses: dtolnay/rust-toolchain@1.96.0\n')
        self.check(0)
        workflow.write_text('steps:\n  - uses: dtolnay/rust-toolchain@1.96.0\n  - uses: dtolnay/rust-toolchain@stable\n')
        self.check(1)
        workflow.write_text('steps:\n  - run: true\n')
        self.check(1)

    def test_actual_compiler_must_match_notices_when_requested(self):
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        rustc = bin_dir / 'rustc'
        rustc.write_text('#!/bin/sh\nprintf "rustc 1.97.0 (synthetic)\\n"\n')
        rustc.chmod(0o755)
        env = {**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']}
        self.check(1, '--check-rust-toolchain', env=env)
        rustc.write_text('#!/bin/sh\nprintf "rustc 1.96.0 (synthetic)\\n"\n')
        self.check(0, '--check-rust-toolchain', env=env)


if __name__ == '__main__':
    unittest.main()
