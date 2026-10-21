import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
import unittest.mock
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('github_deploy', Path(__file__).with_name('github-deploy.py'))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


def archive(name='index.html', kind=tarfile.REGTYPE):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w:gz') as tar:
        entry = tarfile.TarInfo(name)
        entry.type = kind
        entry.linkname = '/etc/passwd' if kind == tarfile.SYMTYPE else ''
        body = b'<html>fixture</html>'
        entry.size = len(body) if kind == tarfile.REGTYPE else 0
        tar.addfile(entry, io.BytesIO(body))
    return output.getvalue()


def payload():
    data = archive()
    return {'sha': 'a' * 40, 'run_id': '123', 'images': {kind: 'ghcr.io/example/opennavo-' + kind + '@sha256:' + 'b' * 64 for kind in ['server', 'web', 'mcp']},
            'admin_archive': base64.b64encode(data).decode(), 'admin_sha256': hashlib.sha256(data).hexdigest()}


class DeployTests(unittest.TestCase):
    def test_prepare_moves_cache_notifier_to_new_web_and_preserves_rollback(self):
        for use_alias in [False, True]:
            with self.subTest(use_alias=use_alias), tempfile.TemporaryDirectory() as root:
                directory = Path(root)
                (directory / 'deploy').mkdir()
                (directory / 'releases').mkdir()
                assets = directory / 'assets'
                assets.mkdir()
                caddy = directory / 'admin.caddy'
                caddy.write_text(':8081 {}')
                services = {kind: kind + '_previous' for kind in ['api', 'web', 'mcp', 'admin']}
                (directory / 'deploy/github-state.json').write_text(json.dumps({'services': services}))
                overlay = {'services': {name: {'environment': {}, 'networks': {}} for name in services.values()}}
                overlay['services'][services['web']].update({
                    'networks': {'default': {'aliases': ['web-previous-alias']}},
                    'volumes': [{'source': str(assets), 'target': '/app/.output/public/_nuxt'}]})
                overlay['services'][services['admin']]['volumes'] = [
                    {'source': str(caddy), 'target': '/etc/caddy/Caddyfile'},
                    {'source': str(directory / 'admin'), 'target': '/srv/admin'}]
                old_host = 'web-previous-alias' if use_alias else services['web']
                overlay['services']['worker'] = {'environment': {
                    'WEB_INTERNAL_URL': 'http://' + old_host + ':9091',
                    'WEB_INTERNAL_SECRET': '${WEB_INTERNAL_SECRET}', 'OTHER_SETTING': 'unchanged'}}
                rolling = directory / 'deploy/rolling.compose.json'
                rolling.write_text(json.dumps(overlay))
                (directory / 'current-release.env').write_text('SERVER_IMAGE=old\n')
                (directory / 'live.caddy').write_text('reverse_proxy web-previous-alias:3000\n')
                obj = deploy.Deployment({'root': root, 'caddy_file': 'live.caddy',
                                         'compose_file': 'compose.yml', 'project': 'fixture'}, payload())
                with patch.object(deploy, 'command', return_value='fixture-container'):
                    obj.prepare(archive())
                candidate = json.loads((obj.directory / 'rolling.compose.json').read_text())
                expected_host = 'web-default-' + obj.release if use_alias else obj.services['web']
                self.assertEqual(candidate['services']['worker']['environment'], {
                    'WEB_INTERNAL_URL': 'http://' + expected_host + ':9091',
                    'WEB_INTERNAL_SECRET': '${WEB_INTERNAL_SECRET}', 'OTHER_SETTING': 'unchanged'})
                self.assertEqual(json.loads(rolling.read_text()), overlay)
                self.assertEqual(candidate['services'][services['web']], overlay['services'][services['web']])

    def test_cleanup_stops_previous_release_and_removes_only_older_app_containers(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'project': 'example-prod'}
        obj.services = {kind: kind + '_new' for kind in ['api', 'web', 'mcp', 'admin']}
        previous = {'services': {kind: kind + '_previous' for kind in obj.services}}
        def container(service, running=True, project='example-prod'):
            return {'Id': service, 'State': {'Running': running}, 'Config': {'Labels': {
                'com.docker.compose.project': project, 'com.docker.compose.service': service}}}
        containers = [container(s) for s in obj.services.values()]
        containers += [container(s) for s in previous['services'].values()]
        containers += [container('api_older'), container('web_ancient', False), container('api')]
        containers += [container(s) for s in ['worker', 'postgres', 'redis', 'web-cache', 'minio', 'caddy', 'prometheus']]
        containers += [container('api_foreign', project='other-project')]
        def cmd(args):
            if args[1] == 'ps': return '\n'.join(c['Id'] for c in containers)
            if args[1] == 'inspect': return json.dumps(containers)
            return ''
        with patch.object(deploy, 'command', side_effect=cmd) as run:
            obj.cleanup(previous)
        mutations = [call.args[0] for call in run.call_args_list if call.args[0][1] in ['stop', 'rm']]
        self.assertEqual(mutations, [
            ['docker', 'stop', '--timeout', '30', *previous['services'].values(), 'api_older', 'api'],
            ['docker', 'rm', 'api_older', 'web_ancient', 'api']])

    def test_cleanup_does_not_remove_containers_if_stop_fails(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'project': 'example-prod'}
        obj.services = {'api': 'api_new'}
        containers = [{'Id': 'old', 'State': {'Running': True}, 'Config': {'Labels': {
            'com.docker.compose.project': 'example-prod', 'com.docker.compose.service': 'api_old'}}}]
        with patch.object(deploy, 'command', side_effect=['old', json.dumps(containers), RuntimeError('stop failed')]) as run:
            with self.assertRaises(RuntimeError): obj.cleanup({})
        self.assertFalse(any(call.args[0][1] == 'rm' for call in run.call_args_list))

    def test_retry_verifies_existing_release_before_resuming_cleanup(self):
        obj = object.__new__(deploy.Deployment)
        obj.payload = payload()
        previous = {'services': {'api': 'api_previous'}}
        obj.state = {'sha': obj.payload['sha'], 'services': {'api': 'api_live'}, 'previous': previous}
        events = []
        obj.verify = lambda: events.append('verify')
        obj.cleanup = lambda state: events.append(('cleanup', state))
        obj.prune = lambda state: events.append(('prune', state))
        obj.apply(archive())
        self.assertEqual(obj.services, obj.state['services'])
        self.assertEqual(events, ['verify', ('cleanup', previous), ('prune', previous)])

    def test_prune_keeps_recent_live_previous_and_mounted_release_snapshots(self):
        with tempfile.TemporaryDirectory() as root, tempfile.TemporaryDirectory() as outside:
            releases = Path(root) / 'releases'
            releases.mkdir()
            names = [f'gh-{index:012x}-100-1' for index in range(9)]
            for index, name in enumerate(names):
                (releases / name).mkdir()
                (releases / name / 'before-migration.dump').write_text('dump')
                os.utime(releases / name, (1_000_000 + index, 1_000_000 + index))
            # Manual snapshots and other files are operator-owned and never pruned.
            (releases / 'main-abc1234-20261007').mkdir()
            (releases / 'privacy-build.tar.gz').write_text('manual')
            for name in ['main-abc1234-20261007', 'privacy-build.tar.gz']:
                os.utime(releases / name, (1, 1))
            (releases / 'gh-00000000000f-1-1').symlink_to(outside, target_is_directory=True)
            obj = object.__new__(deploy.Deployment)
            obj.config, obj.root = {}, Path(root)
            obj.release, obj.state = names[8], {'release': names[7]}
            containers = [{'Image': 'x', 'Mounts': [{'Source': str(releases / names[2] / 'admin')}]}]
            with contextlib.redirect_stdout(io.StringIO()):
                obj.prune_releases({'release': names[1]}, containers)
            remaining = sorted(path.name for path in releases.iterdir())
            self.assertEqual(remaining, sorted([*names[4:], names[1], names[2], 'main-abc1234-20261007',
                                                'privacy-build.tar.gz', 'gh-00000000000f-1-1']))
            self.assertTrue(Path(outside).is_dir())

    def test_prune_removes_only_unused_application_images(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'registry': 'ghcr.io/example', 'project': 'fixture', 'compose_file': 'compose.yml'}
        obj.root, obj.release, obj.state, obj.payload = Path('/fixture'), 'gh-current', {}, payload()
        image = {name: 'sha256:' + name * 64 for name in 'abcde'}
        listed = {'server': [image['a'], image['c']], 'web': [image['b'], image['d'], image['d']], 'mcp': [image['e']]}
        def cmd(args):
            if args[:3] == ['docker', 'image', 'ls']:
                return '\n'.join(listed[args[-1].rsplit('-', 1)[1]])
            if args[-1] == image['d']:
                raise RuntimeError('command failed: docker (exit 1); inspect privately on host')
            return ''
        with patch.object(deploy, 'command', side_effect=cmd) as run, contextlib.redirect_stdout(io.StringIO()):
            obj.prune_images([{'Image': image['a']}, {'Image': image['b']}])
        removed = [call.args[0][-1] for call in run.call_args_list if call.args[0][:3] == ['docker', 'image', 'rm']]
        self.assertEqual(removed, [image['c'], image['d'], image['e']])

    def test_prune_protects_worker_hotfix_and_resolves_retry_snapshot(self):
        for retry in [False, True]:
            with self.subTest(retry=retry):
                obj = object.__new__(deploy.Deployment)
                obj.config = {'registry': 'ghcr.io/example', 'project': 'fixture', 'compose_file': 'compose.yml'}
                obj.root, obj.release, obj.payload = Path('/fixture'), 'gh-candidate', payload()
                obj.state = {'sha': obj.payload['sha'], 'release': 'gh-live'} if retry else {}
                hotfix, unused = 'sha256:' + 'c' * 64, 'sha256:' + 'd' * 64
                def cmd(args):
                    if args[1] == 'compose': return 'ghcr.io/example/opennavo-server:worker-hotfix'
                    if args[:3] == ['docker', 'image', 'inspect']: return hotfix
                    if args[:3] == ['docker', 'image', 'ls']: return hotfix + '\n' + unused
                    return ''
                with patch.object(deploy, 'command', side_effect=cmd) as run, contextlib.redirect_stdout(io.StringIO()):
                    obj.prune_images([])
                compose = run.call_args_list[0].args[0]
                snapshot = 'gh-live' if retry else 'gh-candidate'
                self.assertIn('/fixture/releases/' + snapshot + '/before-rolling.json', compose)
                self.assertIn('/fixture/releases/' + snapshot + '/before-release.env', compose)
                removed = [call.args[0][-1] for call in run.call_args_list if call.args[0][:3] == ['docker', 'image', 'rm']]
                self.assertNotIn(hotfix, removed)
                self.assertIn(unused, removed)
                with patch.object(deploy, 'command', side_effect=['missing-image', RuntimeError('lookup failed')]) as run:
                    with self.assertRaises(RuntimeError): obj.prune_images([])
                self.assertFalse(any(call.args[0][:3] == ['docker', 'image', 'rm'] for call in run.call_args_list))

    def test_prune_failures_never_fail_a_verified_release(self):
        obj = object.__new__(deploy.Deployment)
        output = io.StringIO()
        with patch.object(deploy, 'command', side_effect=RuntimeError('command failed: docker (exit 1)')), \
                contextlib.redirect_stdout(output):
            obj.prune({})
        self.assertIn('Retention cleanup incomplete', output.getvalue())

    def test_accepts_only_complete_digest_pinned_release(self):
        body = payload()
        self.assertEqual(deploy.validate(body, 'ghcr.io/example'), archive())
        for key, value in [('sha', '../secret'), ('run_id', '1;id')]:
            with self.assertRaises(ValueError):
                deploy.validate({**body, key: value}, 'ghcr.io/example')
        for value in ['ghcr.io/example/opennavo-web:latest', 'ghcr.io/other/opennavo-web@sha256:' + 'b' * 64]:
            body['images']['web'] = value
            with self.assertRaises(ValueError):
                deploy.validate(body, 'ghcr.io/example')

    def test_checksum_and_missing_image_fail_before_deployment(self):
        body = payload()
        body['admin_sha256'] = '0' * 64
        with self.assertRaises(ValueError): deploy.validate(body, 'ghcr.io/example')
        body = payload()
        del body['images']['mcp']
        with self.assertRaises(ValueError): deploy.validate(body, 'ghcr.io/example')

    def test_rejects_archive_traversal_and_links(self):
        for name, kind in [('../escape', tarfile.REGTYPE), ('/etc/escape', tarfile.REGTYPE), ('link', tarfile.SYMTYPE)]:
            with tempfile.TemporaryDirectory() as root, self.assertRaises(ValueError):
                deploy.extract_admin(archive(name, kind), Path(root))

    def test_extracts_static_admin_and_requires_index(self):
        with tempfile.TemporaryDirectory() as root:
            deploy.extract_admin(archive(), Path(root))
            self.assertTrue((Path(root) / 'index.html').is_file())
        with tempfile.TemporaryDirectory() as root, self.assertRaises(ValueError):
            deploy.extract_admin(archive('other.txt'), Path(root))

    def test_environment_update_preserves_unrelated_secrets(self):
        self.assertEqual(deploy.update_env('SERVER_IMAGE=old\nPRIVATE=value\n', {'SERVER_IMAGE': 'new', 'WEB_IMAGE': 'web'}), 'SERVER_IMAGE=new\nPRIVATE=value\nWEB_IMAGE=web\n')

    def test_pulls_every_image_and_rejects_a_foreign_revision_before_preparing(self):
        for revision in ['a' * 40, 'f' * 40]:
            with self.subTest(revision=revision):
                obj = object.__new__(deploy.Deployment)
                obj.payload, obj.state = payload(), {}
                events = []
                def stop(_):
                    events.append('prepare')
                    raise RuntimeError('stop after pulls')
                obj.prepare = stop
                def cmd(args, output=None):
                    if args[1:4] == ['--config', args[2], 'pull']:
                        events.append(('pull', args[-1]))
                    if args[:3] == ['docker', 'image', 'inspect']:
                        return json.dumps({'org.opencontainers.image.revision': revision})
                    return ''
                with patch.object(deploy, 'command', side_effect=cmd), contextlib.redirect_stdout(io.StringIO()), \
                        self.assertRaises(RuntimeError) as failure:
                    obj.apply(archive())
                pulled = sorted(event[1] for event in events if event[0] == 'pull')
                self.assertEqual(pulled, sorted(obj.payload['images'].values()))
                if revision == obj.payload['sha']:
                    self.assertEqual(str(failure.exception), 'stop after pulls')
                else:
                    self.assertIn('image source revision does not match', str(failure.exception))
                    self.assertNotIn('prepare', events)

    def test_backup_prefers_zstd_and_falls_back_to_the_default_compression(self):
        for zstd in [True, False]:
            with self.subTest(zstd=zstd), tempfile.TemporaryDirectory() as root:
                obj = object.__new__(deploy.Deployment)
                obj.config = {'postgres_container': 'db', 'postgres_user': 'user', 'postgres_database': 'app'}
                obj.directory = Path(root)
                dumps = []
                def cmd(args, output=None):
                    dumps.append(args)
                    if '--compress=zstd' in args and not zstd:
                        output.write(b'pg_dump: error: compression method zstd is not supported')
                        raise RuntimeError('command failed: docker (exit 1); inspect privately on host')
                    output.write(b'PGDMP' + b'x' * 2048)
                    return ''
                restored = unittest.mock.Mock(returncode=0)
                with patch.object(deploy, 'command', side_effect=cmd), \
                        patch.object(deploy.subprocess, 'run', return_value=restored), contextlib.redirect_stdout(io.StringIO()):
                    obj.backup()
                self.assertEqual(dumps[0][-1], '--compress=zstd')
                self.assertEqual(len(dumps), 1 if zstd else 2)
                if not zstd:
                    self.assertEqual(dumps[1][-1], '-Fc')
                # A retry replaces the failed attempt's output instead of appending to it.
                self.assertTrue((obj.directory / 'before-migration.dump').read_bytes().startswith(b'PGDMP'))

    def test_failed_route_check_restores_old_route_and_worker_without_down_migration(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {}
            obj.state = {'sha': 'c' * 40}
            obj.payload = payload()
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            obj.release = 'test'
            obj.services = {k: k + '_new' for k in ['api', 'web', 'admin', 'mcp']}
            obj.live_caddy = directory / 'live'
            obj.env = directory / 'env'
            obj.rolling = directory / 'rolling'
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            (directory / 'Caddyfile.next').write_text('new')
            obj.live_caddy.write_text('old')
            events = []
            obj.prepare = lambda _: events.append('prepare')
            obj.backup = lambda: events.append('backup')
            obj.compose = lambda *args, **kwargs: events.append(args)
            obj.reload = lambda: events.append('reload')
            obj.cleanup = lambda _: events.append('cleanup')
            obj.prune = lambda _: events.append('prune')
            def fail(): raise RuntimeError('route check failed')
            obj.verify = fail
            def cmd(args, output=None):
                if args[:3] == ['docker', 'image', 'inspect']:
                    return json.dumps({'org.opencontainers.image.revision': 'a' * 40})
                return ''
            with patch.object(deploy, 'command', side_effect=cmd), self.assertRaises(RuntimeError):
                obj.apply(archive())
            self.assertEqual(obj.live_caddy.read_text(), 'old')
            self.assertEqual(obj.env.read_text(), 'old')
            self.assertEqual(obj.rolling.read_text(), 'old')
            self.assertLess(events.index('backup'), events.index(('run', '--rm', '--no-deps', 'migrate', 'up')))
            self.assertIn(('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker'), events)
            self.assertFalse(obj.state_path.exists())
            self.assertNotIn('down', str(events))
            self.assertNotIn('cleanup', events)
            self.assertNotIn('prune', events)

            # A successful retry must persist the verified release before cleanup.
            obj.state['previous'] = {'sha': 'd' * 40}
            (directory / 'release.env').write_text('new-env')
            (directory / 'rolling.compose.json').write_text('{}')
            events.clear()
            obj.verify = lambda: events.append('verify')
            def cleanup(previous):
                state = json.loads(obj.state_path.read_text())
                self.assertEqual(state['sha'], obj.payload['sha'])
                self.assertEqual(state['previous'], {'sha': 'c' * 40})
                self.assertEqual(previous, state['previous'])
                events.append('cleanup')
            obj.cleanup = cleanup
            with patch.object(deploy, 'command', side_effect=cmd):
                obj.apply(archive())
            self.assertEqual(events.count('verify'), 2)
            self.assertEqual(events[-2:], ['cleanup', 'prune'])
            self.assertLess(events.index(('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker')), len(events) - 1)


if __name__ == '__main__': unittest.main()
