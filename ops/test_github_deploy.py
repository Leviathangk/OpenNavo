import base64
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
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
        obj.apply(archive())
        self.assertEqual(obj.services, obj.state['services'])
        self.assertEqual(events, ['verify', ('cleanup', previous)])

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
            self.assertEqual(events[-1], 'cleanup')
            self.assertLess(events.index(('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker')), len(events) - 1)


if __name__ == '__main__': unittest.main()
