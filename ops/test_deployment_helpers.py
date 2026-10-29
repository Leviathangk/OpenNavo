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


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


deploy = load('github_deploy_helpers', Path(__file__).with_name('github-deploy.py'))
sender = load('send_deployment_helpers', Path(__file__).parent.parent / '.github/scripts/send-deployment.py')


def archive():
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w:gz') as tar:
        entry = tarfile.TarInfo('index.html')
        body = b'<html>fixture</html>'
        entry.size = len(body)
        tar.addfile(entry, io.BytesIO(body))
    return output.getvalue()


class DeploymentHelperTests(unittest.TestCase):
    def test_capacity_gate_passes_one_replica_and_rejects_two_before_mutation(self):
        mem = 'MemAvailable:       1300000 kB\n'
        one = object.__new__(deploy.Deployment)
        one.config = {'project': 'fixture', 'web_replicas': 1}
        one.web_replicas = 1
        with patch.object(deploy.Path, 'read_text', return_value=mem):
            one.capacity_gate()
        two = object.__new__(deploy.Deployment)
        two.config = {'project': 'fixture', 'web_replicas': 2}
        two.web_replicas = 2
        with patch.object(deploy.Path, 'read_text', return_value=mem):
            with self.assertRaisesRegex(RuntimeError, r'web_replicas=2.*requires'):
                two.capacity_gate()

    def test_candidate_overlay_and_compose_scale_use_configured_web_replicas(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            for name in ['deploy', 'releases', 'assets']:
                (directory / name).mkdir()
            (directory / 'admin.caddy').write_text(':8081 {}')
            services = {kind: kind + '_previous' for kind in ['api', 'web', 'mcp', 'admin']}
            (directory / 'deploy/github-state.json').write_text(json.dumps({'services': services}))
            overlay = {'services': {name: {'environment': {}, 'networks': {}} for name in services.values()}}
            overlay['services'][services['web']]['volumes'] = [
                {'source': str(directory / 'assets'), 'target': '/app/.output/public/_nuxt'}
            ]
            overlay['services'][services['admin']]['volumes'] = [
                {'source': str(directory / 'admin.caddy'), 'target': '/etc/caddy/Caddyfile'},
                {'source': str(directory / 'admin'), 'target': '/srv/admin'},
            ]
            (directory / 'deploy/rolling.compose.json').write_text(json.dumps(overlay))
            (directory / 'current-release.env').write_text('SERVER_IMAGE=old\n')
            (directory / 'live.caddy').write_text('reverse_proxy web_previous:3000\n')
            config = {'root': root, 'caddy_file': 'live.caddy', 'compose_file': 'compose.yml',
                      'project': 'fixture', 'web_replicas': 2}
            obj = deploy.Deployment(config, {
                **{
                    'sha': 'a' * 40, 'run_id': '123',
                    'images': {kind: 'ghcr.io/example/opennavo-' + kind + '@sha256:' + 'b' * 64
                               for kind in ['server', 'web', 'mcp']},
                },
                'admin_archive': base64.b64encode(archive()).decode(),
                'admin_sha256': hashlib.sha256(archive()).hexdigest(),
            })
            with patch.object(deploy, 'command', return_value='fixture-container'):
                obj.prepare(archive())
            candidate = json.loads((obj.directory / 'rolling.compose.json').read_text())
            self.assertEqual(candidate['services'][obj.services['web']]['deploy']['replicas'], 2)
            healthcheck = candidate['services'][obj.services['web']]['healthcheck']['test'][-1]
            self.assertIn('9091/healthz', healthcheck)
            self.assertIn('3000/robots.txt', healthcheck)
            self.assertNotIn('NODE_OPTIONS', candidate['services'][obj.services['web']].get('environment', {}))

    def test_scale_flag_is_omitted_for_one_replica_and_matches_overlay_for_two(self):
        obj = object.__new__(deploy.Deployment)
        obj.services = {'api': 'api_new', 'web': 'web_new', 'mcp': 'mcp_new', 'admin': 'admin_new'}
        obj.web_replicas = 1
        one = obj.candidate_up_args()
        self.assertNotIn('--scale', one)
        obj.web_replicas = 2
        two = obj.candidate_up_args()
        self.assertEqual(two[two.index('--scale') + 1], 'web_new=2')

    def test_warm_paths_are_optional_but_reject_private_query_encoded_and_traversal_paths(self):
        self.assertEqual(deploy.warm_paths({})[:len(deploy.WARM_PATHS)], deploy.WARM_PATHS)
        for path in ['/api/v1/private', '/apps?x=1', '/apps/%2F', '/apps/../admin', '/apps//x']:
            with self.subTest(path=path), self.assertRaises(ValueError):
                deploy.warm_paths({'warm_paths': [path]})

    def test_payload_with_warm_paths_is_accepted_and_old_payload_without_it_remains_valid(self):
        data = archive()
        body = {
            'sha': 'a' * 40, 'run_id': '123',
            'images': {kind: 'ghcr.io/example/opennavo-' + kind + '@sha256:' + 'b' * 64
                       for kind in ['server', 'web', 'mcp']},
            'admin_archive': base64.b64encode(data).decode(),
            'admin_sha256': hashlib.sha256(data).hexdigest(),
            'warm_paths': ['/apps/public'],
        }
        self.assertEqual(deploy.validate(body, 'ghcr.io/example'), data)
        self.assertEqual(deploy.validate({key: value for key, value in body.items() if key != 'warm_paths'},
                                         'ghcr.io/example'), data)

    def test_sender_falls_back_when_analytics_artifact_is_missing_or_corrupt(self):
        with tempfile.TemporaryDirectory() as root:
            missing = Path(root) / 'missing.json'
            self.assertEqual(sender.load_warm_paths(missing), sender.DEFAULT_WARM_PATHS)
            corrupt = Path(root) / 'warm-paths.json'
            corrupt.write_text('{not-json')
            self.assertEqual(sender.load_warm_paths(corrupt), sender.DEFAULT_WARM_PATHS)


if __name__ == '__main__':
    unittest.main()
