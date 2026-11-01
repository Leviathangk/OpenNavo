import base64
import contextlib
import datetime
import hashlib
import importlib.util
import io
import json
import re
import os
import subprocess
from pathlib import Path
import shutil
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



GOOD_CADDY = b'{$WEB_DOMAIN} {\n\treverse_proxy web:3000\n}\n'
TRUNCATED_CADDY = b'{$WEB_DOMAIN} {\n\treverse_proxy'


class FakeCaddy:
    """Fake docker/caddy: validate reads the real file it was pointed at; contents in `good` pass."""

    def __init__(self, live, good=(GOOD_CADDY,), validator='ok', signal_ok=True):
        self.live, self.good, self.validator, self.signal_ok = live, set(good), validator, signal_ok
        self.staged, self.events = {}, []

    def __call__(self, args):
        if args[:2] == ['docker', 'cp']:
            self.staged[args[3].split(':', 1)[1]] = Path(args[2]).read_bytes()
            return ''
        if 'validate' in args:
            target = args[args.index('--config') + 1]
            content = self.live.read_bytes() if target == '/etc/caddy/Caddyfile' else self.staged[target]
            self.events.append(('validate', target, content))
            if self.validator == 'timeout':
                raise deploy.subprocess.TimeoutExpired(args, 1)
            if self.validator == 'daemon':
                raise RuntimeError('command failed: docker (exit 125); inspect privately on host')
            if self.validator == 'missing' or content not in self.good:
                raise RuntimeError('command failed: docker (exit 1); inspect privately on host')
            return ''
        if 'version' in args:
            if self.validator == 'missing':
                raise RuntimeError('command failed: docker (exit 1); inspect privately on host')
            return 'v2'
        if '--signal=USR1' in args:
            self.events.append(('signal', self.live.read_bytes()))
            if not self.signal_ok:
                raise RuntimeError('fixture reload failure')
        return ''


def prepare_on_host(root, api_environment, include_public_mcp=False, api_proxy_alias=None):
    """Prepare a release on a minimal existing host and return the deployment and its candidate overlay."""
    directory = Path(root)
    # Pin host configuration so lint's isolated verification environment cannot change it.
    (directory / '.env').write_text('PUBLIC_MCP_PROXY_IP=172.30.80.3\nPUBLIC_MCP_PROXY_IP_ALT=172.30.80.4\n')
    for name in ['deploy', 'releases', 'assets']:
        (directory / name).mkdir()
    (directory / 'admin.caddy').write_text(':8081 {}')
    kinds = ['api', 'web', 'mcp', 'admin'] + (['public-mcp'] if include_public_mcp else [])
    services = {kind: kind + '_previous' for kind in kinds}
    (directory / 'deploy/github-state.json').write_text(json.dumps({'services': services}))
    overlay = {'services': {name: {'environment': {}, 'networks': {}} for name in services.values()}}
    overlay['services'][services['api']]['environment'] = dict(api_environment)
    overlay['services'][services['web']]['volumes'] = [{'source': str(directory / 'assets'), 'target': '/app/.output/public/_nuxt'}]
    overlay['services'][services['admin']]['volumes'] = [
        {'source': str(directory / 'admin.caddy'), 'target': '/etc/caddy/Caddyfile'},
        {'source': str(directory / 'admin'), 'target': '/srv/admin'}]
    if include_public_mcp:
        overlay['services'][services['api']]['networks'] = {'proxy': {'aliases': ['api-proxy']}}
        overlay['services'][services['public-mcp']].update({
            'entrypoint': ['opennavo-public-mcp'],
            'environment': {'OPENNAVO_PUBLIC_API_BASE': 'http://api-proxy:8080/api/v1'},
            'networks': {'proxy': {'aliases': ['public-mcp-proxy']}},
        })
    if api_proxy_alias:
        overlay['services'][services['api']]['networks'] = {'proxy': {'aliases': [api_proxy_alias]}}
    (directory / 'deploy/rolling.compose.json').write_text(json.dumps(overlay))
    (directory / 'current-release.env').write_text('SERVER_IMAGE=old\n')
    route = ('{$WEB_DOMAIN} {\n'
             '\timport security-web\n'
             '\tencode zstd gzip\n'
             '\treverse_proxy {\n'
             '\t\tdynamic a\n'
             '\t}\n'
             '}\n')
    (directory / 'live.caddy').write_text(route)
    obj = deploy.Deployment({'root': root, 'caddy_file': 'live.caddy', 'caddy_container': 'fixture-caddy',
                             'compose_file': 'compose.yml', 'project': 'fixture'}, payload())
    with patch.object(deploy, 'command', return_value='fixture-container'):
        obj.prepare(archive())
    return obj, json.loads((obj.directory / 'rolling.compose.json').read_text())



def fake_public_mcp(requests=None, *, call_result=None, subpath_status=404, subpath_body=None):
    """urlopen fake for the public MCP: handshake, tools/list, tools/call and the /mcp/x probe."""
    requests = [] if requests is None else requests
    if call_result is None:
        call_result = {'result': {'content': [{'type': 'text', 'text': '[{"slug":"developer-tools"}]'}]}}
    if subpath_body is None:
        subpath_body = deploy.PUBLIC_MCP_SUBPATH_MARKER

    class Response(io.BytesIO):
        def __init__(self, body, session=None, status=200):
            super().__init__(json.dumps(body).encode())
            self.status = status
            self.headers = {'Mcp-Session-Id': session} if session else {}

    def urlopen(request, timeout):
        if request.data is None:
            requests.append(('GET', request.full_url))
            if subpath_status >= 400:
                raise deploy.urllib.error.HTTPError(request.full_url, subpath_status, 'probe', {},
                                                    io.BytesIO(subpath_body.encode()))
            return Response({}, status=subpath_status)
        method = json.loads(request.data)['method']
        requests.append(method)
        if method == 'initialize':
            return Response({'result': {'protocolVersion': '2025-06-18'}}, 'session-1')
        if method == 'tools/list':
            return Response({'result': {'tools': [{'name': 'list_categories'}]}})
        return Response(call_result)
    return urlopen


class DeployTests(unittest.TestCase):
    def setUp(self):
        # Rollback flows validate before-caddy as a staged copy. Tests that script the exact docker
        # command sequence treat that step as passing; Caddy-specific tests use the real check.
        self._staged = patch.object(deploy.Deployment, '_validate_staged', return_value=True)
        self._staged.start()
        # Route the no-redirect probe through whatever urlopen fake a test installs.
        patch.object(deploy, 'open_without_redirects',
                     side_effect=lambda request, timeout: deploy.urllib.request.urlopen(request, timeout)).start()
        self.addCleanup(patch.stopall)

    def real_staged_validation(self):
        self._staged.stop()

    def test_merge_web_assets_keeps_recent_old_files_and_expires_stale_files(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            previous, current, destination = (root / name for name in ['previous', 'current', 'destination'])
            previous.mkdir(); current.mkdir(); destination.mkdir()
            now = deploy.WEB_ASSET_RETENTION_SECONDS + 100
            (previous / 'old.js').write_text('old')
            (previous / 'stale.js').write_text('stale')
            os.utime(previous / 'old.js', (now - 86400, now - 86400))
            os.utime(previous / 'stale.js', (now - deploy.WEB_ASSET_RETENTION_SECONDS - 1, now - deploy.WEB_ASSET_RETENTION_SECONDS - 1))
            shutil.copytree(previous, destination, dirs_exist_ok=True)
            (current / 'new.js').write_text('new')
            deploy.merge_web_assets(current, destination, now=now, previous_current={'old.js'})
            self.assertTrue((destination / 'old.js').exists())
            self.assertTrue((destination / 'new.js').exists())
            self.assertFalse((destination / 'stale.js').exists())
            self.assertEqual((destination / 'new.js').stat().st_mtime, now)

    def test_merge_web_assets_refreshes_the_previous_live_release_after_eight_days(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            current, destination = (root / name for name in ['current', 'destination'])
            current.mkdir(); destination.mkdir()
            now = deploy.WEB_ASSET_RETENTION_SECONDS * 2
            (destination / 'previous.js').write_text('previous')
            (destination / 'earlier.js').write_text('earlier')
            os.utime(destination / 'previous.js', (now - deploy.WEB_ASSET_RETENTION_SECONDS - 86400,) * 2)
            os.utime(destination / 'earlier.js', (now - deploy.WEB_ASSET_RETENTION_SECONDS * 2,) * 2)
            (current / 'new.js').write_text('new')

            deploy.merge_web_assets(current, destination, now=now, previous_current={'previous.js'})

            self.assertTrue((destination / 'previous.js').exists())
            self.assertEqual((destination / 'previous.js').stat().st_mtime, now)
            self.assertTrue((destination / 'new.js').exists())
            self.assertFalse((destination / 'earlier.js').exists())

    def test_merge_web_assets_does_not_refresh_older_retained_assets(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            current, destination = (root / name for name in ['current', 'destination'])
            current.mkdir(); destination.mkdir()
            now = deploy.WEB_ASSET_RETENTION_SECONDS + 100
            (destination / 'live.js').write_text('live')
            (destination / 'retained.js').write_text('retained')
            retained_mtime = now - deploy.WEB_ASSET_RETENTION_SECONDS - 1
            os.utime(destination / 'live.js', (now - 86400,) * 2)
            os.utime(destination / 'retained.js', (retained_mtime,) * 2)
            (current / 'new.js').write_text('new')

            deploy.merge_web_assets(current, destination, now=now, previous_current={'live.js'})

            self.assertTrue((destination / 'live.js').exists())
            self.assertFalse((destination / 'retained.js').exists())

    def test_merge_web_assets_refreshes_all_files_when_legacy_manifest_is_missing(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            current, destination = (root / name for name in ['current', 'release/web-assets'])
            current.mkdir(); destination.mkdir(parents=True)
            now = deploy.WEB_ASSET_RETENTION_SECONDS + 100
            for index, name in enumerate(['old-a.js', 'old-b.js', 'old-c.js']):
                path = destination / name
                path.write_text(name)
                # Keep each file on a different whole-second timestamp, all
                # older than the retention window.
                mtime = now - deploy.WEB_ASSET_RETENTION_SECONDS - 10 - index
                os.utime(path, (mtime, mtime))
            (current / 'new.js').write_text('new')

            deploy.merge_web_assets(current, destination, now=now)

            for name in ['old-a.js', 'old-b.js', 'old-c.js']:
                path = destination / name
                self.assertTrue(path.exists())
                self.assertEqual(path.stat().st_mtime, now)
            self.assertTrue((destination / 'new.js').exists())

    def test_previous_web_asset_paths_uses_the_release_manifest(self):
        with tempfile.TemporaryDirectory() as root:
            release = Path(root) / 'release'
            destination = release / 'web-assets'
            destination.mkdir(parents=True)
            (destination / 'live.js').write_text('live')
            (destination / 'retained.js').write_text('retained')
            os.utime(destination / 'live.js', (1, 1))
            os.utime(destination / 'retained.js', (2, 2))
            (release / 'manifest.json').write_text(json.dumps({'web_assets': ['live.js']}))

            self.assertEqual(deploy.previous_web_asset_paths(destination), {'live.js'})

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
                (directory / 'live.caddy').write_text(
                    '{$WEB_DOMAIN} {\n\treverse_proxy {\n\t dynamic a\n\t}\n}\n')
                obj = deploy.Deployment({'root': root, 'caddy_file': 'live.caddy',
                                         'compose_file': 'compose.yml', 'project': 'fixture'}, payload())
                with patch.object(deploy, 'command', return_value='fixture-container'):
                    obj.prepare(archive())
                candidate = json.loads((obj.directory / 'rolling.compose.json').read_text())
                self.assertEqual(candidate['services'][obj.services['web']]['environment']['NUXT_IMAGE_REDIS_URL'], 'redis://image-cache:6379/0')
                self.assertNotIn('volumes', candidate['services']['image-cache'])
                self.assertNotIn('ports', candidate['services']['image-cache'])
                expected_host = 'web-default-' + obj.release if use_alias else obj.services['web']
                self.assertEqual(candidate['services']['worker']['environment'], {
                    'WEB_INTERNAL_URL': 'http://' + expected_host + ':9091',
                    'WEB_INTERNAL_SECRET': '${WEB_INTERNAL_SECRET}', 'OTHER_SETTING': 'unchanged'})
                self.assertEqual(json.loads(rolling.read_text()), overlay)
                self.assertEqual(candidate['services'][services['web']], overlay['services'][services['web']])

    def test_prepare_gives_an_old_api_service_the_usage_country_proxy_and_keeps_host_values(self):
        # A host provisioned before the setting gets the same value as a fresh install from the base compose.
        default = '${CADDY_PROXY_IP:-172.30.80.2}'
        public_proxy = '${PUBLIC_MCP_PROXY_IP:-172.30.80.3}/32'
        public_proxy_alt = '${PUBLIC_MCP_PROXY_IP_ALT:-172.30.80.4}/32'
        trusted_default = '${CADDY_PROXY_IP:-172.30.80.2},' + public_proxy + ',' + public_proxy_alt + ',${SSR_PROXY_SUBNET:-172.30.81.0/24}'
        base = Path(__file__).with_name('docker-compose.prod.yml').read_text()
        self.assertIn('USAGE_COUNTRY_TRUSTED_PROXIES: ' + default + '\n', base)
        cases = [
            ({}, trusted_default, default),
            ({'TRUSTED_PROXIES': '172.30.80.2'}, '172.30.80.2,' + public_proxy + ',' + public_proxy_alt, default),
            ({'TRUSTED_PROXIES': '192.0.2.10'}, '192.0.2.10,' + public_proxy + ',' + public_proxy_alt, default),
            ({'TRUSTED_PROXIES': '192.0.2.10,' + public_proxy}, '192.0.2.10,' + public_proxy + ',' + public_proxy_alt, default),
            ({'TRUSTED_PROXIES': '192.0.2.10,172.30.80.3'}, '192.0.2.10,172.30.80.3,' + public_proxy_alt, default),
            ({'USAGE_COUNTRY_TRUSTED_PROXIES': '192.0.2.10'}, trusted_default, '192.0.2.10'),
            ({'USAGE_COUNTRY_TRUSTED_PROXIES': ''}, trusted_default, ''),
        ]
        for environment, trusted_expected, usage_expected in cases:
            with self.subTest(environment=environment), tempfile.TemporaryDirectory() as root:
                obj, candidate = prepare_on_host(root, environment)
                self.assertEqual(candidate['services'][obj.services['api']]['environment'], {
                    'TRUSTED_PROXIES': trusted_expected,
                    'USAGE_COUNTRY_TRUSTED_PROXIES': usage_expected,
                })
                # The rollback service stays exactly as it ran.
                self.assertEqual(candidate['services']['api_previous']['environment'],
                                 environment)

    def test_prepare_uses_host_proxy_addresses_with_conflicting_process_environment(self):
        with patch.dict(os.environ, {'PUBLIC_MCP_PROXY_IP': '10.250.90.3',
                                     'PUBLIC_MCP_PROXY_IP_ALT': '10.250.90.4'}), \
                tempfile.TemporaryDirectory() as root:
            obj, candidate = prepare_on_host(root, {})
            self.assertEqual(obj.public_mcp_proxy_ips(), ('172.30.80.3', '172.30.80.4'))
            self.assertEqual(candidate['services'][obj.services['public-mcp']]['networks']['proxy']['ipv4_address'],
                             '172.30.80.3')
            self.assertEqual(candidate['services'][obj.services['api']]['environment']['TRUSTED_PROXIES'],
                             deploy.DEFAULT_TRUSTED_PROXIES)

    def test_prepare_creates_public_mcp_candidate_with_alias_and_health_check(self):
        with tempfile.TemporaryDirectory() as root:
            obj, candidate = prepare_on_host(root, {
                'TRUSTED_PROXIES': '172.30.80.2',
            }, include_public_mcp=True)
            service = candidate['services'][obj.services['public-mcp']]
            self.assertEqual(service['image'], payload()['images']['mcp'])
            self.assertEqual(service['entrypoint'], ['opennavo-public-mcp'])
            self.assertIn("127.0.0.1:8790/healthz", service['healthcheck']['test'][-1])
            self.assertEqual(service['environment']['OPENNAVO_PUBLIC_API_BASE'],
                             'http://api-proxy-' + obj.release + ':8080/api/v1')
            self.assertEqual(obj.services['public-mcp'], 'public-mcp_' + obj.release.replace('-', '_'))
            self.assertIn('public-mcp-proxy-' + obj.release, (obj.directory / 'Caddyfile.next').read_text())
            self.assertIn(obj.services['public-mcp'], obj.candidate_up_args())

    def test_public_mcp_proxy_preflight_rotates_reserved_addresses(self):
        for old_ip, expected in [(None, '172.30.80.3'), ('172.30.80.3', '172.30.80.4'),
                                 ('172.30.80.4', '172.30.80.3')]:
            with self.subTest(old_ip=old_ip), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                (root / '.env').write_text('PUBLIC_MCP_PROXY_IP=172.30.80.3\nPUBLIC_MCP_PROXY_IP_ALT=172.30.80.4\n')
                old = 'public-mcp_previous'
                (root / 'rolling.json').write_text(json.dumps({'services': {
                    old: {'networks': {'proxy': ({'ipv4_address': old_ip} if old_ip else {})}}
                }}))
                obj = object.__new__(deploy.Deployment)
                obj.root = root
                obj.rolling = root / 'rolling.json'
                obj.state = {'services': {'public-mcp': old}}
                obj.config = {'project': 'fixture'}
                containers = {}
                if old_ip:
                    containers['old-id'] = {'Name': 'fixture-public-mcp_previous-1', 'IPv4Address': old_ip + '/24'}

                def command(args, output=None):
                    if args[:3] == ['docker', 'network', 'inspect']:
                        return json.dumps([{'Containers': containers}])
                    if args[:2] == ['docker', 'inspect']:
                        return json.dumps([{'Id': 'old-id', 'Config': {'Labels': {
                            'com.docker.compose.project': 'fixture',
                            'com.docker.compose.service': old}}}])
                    raise AssertionError(args)

                with patch.object(deploy, 'command', side_effect=command):
                    obj.public_mcp_preflight()
                self.assertEqual(obj.public_mcp_proxy_ip, expected)

    def test_public_mcp_proxy_preflight_rejects_foreign_or_exhausted_addresses(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            (root / '.env').write_text('PUBLIC_MCP_PROXY_IP=172.30.80.3\nPUBLIC_MCP_PROXY_IP_ALT=172.30.80.4\n')
            obj = object.__new__(deploy.Deployment)
            obj.root = root
            obj.rolling = root / 'missing-rolling.json'
            obj.state = {'services': {}}
            obj.config = {'project': 'fixture'}
            foreign = {'foreign': {'Name': 'fixture-redis-1', 'IPv4Address': '172.30.80.3/24'}}
            def foreign_command(args, output=None):
                if args[:3] == ['docker', 'network', 'inspect']:
                    return json.dumps([{'Containers': foreign}])
                if args[:2] == ['docker', 'inspect']:
                    return json.dumps([{'Id': 'foreign', 'Config': {'Labels': {
                        'com.docker.compose.project': 'fixture', 'com.docker.compose.service': 'redis'}}}])
                raise AssertionError(args)
            with patch.object(deploy, 'command', side_effect=foreign_command), \
                    self.assertRaisesRegex(RuntimeError, 'non-public-mcp'):
                obj.public_mcp_preflight()

            occupied = {
                'old-a': {'Name': 'fixture-public-mcp_a-1', 'IPv4Address': '172.30.80.3/24'},
                'old-b': {'Name': 'fixture-public-mcp_b-1', 'IPv4Address': '172.30.80.4/24'},
            }
            def full_command(args, output=None):
                if args[:3] == ['docker', 'network', 'inspect']:
                    return json.dumps([{'Containers': occupied}])
                if args[:2] == ['docker', 'inspect']:
                    return json.dumps([{'Id': item, 'Config': {'Labels': {
                        'com.docker.compose.project': 'fixture',
                        'com.docker.compose.service': 'public-mcp_' + item}}} for item in occupied])
                raise AssertionError(args)
            with patch.object(deploy, 'command', side_effect=full_command), \
                    self.assertRaisesRegex(RuntimeError, 'both reserved addresses'):
                obj.public_mcp_preflight()

    def test_public_mcp_trusted_proxies_use_two_host_routes(self):
        value = deploy.merge_public_mcp_trusted_proxy(None)
        self.assertIn('${PUBLIC_MCP_PROXY_IP:-172.30.80.3}/32', value)
        self.assertIn('${PUBLIC_MCP_PROXY_IP_ALT:-172.30.80.4}/32', value)
        self.assertNotIn('172.30.80.0/24', value)
        self.assertEqual(deploy.merge_public_mcp_trusted_proxy('192.0.2.1,192.0.2.1').count('192.0.2.1'), 1)

    def test_public_mcp_preflight_reclaims_failed_and_retained_project_peers(self):
        for running in [True, False]:
            with self.subTest(running=running), tempfile.TemporaryDirectory() as root:
                obj = object.__new__(deploy.Deployment)
                obj.root, obj.rolling = Path(root), Path(root) / 'rolling.json'
                obj.config = {'project': 'fixture', 'public_mcp_proxy_ips': ['172.30.80.3', '172.30.80.4']}
                obj.state = {'public_mcp_proxy_ip': '172.30.80.3', 'services': {'public-mcp': 'public-mcp_live'}}
                entries = {name: {'Name': 'fixture-' + name + '-1', 'IPv4Address': ip + '/24'}
                           for name, ip in [('live', '172.30.80.3'), ('failed', '172.30.80.4')]}
                details = [{'Id': name, 'State': {'Running': running if name == 'live' else False},
                            'Config': {'Labels': {'com.docker.compose.project': 'fixture',
                                                 'com.docker.compose.service': 'public-mcp_' + name}}}
                           for name in entries]
                disconnected = []
                def command(args):
                    if args[:3] == ['docker', 'network', 'inspect']:
                        return json.dumps([{'Containers': entries}])
                    if args[:2] == ['docker', 'inspect']:
                        return json.dumps(details)
                    self.assertEqual(args[:4], ['docker', 'network', 'disconnect', 'fixture_proxy'])
                    self.assertFalse(next(item for item in details if item['Id'] == args[4])['State']['Running'])
                    disconnected.append(args[4])
                    return ''
                with patch.object(deploy, 'command', side_effect=command):
                    obj.public_mcp_preflight()
                self.assertEqual(obj.public_mcp_proxy_ip, '172.30.80.4')
                self.assertEqual(set(disconnected), {'failed'} if running else {'live', 'failed'})

    def test_public_mcp_preflight_never_reclaims_foreign_or_unlabelled_peers(self):
        for labels in [{}, {'com.docker.compose.project': 'other', 'com.docker.compose.service': 'public-mcp_old'},
                       {'com.docker.compose.project': 'fixture', 'com.docker.compose.service': 'redis'}]:
            with self.subTest(labels=labels), tempfile.TemporaryDirectory() as root:
                obj = object.__new__(deploy.Deployment)
                obj.root = Path(root)
                obj.config = {'project': 'fixture', 'public_mcp_proxy_ips': ['172.30.80.3', '172.30.80.4']}
                responses = [json.dumps([{'Containers': {'foreign': {
                    'Name': 'fixture-public-mcp-old-1', 'IPv4Address': '172.30.80.4/24'}}}]),
                    json.dumps([{'Id': 'foreign', 'State': {'Running': False}, 'Config': {'Labels': labels}}])]
                with patch.object(deploy, 'command', side_effect=responses) as run, \
                        self.assertRaisesRegex(RuntimeError, 'non-public-mcp'):
                    obj.public_mcp_preflight()
                self.assertEqual(run.call_count, 2)

    def test_public_mcp_address_change_preserves_entries_without_ownership_evidence(self):
        value = '192.0.2.1,172.30.80.3/32,172.30.80.4/32'
        result = deploy.merge_public_mcp_trusted_proxy(value, ['172.30.82.3', '172.30.82.4'])
        self.assertEqual(result, value + ',172.30.82.3/32,172.30.82.4/32')

    def test_compose_up_restores_reclaimed_public_mcp_proxy_idempotently(self):
        for candidate, address_source in [(False, 'state'), (False, 'overlay'),
                                          (False, 'compose'), (True, 'compose')]:
            with self.subTest(candidate=candidate, address_source=address_source), tempfile.TemporaryDirectory() as root:
                obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
                service = (obj.services if candidate else obj.state['services'])['public-mcp']
                address = '172.30.80.4' if candidate else '172.30.80.3'
                if address_source == 'state':
                    obj.state['public_mcp_proxy_ip'] = address
                    obj.config['proxy_network'] = 'custom-proxy'
                elif address_source == 'overlay':
                    overlay = json.loads(obj.rolling.read_text())
                    overlay['services'][service]['networks']['proxy']['ipv4_address'] = address
                    obj.rolling.write_text(json.dumps(overlay))
                network = obj.config.get('proxy_network', 'fixture_proxy')
                aliases = ['public-mcp-proxy-' + ('candidate' if candidate else 'previous')]
                resolved = {'services': {service: {'networks': {'proxy': {
                    # A rollback's recorded address takes precedence over a changed base file.
                    'ipv4_address': '172.30.80.4' if address_source == 'state' else address,
                    'aliases': aliases}}}}}
                networks, events = {}, []
                up = ('up', '-d', '--no-deps', '--wait', '--wait-timeout', '240', service)

                def command(args):
                    if args[:2] == ['docker', 'compose']:
                        action = tuple(args[args.index('automation') + 1:])
                        events.append(action)
                        if action == up:
                            # Starting a retained container does not restore its endpoint.
                            return 'started'
                        if action == ('ps', '-q', service):
                            return 'target-id'
                        self.assertEqual(action, ('config', '--format', 'json'))
                        return json.dumps(resolved)
                    events.append(tuple(args))
                    if args[:2] == ['docker', 'inspect']:
                        self.assertEqual(args[2:], ['target-id'])
                        return json.dumps([{'Id': 'target-id', 'State': {'Running': True},
                                            'NetworkSettings': {'Networks': networks}}])
                    self.assertEqual(args, ['docker', 'network', 'connect', '--ip', address,
                                            '--alias', service, '--alias', *aliases, network, 'target-id'])
                    networks[network] = {'IPAddress': address}
                    return ''

                with patch.object(deploy, 'command', side_effect=command):
                    self.assertEqual(obj.compose(*up, candidate=candidate), 'started')
                    self.assertEqual(obj.compose(*up, candidate=candidate), 'started')
                connects = [event for event in events if event[:3] == ('docker', 'network', 'connect')]
                self.assertEqual(len(connects), 1)
                self.assertEqual(networks[network]['IPAddress'], address)
                self.assertLess(events.index(up), events.index(connects[0]))

    def test_compose_up_leaves_other_services_and_commands_alone(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {})
            calls = [
                (('up', '-d', '--no-deps', 'worker'), False),
                (('up', '-d', '--no-deps', obj.state['services']['mcp']), False),
                (('up', '-d', '--no-deps', obj.services['web']), True),
                (('stop', obj.services['public-mcp']), True),
            ]
            with patch.object(deploy, 'command', return_value='unchanged') as run:
                for args, candidate in calls:
                    self.assertEqual(obj.compose(*args, candidate=candidate), 'unchanged')
            self.assertEqual(run.call_count, len(calls))
            self.assertTrue(all(call.args[0][:2] == ['docker', 'compose'] for call in run.call_args_list))

    def test_public_mcp_proxy_restore_refuses_wrong_or_unrecorded_addresses(self):
        for attached in [False, True]:
            with self.subTest(attached=attached):
                obj = object.__new__(deploy.Deployment)
                obj.config = {'project': 'fixture'}
                resolved = {'services': {'public-mcp_new': {'networks': {'proxy': {
                    'ipv4_address': '172.30.80.4'} if attached else {}}}}}
                obj.compose = unittest.mock.Mock(side_effect=['target-id', json.dumps(resolved)])
                networks = {'fixture_proxy': {'IPAddress': '172.30.80.99'}} if attached else {}
                info = [{'Id': 'target-id', 'NetworkSettings': {'Networks': networks}}]
                message = 'unexpected fixed address' if attached else 'requires a recorded fixed address'
                with patch.object(deploy, 'command', return_value=json.dumps(info)) as run, \
                        self.assertRaisesRegex(RuntimeError, message):
                    obj.restore_public_mcp_proxy('public-mcp_new', candidate=True)
                run.assert_called_once_with(['docker', 'inspect', 'target-id'])

    def test_rollback_reconnects_public_mcp_before_switching_routes_and_retiring_candidates(self):
        for failure in [False, True]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as root:
                obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
                obj.state['public_mcp_proxy_ip'] = '172.30.80.3'
                service = obj.state['services']['public-mcp']
                obj.live_caddy.write_text('candidate route')
                obj._switch_phase = deploy.SWITCH_CADDY_RELOADED
                resolved = {'services': {service: {'networks': {'proxy': {
                    'ipv4_address': '172.30.80.3', 'aliases': ['public-mcp-proxy-previous']}}}}}
                events = []

                def command(args):
                    if args[:2] == ['docker', 'compose']:
                        action = tuple(args[args.index('automation') + 1:])
                        events.append(action)
                        if action == ('ps', '-q', service):
                            return 'old-id'
                        if action == ('config', '--format', 'json'):
                            return json.dumps(resolved)
                        self.assertIn(action[0], ('up', 'stop', 'rm'))
                        return ''
                    if args[:2] == ['docker', 'inspect']:
                        self.assertEqual(args[2:], ['old-id'])
                        return json.dumps([{'Id': 'old-id', 'NetworkSettings': {'Networks': {}}}])
                    if args[:3] == ['docker', 'network', 'connect']:
                        self.assertEqual(args, ['docker', 'network', 'connect', '--ip', '172.30.80.3',
                                                '--alias', service, '--alias', 'public-mcp-proxy-previous',
                                                'fixture_proxy', 'old-id'])
                        events.append('reconnect')
                        self.assertEqual(obj.live_caddy.read_text(), 'candidate route')
                        if failure:
                            raise RuntimeError('fixture reconnect failure')
                        return ''
                    self.assertEqual(args[:3], ['docker', 'ps', '-q'])
                    return 'candidate-id'

                with patch.object(deploy, 'command', side_effect=command), \
                        patch.object(obj, 'reload', side_effect=lambda: events.append('reload')) as reload, \
                        patch.object(obj, 'verify') as verify, \
                        patch.object(obj, 'verify_public_mcp', side_effect=lambda: events.append('handshake')) as handshake, \
                        patch.object(obj, 'update_monitoring') as monitoring, \
                        patch.object(obj, 'drain_retired', return_value=True), \
                        contextlib.redirect_stdout(io.StringIO()):
                    if failure:
                        with self.assertRaisesRegex(RuntimeError, 'degraded rollback') as error:
                            obj.rollback()
                        self.assertIn('fixture reconnect failure', str(error.exception.__cause__))
                        self.assertTrue(obj.degraded_path.is_file())
                        self.assertEqual(obj.live_caddy.read_text(), 'candidate route')
                        self.assertEqual(obj.env.read_bytes(), (obj.directory / 'release.env').read_bytes())
                        self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'rolling.compose.json').read_bytes())
                        for mock in [reload, verify, handshake, monitoring]:
                            mock.assert_not_called()
                        self.assertFalse(any(isinstance(event, tuple) and event[0] in ('stop', 'rm') for event in events))
                    else:
                        obj.rollback()
                        self.assertLess(events.index('reconnect'), events.index('reload'))
                        self.assertLess(events.index('reload'), events.index('handshake'))
                        stop = ('stop', '--timeout', '90', *obj.services.values())
                        remove = ('rm', '--force', '--stop', *obj.services.values())
                        self.assertLess(events.index('handshake'), events.index(stop))
                        self.assertLess(events.index(stop), events.index(remove))

    def test_rollback_keeps_previous_public_mcp_address_and_removes_candidate(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {'project': 'fixture', 'local_origin': 'http://127.0.0.1:80',
                          'public_mcp_host': 'web.example.test', 'checks': []}
            obj.state = {'sha': 'c' * 40, 'services': {'public-mcp': 'public-mcp_old'}}
            obj.services = {'public-mcp': 'public-mcp_new'}
            obj.directory = root
            obj.state_path = root / 'state.json'
            deploy.write_private(root / 'before-state.json', obj.state)
            obj.root, obj.release, obj.payload = root, 'gh-test', payload()
            obj.live_caddy, obj.env, obj.rolling = (root / name for name in ['live', 'env', 'rolling'])
            obj.live_caddy.write_text('candidate')
            (root / 'before-caddy').write_text('old')
            (root / 'before-release.env').write_text('old-env')
            previous_overlay = {'services': {'public-mcp_old': {'networks': {
                'proxy': {'ipv4_address': '172.30.80.3'}}}}}
            (root / 'before-rolling.json').write_text(json.dumps(previous_overlay))
            events = []
            obj.compose = lambda *args, **kwargs: events.append(args)
            obj.reload = lambda: events.append('reload')
            obj.verify = lambda expected=None: events.append(('verify', expected))
            obj.update_monitoring = lambda services: events.append('monitoring')
            obj.drain_retired = lambda *args, **kwargs: True
            requests = []
            urlopen = fake_public_mcp(requests)
            with patch.object(deploy, 'command', return_value='candidate-id'), \
                    patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen):
                obj.rollback()
            self.assertEqual(requests[:3], ['initialize', 'tools/list', 'tools/call'])
            self.assertLess(events.index('reload'), events.index('monitoring'))
            self.assertIn(('up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                           'public-mcp_old', 'worker'), events)
            self.assertIn(('stop', '--timeout', '90', 'public-mcp_new'), events)
            self.assertIn(('rm', '--force', '--stop', 'public-mcp_new'), events)
            self.assertEqual(json.loads((root / 'before-rolling.json').read_text()), previous_overlay)
            events.clear()
            with patch.object(deploy, 'command') as run, \
                    patch.object(deploy.urllib.request, 'urlopen', side_effect=RuntimeError('handshake failed')), \
                    patch.object(obj, 'mark_degraded') as marked, \
                    self.assertRaisesRegex(RuntimeError, 'handshake failed'):
                obj.rollback()
            run.assert_not_called()
            self.assertIn('verification', marked.call_args.args[0])
            self.assertNotIn('monitoring', events)
            self.assertFalse(any(isinstance(event, tuple) and event[0] in ('stop', 'rm') for event in events))

    def test_public_mcp_route_is_inserted_idempotently_and_excludes_www(self):
        source = ("http://{$WEB_DOMAIN} {\n"
                  "\timport security-web\n"
                  "\t@private path /agent-api /agent-api/*\n"
                  "\trespond @private 404\n"
                  "\treverse_proxy {\n\t\tdynamic a\n\t}\n"
                  "}\n"
                  "http://www.{$WEB_DOMAIN} {\n\treverse_proxy {\n\t}\n}\n")
        routed = deploy.install_public_mcp_route(source, 'public-mcp-proxy-gh-test')
        self.assertEqual(routed.count(deploy.PUBLIC_MCP_BEGIN), 1)
        self.assertIn('reverse_proxy public-mcp-proxy-gh-test:8790', routed)
        self.assertLess(routed.index(deploy.PUBLIC_MCP_END), routed.index('\treverse_proxy {'))
        www = routed[routed.index('http://www.{$WEB_DOMAIN}'):]
        self.assertNotIn(deploy.PUBLIC_MCP_BEGIN, www)
        self.assertEqual(deploy.install_public_mcp_route(routed, 'public-mcp-proxy-gh-test'), routed)

    def test_public_mcp_route_replaces_an_older_marked_block(self):
        source = "{$WEB_DOMAIN} {\n" + deploy.public_mcp_route('public-mcp-proxy-old') + "\treverse_proxy {\n}\n}\n"
        routed = deploy.install_public_mcp_route(source, 'public-mcp-proxy-new')
        self.assertEqual(routed.count(deploy.PUBLIC_MCP_BEGIN), 1)
        self.assertNotIn('public-mcp-proxy-old', routed)
        self.assertIn('public-mcp-proxy-new:8790', routed)

    def test_public_mcp_route_real_repo_and_unmarked_legacy_are_idempotent(self):
        source = Path(__file__).with_name('caddy').joinpath('Caddyfile').read_text()
        self.assertEqual(source.count(deploy.PUBLIC_MCP_BEGIN), 1)
        for legacy in [False, True]:
            with self.subTest(legacy=legacy):
                fixture = source
                if legacy:
                    fixture = fixture.replace('\t' + deploy.PUBLIC_MCP_BEGIN + '\n', '').replace(
                        '\t' + deploy.PUBLIC_MCP_END + '\n', '')
                routed = deploy.install_public_mcp_route(fixture, 'public-mcp-proxy-new')
                for _ in range(2):
                    self.assertEqual(routed.count('@publicMcp path /mcp'), 1)
                    self.assertEqual(routed.count('handle @publicMcp {'), 1)
                    self.assertEqual(routed.count('@publicMcpOther path /mcp/*'), 1)
                    self.assertEqual(routed.count(deploy.PUBLIC_MCP_BEGIN), 1)
                    self.assertNotIn('reverse_proxy public-mcp-proxy:8790', routed)
                    again = deploy.install_public_mcp_route(routed, 'public-mcp-proxy-new')
                    self.assertEqual(again, routed)
                    routed = again

    def _caddy_obj(self, root, live=GOOD_CADDY, backup=None):
        with contextlib.suppress(RuntimeError):
            self.real_staged_validation()
        obj = object.__new__(deploy.Deployment)
        obj.root = obj.directory = Path(root)
        (obj.root / 'deploy').mkdir(exist_ok=True)
        obj.config = {'caddy_container': 'fixture-caddy'}
        obj.release, obj.payload = 'fixture', {'sha': 'a' * 40}
        obj.live_caddy = obj.root / 'Caddyfile'
        obj.live_caddy.write_bytes(live)
        if backup is not None:
            (obj.root / 'Caddyfile.bak').write_bytes(backup)
        return obj

    def test_validate_live_caddy_leaves_files_when_validator_cannot_run(self):
        for mode in ['timeout', 'daemon', 'missing']:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as root:
                newer = GOOD_CADDY + b'# newer\n'
                obj = self._caddy_obj(root, live=newer, backup=GOOD_CADDY)
                fake = FakeCaddy(obj.live_caddy, good=(GOOD_CADDY, newer), validator=mode)
                with patch.object(deploy, 'command', side_effect=fake), \
                        contextlib.redirect_stdout(io.StringIO()) as output, \
                        self.assertRaises(deploy.CaddyValidatorUnavailable):
                    obj.validate_live_caddy()
                self.assertEqual(obj.live_caddy.read_bytes(), newer)
                self.assertNotIn('restoring', output.getvalue())

    def test_valid_new_caddy_is_never_replaced_by_stale_backup(self):
        with tempfile.TemporaryDirectory() as root:
            newer = GOOD_CADDY + b'# newer\n'
            obj = self._caddy_obj(root, live=newer, backup=GOOD_CADDY)
            fake = FakeCaddy(obj.live_caddy, good=(GOOD_CADDY, newer))
            with patch.object(deploy, 'command', side_effect=fake), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                obj.reload()
            self.assertEqual(obj.live_caddy.read_bytes(), newer)
            self.assertEqual(fake.events[-1], ('signal', newer))
            self.assertNotIn('restoring', output.getvalue())

    def test_invalid_live_caddy_restored_only_from_valid_backup(self):
        for backup in [GOOD_CADDY, TRUNCATED_CADDY, None]:
            with self.subTest(backup=backup), tempfile.TemporaryDirectory() as root:
                obj = self._caddy_obj(root, live=TRUNCATED_CADDY, backup=backup)
                inode = obj.live_caddy.stat().st_ino
                fake = FakeCaddy(obj.live_caddy)
                with patch.object(deploy, 'command', side_effect=fake), \
                        contextlib.redirect_stdout(io.StringIO()) as output:
                    if backup == GOOD_CADDY:
                        obj.validate_live_caddy()
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'missing' if backup is None else 'invalid too'):
                            obj.validate_live_caddy()
                self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY if backup == GOOD_CADDY else TRUNCATED_CADDY)
                self.assertEqual(obj.live_caddy.stat().st_ino, inode)
                if backup == GOOD_CADDY:
                    self.assertIn('restoring', output.getvalue())
                    self.assertIn('validation passed', output.getvalue())

    def test_interrupted_in_place_write_is_detected_even_if_it_validates(self):
        with tempfile.TemporaryDirectory() as root:
            prefix = b'{$WEB_DOMAIN} {\n}\n'
            obj = self._caddy_obj(root, live=prefix, backup=GOOD_CADDY)
            deploy.write_atomic(deploy.caddy_intent_path(obj.live_caddy),
                                (deploy.hashlib.sha256(GOOD_CADDY + b'# more\n').hexdigest() + '\n').encode())
            fake = FakeCaddy(obj.live_caddy, good=(GOOD_CADDY, prefix))
            with patch.object(deploy, 'command', side_effect=fake), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                obj.validate_live_caddy()
            self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY)
            self.assertIn('interrupted write', output.getvalue())
            self.assertFalse(deploy.caddy_intent_path(obj.live_caddy).exists())

    def test_completed_write_clears_intent_and_keeps_inode(self):
        with tempfile.TemporaryDirectory() as root:
            obj = self._caddy_obj(root, live=b'before')
            inode = obj.live_caddy.stat().st_ino
            newer = GOOD_CADDY + b'# newer\n'
            fake = FakeCaddy(obj.live_caddy, good=(newer,))
            with patch.object(deploy, 'command', side_effect=fake):
                obj.install_caddy(newer)
            self.assertEqual(obj.live_caddy.read_bytes(), newer)
            self.assertEqual(obj.live_caddy.stat().st_ino, inode)
            self.assertEqual((obj.root / 'Caddyfile.bak').read_bytes(), b'before')
            self.assertFalse(deploy.caddy_intent_path(obj.live_caddy).exists())
            self.assertEqual(list(obj.root.glob('.Caddyfile-*')), [])

    def test_install_caddy_invalid_or_unvalidated_candidate_never_touches_live(self):
        for mode in ['invalid', 'timeout', 'daemon']:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as root:
                obj = self._caddy_obj(root, live=GOOD_CADDY)
                before = obj.live_caddy.stat()
                fake = FakeCaddy(obj.live_caddy, validator='ok' if mode == 'invalid' else mode)
                with patch.object(deploy, 'command', side_effect=fake), self.assertRaises(RuntimeError):
                    obj.install_caddy(TRUNCATED_CADDY)
                self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY)
                self.assertEqual(obj.live_caddy.stat().st_mtime_ns, before.st_mtime_ns)
                self.assertEqual(list(obj.root.glob('.Caddyfile-*')), [])

    def test_install_caddy_partial_write_restores_backup(self):
        with tempfile.TemporaryDirectory() as root:
            obj = self._caddy_obj(root, live=GOOD_CADDY)
            newer = GOOD_CADDY + b'# newer\n'
            calls = []
            real = deploy.write_caddy_in_place
            def partial(path, content):
                calls.append(content)
                if len(calls) == 1:
                    deploy.write_atomic(deploy.caddy_intent_path(path), b'0' * 64 + b'\n')
                    path.write_bytes(b'partial')
                    raise OSError('fixture partial write')
                real(path, content)
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(GOOD_CADDY, newer))), \
                    patch.object(deploy, 'write_caddy_in_place', side_effect=partial), \
                    contextlib.redirect_stdout(io.StringIO()), \
                    self.assertRaisesRegex(OSError, 'fixture partial write'):
                obj.install_caddy(newer)
            self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY)

    def test_reload_signal_failure_with_valid_live_never_overwrites(self):
        for backup in [GOOD_CADDY, TRUNCATED_CADDY]:
            with self.subTest(backup=backup), tempfile.TemporaryDirectory() as root:
                newer = GOOD_CADDY + b'# newer\n'
                obj = self._caddy_obj(root, live=newer, backup=backup)
                (obj.root / 'before-caddy').write_bytes(GOOD_CADDY)
                before = obj.live_caddy.stat()
                fake = FakeCaddy(obj.live_caddy, good=(GOOD_CADDY, newer), signal_ok=False)
                with patch.object(deploy, 'command', side_effect=fake), \
                        contextlib.redirect_stdout(io.StringIO()) as output, \
                        self.assertRaisesRegex(RuntimeError, 'fixture reload failure'):
                    obj.reload()
                self.assertEqual(obj.live_caddy.read_bytes(), newer)
                self.assertEqual(obj.live_caddy.stat().st_mtime_ns, before.st_mtime_ns)
                self.assertEqual((obj.root / 'Caddyfile.bak').read_bytes(), backup)
                self.assertIn('left unchanged', output.getvalue())
                self.assertTrue(obj.degraded_path.exists())

    def test_install_caddy_partial_write_with_invalid_backup_aborts_without_overwrite(self):
        with tempfile.TemporaryDirectory() as root:
            obj = self._caddy_obj(root, live=TRUNCATED_CADDY)
            newer = GOOD_CADDY + b'# newer\n'
            def partial(path, content):
                deploy.write_atomic(deploy.caddy_intent_path(path), b'0' * 64 + b'\n')
                path.write_bytes(b'partial')
                raise OSError('fixture partial write')
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(newer,))), \
                    patch.object(deploy, 'write_caddy_in_place', side_effect=partial), \
                    contextlib.redirect_stdout(io.StringIO()), \
                    self.assertRaisesRegex(RuntimeError, 'invalid too'):
                obj.install_caddy(newer)
            self.assertEqual(obj.live_caddy.read_bytes(), b'partial')
    def _switched(self, root, before_caddy, phase):
        """Run apply()'s real switch steps up to `phase`, using the real install_caddy and reload.

        caddy-written: install succeeded but USR1 failed (Caddy may still run the previous release).
        caddy-reloaded: USR1 succeeded, the first verify() failed (env/rolling still previous).
        config-promoted: env/rolling promoted, a later step failed.
        """
        obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
        self._staged.stop()
        candidate_caddy = (obj.directory / 'Caddyfile.next').read_bytes()
        self.assertNotEqual(obj.live_caddy.read_bytes(), candidate_caddy)
        # Aliases carry the failed candidate's release suffix.
        self.assertIn(b'public-mcp-proxy-' + obj.release.encode(), candidate_caddy)
        obj.degraded_marker_written = False
        fake = FakeCaddy(obj.live_caddy, good=(obj.live_caddy.read_bytes(), candidate_caddy),
                         signal_ok=phase != deploy.SWITCH_CADDY_WRITTEN)
        with patch.object(deploy, 'command', side_effect=fake), contextlib.redirect_stdout(io.StringIO()):
            obj.install_caddy(candidate_caddy, before_write=lambda: obj.record_switch(deploy.SWITCH_CADDY_WRITING))
            obj.record_switch(deploy.SWITCH_CADDY_WRITTEN)
            try:
                obj.reload()
                obj.record_switch(deploy.SWITCH_CADDY_RELOADED)
            except RuntimeError:
                self.assertEqual(phase, deploy.SWITCH_CADDY_WRITTEN)
        if phase == deploy.SWITCH_CONFIG_PROMOTED:
            obj.env.write_bytes((obj.directory / 'release.env').read_bytes())
            obj.rolling.write_bytes((obj.directory / 'rolling.compose.json').read_bytes())
            obj.record_switch(deploy.SWITCH_CONFIG_PROMOTED)
        self.assertEqual(obj.switch_phase(), phase)
        self.assertEqual(json.loads(obj.state_path.read_text())['switch']['phase'], phase)
        if before_caddy is not None:  # None keeps prepare()'s real snapshot of the previous route
            (obj.directory / 'before-caddy').write_bytes(before_caddy)
        obj.compose = unittest.mock.Mock()
        return obj, candidate_caddy

    def assert_retry_prepares_from(self, obj, release, services, caddy):
        """After a manual marker removal, prepare() must derive aliases from one coherent release."""
        obj.degraded_path.unlink()
        retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
        self.assertEqual(retry.state['services'], services)
        with patch.object(deploy, 'command', return_value='fixture-container'):
            retry.prepare(archive())
        self.assertEqual((retry.directory / 'before-caddy').read_bytes(), caddy)
        routes = (retry.directory / 'Caddyfile.next').read_text()
        self.assertIn('public-mcp-proxy-' + retry.release, routes)
        self.assertNotIn(release, routes)
        overlay = json.loads((retry.directory / 'rolling.compose.json').read_text())
        public = overlay['services'][retry.services['public-mcp']]
        self.assertTrue(all(alias.endswith(retry.release) for alias in public['networks']['proxy']['aliases']))
        self.assertIn(services['public-mcp'], overlay['services'])  # kept as the rollback target

    def test_rollback_abort_follows_recorded_switch_phase(self):
        aborts = {
            'invalid before-caddy': (TRUNCATED_CADDY, 'ok', None, RuntimeError),
            'validator exit 125': (GOOD_CADDY, 'daemon', None, deploy.CaddyValidatorUnavailable),
            'validator timeout': (GOOD_CADDY, 'timeout', None, deploy.CaddyValidatorUnavailable),
            'before-caddy unreadable': (GOOD_CADDY, 'ok', 'read', OSError),
            'docker cp failure': (GOOD_CADDY, 'ok', 'cp', RuntimeError),
        }
        process = {
            deploy.SWITCH_CADDY_WRITTEN: 'may still run the previous release',
            deploy.SWITCH_CADDY_RELOADED: 'USR1 sent to Caddy; routing not verified',
            deploy.SWITCH_CONFIG_PROMOTED: 'USR1 sent to Caddy; routing not verified',
        }
        for label, (before_caddy, mode, inject, error) in aborts.items():
            for phase, running in process.items():
                with self.subTest(label, phase=phase), tempfile.TemporaryDirectory() as root:
                    obj, candidate_caddy = self._switched(root, before_caddy, phase)
                    fake = FakeCaddy(obj.live_caddy, good=(candidate_caddy, GOOD_CADDY), validator=mode)
                    def command(args):
                        if inject == 'cp' and args[:2] == ['docker', 'cp']:
                            raise RuntimeError('command failed: docker (exit 1); inspect privately on host')
                        return fake(args)
                    if inject == 'read':
                        (obj.directory / 'before-caddy').unlink()
                    with patch.object(deploy, 'command', side_effect=command), \
                            contextlib.redirect_stdout(io.StringIO()) as out, self.assertRaises(error) as raised:
                        obj.rollback()
                    # Live file, env, rolling and state all name the candidate release.
                    self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
                    self.assertEqual(obj.env.read_bytes(), (obj.directory / 'release.env').read_bytes())
                    self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'rolling.compose.json').read_bytes())
                    state = json.loads(obj.state_path.read_text())
                    self.assertEqual((state['release'], state['services'], state['unverified']),
                                     (obj.release, obj.services, True))
                    self.assertEqual(state['switch']['phase'], phase)
                    reason = json.loads(obj.degraded_path.read_text())['reason']
                    self.assertIn(running, reason)
                    self.assertIn('all name the candidate release', reason)
                    self.assertIn(f'switch phase {phase}', reason)
                    if phase == deploy.SWITCH_CADDY_WRITTEN:
                        self.assertNotIn('runs the candidate', reason)
                    if inject == 'read':
                        self.assertIn('before-caddy could not be read (FileNotFoundError)', reason)
                    self.assertIn(reason, str(raised.exception) + out.getvalue())
                    obj.compose.assert_not_called()
                    self.assertFalse(any(event[0] == 'signal' for event in fake.events))
                    with self.assertRaisesRegex(RuntimeError, 'blocked by persistent degraded marker'):
                        obj.apply(None)
                    self.assert_retry_prepares_from(obj, obj.release, obj.services, candidate_caddy)
                    self._staged.start()

    def test_rollback_abort_before_any_switch_keeps_everything_previous(self):
        # install_caddy rejected the candidate (or failed) before the live write began.
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
            self._staged.stop()
            obj.record_switch(deploy.SWITCH_NONE)
            previous_caddy = obj.live_caddy.read_bytes()
            env, rolling, services = obj.env.read_bytes(), obj.rolling.read_bytes(), dict(obj.state['services'])
            obj.compose = unittest.mock.Mock()
            obj.degraded_marker_written = False
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, validator='daemon')), \
                    contextlib.redirect_stdout(io.StringIO()), self.assertRaises(deploy.CaddyValidatorUnavailable):
                obj.rollback()
            self.assertEqual((obj.live_caddy.read_bytes(), obj.env.read_bytes(), obj.rolling.read_bytes()),
                             (previous_caddy, env, rolling))
            self.assertEqual(json.loads(obj.state_path.read_text())['services'], services)
            reason = json.loads(obj.degraded_path.read_text())['reason']
            self.assertIn('the Caddy process runs the previous release', reason)
            self.assertIn('all name the previous release', reason)
            self.assert_retry_prepares_from(obj, obj.release, services, previous_caddy)

    def test_rollback_abort_during_caddy_writing_reports_live_as_unknown(self):
        # The live write completed (live really is the candidate) but the phase never advanced.
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
            self._staged.stop()
            obj.state['sha'] = 'c' * 40
            deploy.write_private(obj.state_path, obj.state)
            deploy.write_private(obj.directory / 'before-state.json', obj.state)
            candidate_caddy = (obj.directory / 'Caddyfile.next').read_bytes()
            previous_caddy = obj.live_caddy.read_bytes()
            deploy.write_atomic(obj.live_caddy.with_name('Caddyfile.bak'), previous_caddy)  # as install_caddy does
            obj.record_switch(deploy.SWITCH_CADDY_WRITING)
            deploy.write_caddy_in_place(obj.live_caddy, candidate_caddy)
            env, rolling = obj.env.read_bytes(), obj.rolling.read_bytes()
            obj.compose = unittest.mock.Mock()
            obj.degraded_marker_written = False
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, validator='daemon')), \
                    contextlib.redirect_stdout(io.StringIO()), self.assertRaises(deploy.CaddyValidatorUnavailable):
                obj.rollback()
            self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
            self.assertEqual((obj.env.read_bytes(), obj.rolling.read_bytes()), (env, rolling))
            state = json.loads(obj.state_path.read_text())
            self.assertEqual((state['sha'], state['services'], state['unverified']),
                             ('c' * 40, obj.state['services'], True))
            reason = json.loads(obj.degraded_path.read_text())['reason']
            self.assertIn('live.caddy content is unknown (may be the candidate, partially written or the '
                          'previous release) and needs manual verification', reason)
            self.assertNotIn('all name the previous release', reason)
            # Retrying the old sha after a manual marker removal must not take the shortcut.
            obj.degraded_path.unlink()
            retry = deploy.Deployment(obj.config, {**payload(), 'sha': 'c' * 40, 'run_id': '124'})
            retry.validate_live_caddy = unittest.mock.Mock()
            retry.verify = unittest.mock.Mock()
            retry.cleanup = unittest.mock.Mock()
            retry.capacity_gate = unittest.mock.Mock(side_effect=RuntimeError('full deployment path'))
            with contextlib.redirect_stdout(io.StringIO()) as out, \
                    patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(previous_caddy,))), \
                    self.assertRaisesRegex(RuntimeError, 'full deployment path'):
                retry.apply(archive())
            self.assertNotIn('already deployed', out.getvalue())
            self.assertEqual(obj.live_caddy.read_bytes(), previous_caddy)  # reconciled from Caddyfile.bak
            retry.verify.assert_not_called()
            retry.cleanup.assert_not_called()

    def retry_after_interrupt(self, obj, good):
        """A later run: reconcile, validate live, then the real prepare(); returns its Caddyfile.next."""
        retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
        with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=good)), \
                contextlib.redirect_stdout(io.StringIO()):
            retry.reconcile_interrupted_switch()
            retry.validate_live_caddy()
        with patch.object(deploy, 'command', return_value='fixture-container'):
            retry.prepare(archive())
        routes = (retry.directory / 'Caddyfile.next').read_text()
        releases = set(re.findall(r'gh-[0-9a-f]{12}-\d+-\d+', routes))
        self.assertEqual(releases, {retry.release})                      # only -124-1
        self.assertNotIn(obj.release, routes)                            # no failed -123-1
        self.assertIsNone(re.search(r'-\d+-\d+-gh-', routes))            # no spliced aliases
        overlay = json.loads((retry.directory / 'rolling.compose.json').read_text())
        for kind, name in retry.services.items():
            for options in overlay['services'][name].get('networks', {}).values():
                for alias in (options or {}).get('aliases', []):
                    self.assertTrue(alias.endswith(retry.release), alias)
                    self.assertNotIn(obj.release, alias)
        return retry

    def _writing_interrupted(self, root):
        obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
        self._staged.stop()
        previous_caddy = obj.live_caddy.read_bytes()
        candidate_caddy = (obj.directory / 'Caddyfile.next').read_bytes()
        obj.degraded_marker_written = False
        fake = FakeCaddy(obj.live_caddy, good=(previous_caddy, candidate_caddy))
        # The complete candidate reached live, then the run stopped before recording caddy-written.
        with patch.object(deploy, 'command', side_effect=fake), \
                patch.object(obj, 'record_switch', wraps=obj.record_switch) as record:
            obj.install_caddy(candidate_caddy, before_write=lambda: obj.record_switch(deploy.SWITCH_CADDY_WRITING))
        record.assert_called_once_with(deploy.SWITCH_CADDY_WRITING)
        self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
        return obj, previous_caddy, candidate_caddy

    def test_retry_after_caddy_writing_restores_backup_and_prepares_clean_aliases(self):
        for aborted in (True, False):  # aborted rollback vs. process killed with no marker
            with self.subTest(aborted=aborted), tempfile.TemporaryDirectory() as root:
                obj, previous_caddy, candidate_caddy = self._writing_interrupted(root)
                if aborted:
                    obj.compose = unittest.mock.Mock()
                    with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, validator='daemon')), \
                            contextlib.redirect_stdout(io.StringIO()), self.assertRaises(deploy.CaddyValidatorUnavailable):
                        obj.rollback()
                    obj.degraded_path.unlink()  # manual removal
                self.assertFalse(obj.degraded_path.exists())
                retry = self.retry_after_interrupt(obj, good=(previous_caddy,))
                self.assertEqual(obj.live_caddy.read_bytes(), previous_caddy)
                self.assertEqual(retry.state['services'], json.loads(
                    (obj.directory / 'before-state.json').read_text())['services'])
                self.assertTrue(retry.state['unverified'])
                self.assertNotIn('switch', retry.state)
                self._staged.start()

    def test_retry_after_caddy_writing_with_unusable_backup_marks_degraded(self):
        for problem in ('invalid', 'unavailable'):
            with self.subTest(problem=problem), tempfile.TemporaryDirectory() as root:
                obj, _, candidate_caddy = self._writing_interrupted(root)
                retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
                fake = FakeCaddy(obj.live_caddy, good=(candidate_caddy,),
                                 validator='ok' if problem == 'invalid' else 'daemon')
                with patch.object(deploy, 'command', side_effect=fake), \
                        contextlib.redirect_stdout(io.StringIO()), self.assertRaises(Exception):
                    retry.apply(archive())
                self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
                self.assertIn('could not be reconciled', json.loads(obj.degraded_path.read_text())['reason'])
                self._staged.start()

    def test_retry_after_killed_switch_adopts_candidate_and_prepares_clean_aliases(self):
        for phase in deploy.SWITCH_ROUTED_TO_CANDIDATE:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as root:
                obj, candidate_caddy = self._switched(root, GOOD_CADDY, phase)
                self.assertFalse(obj.degraded_path.exists() and phase != deploy.SWITCH_CADDY_WRITTEN)
                obj.degraded_path.unlink(missing_ok=True)  # killed: no abort marker (or removed)
                retry = self.retry_after_interrupt(obj, good=(candidate_caddy,))
                self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
                self.assertEqual(obj.env.read_bytes(), (obj.directory / 'release.env').read_bytes())
                self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'rolling.compose.json').read_bytes())
                self.assertEqual((retry.state['release'], retry.state['services'], retry.state['unverified']),
                                 (obj.release, obj.services, True))
                self._staged.start()

    def test_retry_accepts_legacy_switch_record_without_sha_or_services(self):
        """Intermediate #68 revisions wrote records with only release and phase; a retry must reconcile."""
        with tempfile.TemporaryDirectory() as root:
            obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            state = json.loads(obj.state_path.read_text())
            state['switch'] = {'release': obj.release, 'phase': deploy.SWITCH_CADDY_RELOADED}
            obj.state_path.write_text(json.dumps(state))
            (obj.directory / 'manifest.json').write_text(json.dumps({'sha': 'a' * 40}))
            obj.degraded_path.unlink(missing_ok=True)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(candidate_caddy,))), \
                    contextlib.redirect_stdout(io.StringIO()):
                retry.reconcile_interrupted_switch()
            self.assertEqual(retry.state['services'], obj.services)
            self.assertEqual(retry.state['release'], obj.release)
            self.assertEqual(retry.state['sha'], 'a' * 40)
            self.assertTrue(retry.state['unverified'])
            self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)

    def test_retry_after_rollback_killed_at_each_point_restores_previous(self):
        class Killed(BaseException):
            """SIGKILL stand-in: no except-Exception handler runs and no marker is written."""

        def kill(*args, **kwargs):
            raise Killed()
        points = ['after rolling-back record', 'between env and rolling', 'before reload',
                  'after reload', 'after verify']
        for point in points:
            with self.subTest(point=point), tempfile.TemporaryDirectory() as root:
                obj, candidate_caddy = self._switched(root, None, deploy.SWITCH_CONFIG_PROMOTED)
                previous_caddy = (obj.directory / 'before-caddy').read_bytes()
                obj.verify = unittest.mock.Mock()
                obj.verify_public_mcp = unittest.mock.Mock()
                obj.update_monitoring = unittest.mock.Mock()
                original = Path.write_bytes
                def write_bytes(path, data):
                    if point == 'between env and rolling' and path == obj.rolling:
                        raise Killed()
                    return original(path, data)
                if point == 'after rolling-back record':
                    obj.compose = unittest.mock.Mock(side_effect=kill)
                elif point == 'before reload':
                    obj.reload = unittest.mock.Mock(side_effect=kill)
                elif point == 'after reload':
                    obj.verify.side_effect = kill
                elif point == 'after verify':
                    obj.update_monitoring.side_effect = kill
                with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(candidate_caddy, previous_caddy))), \
                        patch.object(Path, 'write_bytes', write_bytes), \
                        contextlib.redirect_stdout(io.StringIO()), self.assertRaises(Killed):
                    obj.rollback()
                self.assertFalse(obj.degraded_path.exists())
                self.assertEqual(json.loads(obj.state_path.read_text())['switch']['phase'], deploy.SWITCH_ROLLING_BACK)
                retry = self.retry_after_interrupt(obj, good=(candidate_caddy, previous_caddy))
                previous = json.loads((obj.directory / 'before-state.json').read_text())
                self.assertEqual(obj.live_caddy.read_bytes(), previous_caddy)
                self.assertEqual(obj.env.read_bytes(), (obj.directory / 'before-release.env').read_bytes())
                self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'before-rolling.json').read_bytes())
                self.assertEqual((retry.state['services'], retry.state['unverified']), (previous['services'], True))
                self.assertNotIn('switch', retry.state)
                self._staged.start()

    def test_retry_after_rolling_back_with_unusable_previous_caddy_marks_degraded(self):
        with tempfile.TemporaryDirectory() as root:
            obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CONFIG_PROMOTED)
            with patch.object(deploy, 'command', return_value=''):
                obj.record_rollback()
            (obj.directory / 'before-caddy').write_bytes(TRUNCATED_CADDY)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(candidate_caddy,))), \
                    contextlib.redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
                retry.apply(archive())
            self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
            self.assertIn('before-caddy failed caddy validate', json.loads(obj.degraded_path.read_text())['reason'])

    def test_private_state_write_is_atomic(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {})
            before = obj.state_path.read_bytes()
            with patch.object(deploy.os, 'replace', side_effect=OSError(28, 'No space left on device')), \
                    self.assertRaises(OSError):
                deploy.write_private(obj.state_path, {'services': {}})
            self.assertEqual(obj.state_path.read_bytes(), before)
            self.assertEqual(list(obj.state_path.parent.glob('.github-state.json-*')), [])
            with patch.object(deploy.os, 'fsync', side_effect=OSError(5, 'I/O error')), self.assertRaises(OSError):
                deploy.write_private(obj.degraded_path, {'reason': 'x'})
            self.assertFalse(obj.degraded_path.exists())
            again = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            self.assertEqual(again.state, json.loads(before))
            deploy.write_private(obj.state_path, again.state)
            self.assertEqual(obj.state_path.stat().st_mode & 0o777, 0o600)

    def test_rollback_abort_with_unrecorded_phase_reports_files_as_found(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
            self._staged.stop()
            obj.live_caddy.write_bytes(b'partial')
            env = obj.env.read_bytes()
            obj.degraded_marker_written = False
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, validator='timeout')), \
                    contextlib.redirect_stdout(io.StringIO()), self.assertRaises(deploy.CaddyValidatorUnavailable):
                obj.rollback()
            self.assertEqual(obj.env.read_bytes(), env)
            reason = json.loads(obj.degraded_path.read_text())['reason']
            self.assertIn('switch phase is unknown', reason)
            self.assertIn('left as found', reason)

    def test_rollback_alignment_write_failures_are_reported_in_the_marker(self):
        for failing in ['env', 'rolling', 'state_path']:
            with self.subTest(failing=failing), tempfile.TemporaryDirectory() as root:
                obj, candidate_caddy = self._switched(root, TRUNCATED_CADDY, deploy.SWITCH_CADDY_RELOADED)
                target = getattr(obj, failing)
                original, original_atomic = Path.write_bytes, deploy.write_atomic
                def write_bytes(path, data):
                    if path == target:
                        raise OSError(28, 'No space left on device')
                    return original(path, data)
                def write_atomic(path, data, mode=None):
                    if path == target:
                        raise OSError(28, 'No space left on device')
                    return original_atomic(path, data, mode)
                with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(candidate_caddy,))), \
                        patch.object(Path, 'write_bytes', write_bytes), \
                        patch.object(deploy, 'write_atomic', write_atomic), \
                        contextlib.redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
                    obj.rollback()
                reason = json.loads(obj.degraded_path.read_text())['reason']
                self.assertIn(f'aligning {target.name} (OSError) failed', reason)
                self.assertIn('may hold either release', reason)
                with self.assertRaisesRegex(RuntimeError, 'blocked by persistent degraded marker'):
                    obj.apply(None)
                self._staged.start()

    def test_rollback_restore_write_failure_realigns_and_blocks_next_apply(self):
        for failing in ['env', 'rolling', 'state_path']:
            with self.subTest(failing=failing), tempfile.TemporaryDirectory() as root:
                obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
                target = getattr(obj, failing)
                fails = {'left': 1}
                original_bytes, original_text, original_atomic = Path.write_bytes, Path.write_text, deploy.write_atomic
                def failing_once(original):
                    def write(path, data, *rest, **options):
                        if path == target and fails['left']:
                            fails['left'] -= 1
                            raise OSError(28, 'No space left on device')
                        return original(path, data, *rest, **options)
                    return write
                with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy, good=(candidate_caddy, GOOD_CADDY))), \
                        patch.object(Path, 'write_bytes', failing_once(original_bytes)), \
                        patch.object(Path, 'write_text', failing_once(original_text)), \
                        patch.object(deploy, 'write_atomic', failing_once(original_atomic)), \
                        contextlib.redirect_stdout(io.StringIO()), self.assertRaises(OSError):
                    obj.rollback()
                # The restore failed part-way; alignment put everything back on the candidate.
                self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)
                self.assertEqual(obj.env.read_bytes(), (obj.directory / 'release.env').read_bytes())
                self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'rolling.compose.json').read_bytes())
                self.assertEqual(json.loads(obj.state_path.read_text())['services'], obj.services)
                obj.compose.assert_not_called()
                reason = json.loads(obj.degraded_path.read_text())['reason']
                self.assertIn('restoring previous env/rolling/state failed', reason)
                self.assertIn('all name the candidate release', reason)
                with self.assertRaisesRegex(RuntimeError, 'blocked by persistent degraded marker'):
                    obj.apply(None)
                self._staged.start()

    def test_rollback_with_real_validation_and_valid_before_caddy_succeeds(self):
        with tempfile.TemporaryDirectory() as root:
            obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CONFIG_PROMOTED)
            fake = FakeCaddy(obj.live_caddy, good=(candidate_caddy, GOOD_CADDY))
            obj.verify = unittest.mock.Mock()
            obj.verify_public_mcp = unittest.mock.Mock()
            obj.update_monitoring = unittest.mock.Mock()
            obj.drain_retired = lambda *args, **kwargs: True
            with patch.object(deploy, 'command', side_effect=fake), contextlib.redirect_stdout(io.StringIO()):
                obj.rollback()
            self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY)
            self.assertEqual(obj.env.read_bytes(), (obj.directory / 'before-release.env').read_bytes())
            self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'before-rolling.json').read_bytes())
            self.assertEqual(json.loads(obj.state_path.read_text()),
                             json.loads((obj.directory / 'before-state.json').read_text()))
            staged = [event for event in fake.events if event[0] == 'validate' and event[1] != '/etc/caddy/Caddyfile']
            self.assertEqual(staged[-1][2], GOOD_CADDY)  # the real staged check ran, not the setUp stub
            self.assertIn(('signal', GOOD_CADDY), fake.events)
            self.assertFalse(obj.degraded_path.exists())

    def test_public_mcp_subpath_probe_does_not_follow_redirects(self):
        import http.server
        import threading

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(301 if self.path == '/mcp/x' else 404)
                if self.path == '/mcp/x':
                    self.send_header('Location', '/missing')
                self.send_header('Content-Length', '0')
                self.end_headers()

            def log_message(self, *args):
                pass
        server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        origin = f'http://127.0.0.1:{server.server_port}'
        patch.stopall()  # use the real no-redirect opener
        with self.assertRaises(deploy.urllib.error.HTTPError) as raised:
            deploy.open_without_redirects(deploy.urllib.request.Request(origin + '/mcp/x'), timeout=5)
        self.assertEqual(raised.exception.code, 301)
        # Through verify_public_mcp, the 404 behind the redirect must not count as a pass.
        obj = deploy.Deployment.__new__(deploy.Deployment)
        obj.config = {'local_origin': origin, 'public_mcp_host': 'opennavo.com', 'checks': []}
        real_open = deploy.open_without_redirects
        fake = fake_public_mcp()
        # JSON-RPC POSTs go to the fake MCP; only the GET probe uses the real no-redirect opener.
        with patch.object(deploy, 'open_without_redirects',
                          side_effect=lambda request, timeout: fake(request, timeout) if request.data is not None
                          else real_open(request, timeout)), \
                self.assertRaises(deploy.RouteCheckError) as raised:
            obj.verify_public_mcp()
        self.assertEqual(raised.exception.status, 301)

    def test_public_mcp_route_requires_one_web_block_and_catch_all_proxy(self):
        for source in ['reverse_proxy {\n}\n', '{$WEB_DOMAIN} {\nreverse_proxy web:3000\n}\n']:
            with self.subTest(source=source), self.assertRaises(ValueError):
                deploy.install_public_mcp_route(source, 'public-mcp-proxy-new')

    def test_verify_public_mcp_handshake_propagates_session_and_requires_tools(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80',
                      'public_mcp_host': 'opennavo.com', 'checks': []}
        responses = []

        def urlopen(request, timeout):
            responses.append(request)
            return inner(request, timeout)
        inner = fake_public_mcp(seen := [])

        with patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen):
            obj.verify_public_mcp()
        self.assertEqual(seen, ['initialize', 'tools/list', 'tools/call',
                                ('GET', 'http://127.0.0.1:80/mcp/x')])
        self.assertEqual(json.loads(responses[2].data)['params'], {'name': 'list_categories', 'arguments': {}})
        self.assertEqual(responses[0].get_header('Host'), 'opennavo.com')
        self.assertEqual(responses[1].get_header('Mcp-session-id'), 'session-1')
        self.assertEqual(responses[0].get_header('Accept'), 'application/json, text/event-stream')

        class Response(io.BytesIO):
            status, headers = 200, {}
        with patch.object(deploy.urllib.request, 'urlopen', side_effect=[
                Response(b'{"result":{}}'), Response(b'{"result":{"tools":[]}}')]):
            with self.assertRaisesRegex(RuntimeError, 'no tools'):
                obj.verify_public_mcp()

    def test_verify_public_mcp_fails_when_api_base_or_trust_is_misconfigured(self):
        obj = deploy.Deployment.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        broken = {
            'tool error (API base unreachable)': {'result': {'isError': True, 'content': [
                {'type': 'text', 'text': 'fetch failed: ECONNREFUSED'}]}},
            'JSON-RPC error (trust rejected)': {'error': {'code': -32603, 'message': 'forbidden'}},
            'empty content': {'result': {'content': []}},
            'blank text': {'result': {'content': [{'type': 'text', 'text': '  '}]}},
        }
        for label, body in broken.items():
            with self.subTest(label), patch.object(deploy.urllib.request, 'urlopen',
                                                   side_effect=fake_public_mcp(call_result=body)):
                with self.assertRaises(RuntimeError):
                    obj.verify_public_mcp()
        for status in (200, 301, 502):
            with self.subTest(subpath=status), patch.object(
                    deploy.urllib.request, 'urlopen', side_effect=fake_public_mcp(subpath_status=status)), \
                    self.assertRaises(deploy.RouteCheckError) as raised:
                obj.verify_public_mcp()
            # A non-404 must reach the deploy log with its status, not just an exception type name.
            self.assertEqual(raised.exception.status, status)
            self.assertIn('expected 404 from the public-MCP sub-path handle', str(raised.exception))

    def test_first_public_mcp_uses_the_new_versioned_api_proxy_alias(self):
        # Production hosts only expose versioned aliases; the base Compose api-proxy name does not resolve.
        with tempfile.TemporaryDirectory() as root:
            obj, candidate = prepare_on_host(root, {}, api_proxy_alias='api-proxy-gh-old-1')
            api_alias = candidate['services'][obj.services['api']]['networks']['proxy']['aliases'][0]
            self.assertEqual(api_alias, 'api-proxy-' + obj.release)
            environment = candidate['services'][obj.services['public-mcp']]['environment']
            self.assertEqual(environment['OPENNAVO_PUBLIC_API_BASE'], f'http://{api_alias}:8080/api/v1')
        with tempfile.TemporaryDirectory() as root:
            obj, candidate = prepare_on_host(root, {}, include_public_mcp=True)
            environment = candidate['services'][obj.services['public-mcp']]['environment']
            self.assertEqual(environment['OPENNAVO_PUBLIC_API_BASE'],
                             f'http://api-proxy-{obj.release}:8080/api/v1')

    def test_verify_logs_public_mcp_failure_detail_and_caps_attempts(self):
        obj = object.__new__(deploy.Deployment)
        obj.payload = payload()
        obj.services = {'public-mcp': 'public-mcp_new'}
        obj.config = {'local_origin': 'http://user:pw@127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        posts = []

        def rejected(request, timeout):
            posts.append(request)
            fake_value = 'abcdef' + '0123456789'  # assembled so secret scanners do not flag the fixture
            body = io.BytesIO(('{"error":"rate_limited","access' + '_token":"' + fake_value + '","x":"'
                               + 'A' * 300 + '"}').encode())
            raise deploy.urllib.error.HTTPError(request.full_url, 429, 'Too Many', {'Retry-After': '7'}, body)
        output, sleeps = io.StringIO(), []
        with patch.object(deploy, 'open_without_redirects', side_effect=rejected), \
                patch.object(deploy.time, 'sleep', side_effect=sleeps.append), \
                contextlib.redirect_stdout(output), \
                self.assertRaisesRegex(RuntimeError, r'route checks failed: POST initialize '
                                                     r'http://127\.0\.0\.1:80/mcp \(Host opennavo\.com\) -> 429'):
            obj.verify()
        log = output.getvalue()
        self.assertEqual(len(posts), deploy.PUBLIC_MCP_VERIFY_ATTEMPTS)
        self.assertEqual(posts[0].get_header('User-agent'), deploy.VERIFY_USER_AGENT)
        self.assertEqual(log.count('Post-deployment check attempt'), 1)  # identical failures are logged once
        self.assertIn('rate_limited', log)
        self.assertNotIn('abcdef' + '0123456789', log)
        self.assertNotIn('pw@', log)
        self.assertNotIn('A' * 250, log)
        self.assertIn('stopped after 5 attempts', log)
        self.assertEqual(sleeps, [7] * deploy.PUBLIC_MCP_VERIFY_ATTEMPTS)

    def test_verify_public_mcp_reports_tool_error_and_redirect(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        body = {'result': {'isError': True, 'content': [
            {'type': 'text', 'text': 'public_api_unavailable: the public API could not be reached'}]}}
        with patch.object(deploy, 'open_without_redirects', side_effect=fake_public_mcp(call_result=body)), \
                self.assertRaisesRegex(deploy.RouteCheckError, 'tools/call list_categories .* tool error.*public_api_unavailable'):
            obj.verify_public_mcp()

        def redirect(request, timeout):
            raise deploy.urllib.error.HTTPError(request.full_url, 308, 'Permanent', {'Location': 'https://opennavo.com/mcp?sig=s3cret'}, io.BytesIO(b''))
        with patch.object(deploy, 'open_without_redirects', side_effect=redirect), \
                self.assertRaises(deploy.RouteCheckError) as raised:
            obj.verify_public_mcp()
        self.assertIn('-> 308 Location https://opennavo.com/mcp;', str(raised.exception))
        self.assertNotIn('s3cret', str(raised.exception))

    def test_redact_preview_masks_tokens_and_limits_length(self):
        # JWT-shaped fixture assembled at runtime so secret scanners do not flag it.
        jwt_like = '.'.join(['h' * 12, 'p' * 12, 's' * 12])
        text = deploy.redact_preview(('Authorization: Bearer abc.def.ghi {"token":"t0k"} session_id=xyz '
                                      + jwt_like + ' ' + 'k' * 400).encode())
        for secret in ['abc.def.ghi', 't0k', 'xyz', jwt_like, 'k' * 40]:
            self.assertNotIn(secret, text)
        self.assertLessEqual(len(deploy.redact_preview(b'x ' * 500)), deploy.VERIFY_BODY_PREVIEW_BYTES)

    def test_monitoring_targets_preserves_other_jobs_and_follows_release_and_rollback(self):
        source = """global:
  scrape_interval: 15s
rule_files: [/etc/prometheus/rules.yml]
scrape_configs:
  - job_name: api
    static_configs:
      - targets: ['api_old:9090']
  - job_name: custom
    static_configs:
      - targets: ['custom:1234']
        labels: {purpose: custom}
"""
        after = deploy.monitoring_targets(source, {'api': 'api_new', 'web': 'web_new'})
        self.assertIn("targets: ['api_new:9090']", after)
        self.assertIn('dns_sd_configs:', after)
        self.assertIn('names: [web_new]', after)
        self.assertIn("targets: ['custom:1234']\n        labels: {purpose: custom}", after)
        self.assertEqual(after.count('job_name: web'), 1)
        self.assertEqual(deploy.monitoring_targets(after, {'api': 'api_new', 'web': 'web_new'}), after)
        restored = deploy.monitoring_targets(after, {'api': 'api_old', 'web': 'web_old'})
        self.assertNotIn('api_new', restored)
        self.assertIn('names: [web_old]', restored)
        with self.assertRaises(ValueError):
            deploy.monitoring_targets(source.replace('    static_configs:', '    relabel_configs:', 1), {'api': 'a', 'web': 'b'})
        with self.assertRaises(ValueError):
            deploy.monitoring_targets(source, {'api': 'a\nmalicious', 'web': 'b'})

    def test_monitoring_health_checks_dns_identity_and_all_replicas(self):
        def target(job, service, ip, health='up', dns=True):
            port = 9090 if job == 'api' else 9091
            return {'labels': {'job': job}, 'health': health,
                    'discoveredLabels': {'job': job, '__meta_dns_name': service} if dns else {'job': job},
                    'scrapeUrl': f'http://{ip if dns else service}:{port}/metrics'}

        good = [target('api', 'api_new', '172.20.0.2'), target('web', 'web_new', '172.20.0.3')]
        cases = [
            ('dns IP targets', good, 0),
            ('static targets', [target('api', 'api_new', '', dns=False), target('web', 'web_new', '', dns=False)], 0),
            ('multiple replicas', good + [target('web', 'web_new', '172.20.0.4')], 0),
            ('unhealthy replica', good + [target('web', 'web_new', '172.20.0.4', 'down')], 1),
            ('old release', [target('api', 'api_old', '172.20.0.2'), good[1]], 1),
            ('missing job', good[:1], 1),
            ('stale replica', good + [target('web', 'web_old', '172.20.0.4')], 1),
            ('static unhealthy replica', [target('api', 'api_new', '', dns=False),
                                          target('web', 'web_new', '', dns=False),
                                          target('web', 'web_new', '', 'down', dns=False)], 1),
            ('wrong port', [dict(good[0], scrapeUrl='http://172.20.0.2:9091/metrics'), good[1]], 1),
        ]
        for name, targets, expected in cases:
            with self.subTest(name=name):
                # Execute the actual deployed Node script; only HTTP and retry timing are replaced.
                prelude = ('globalThis.fetch = async () => ({json: async () => (' +
                           json.dumps({'status': 'success', 'data': {'activeTargets': targets}}) +
                           ')}); globalThis.setTimeout = (callback) => { callback(); };\n')
                result = subprocess.run(['node', '--input-type=module', '-e', prelude + deploy.MONITORING_HEALTH_SCRIPT,
                                         json.dumps({'api': 'api_new:9090', 'web': 'web_new:9091'})],
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, expected, result.stderr)

    def run_warm_script(self, page_statuses, ready_statuses=(200,), paths=('/apps/retired',), ready_timeout='60000'):
        """Execute the deployed warmup script with fetch/timers replaced; statuses are consumed in order."""
        prelude = """
const pageStatuses = %s;
const readyStatuses = %s;
let now = 0;
Date.now = () => now;
globalThis.setTimeout = (callback, ms) => { now += ms || 0; callback(); };
globalThis.fetch = async (url) => {
  const queue = String(url).endsWith('/readyz') ? readyStatuses : pageStatuses;
  const status = queue.length > 1 ? queue.shift() : queue[0];
  if (status === 'error') throw Object.assign(new Error('boom SECRET_ENV=leak'), {name: 'TypeError'});
  return { status, arrayBuffer: async () => new ArrayBuffer(0) };
};
""" % (json.dumps(list(page_statuses)), json.dumps(list(ready_statuses)))
        return subprocess.run(
            ['node', '--input-type=module', '-e', prelude + deploy.WARM_SCRIPT, json.dumps(list(paths)),
             deploy.WARM_ORIGIN, deploy.WARM_HOST, deploy.WARM_INTERNAL_ORIGIN, ready_timeout],
            capture_output=True, text=True, timeout=10
        )

    def test_candidate_warmup_records_retired_statuses_but_rejects_server_failures(self):
        result = self.run_warm_script([404, 410, 200])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('status=404', result.stdout)
        self.assertIn('status=410', result.stdout)
        failure = self.run_warm_script([503], paths=['/apps/broken'])
        self.assertEqual(failure.returncode, 1)

    def test_candidate_warmup_retries_first_pass_cold_start_failure_once(self):
        for first in (502, 'error'):
            with self.subTest(first=first):
                result = self.run_warm_script([first, 200], paths=['/'])
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('warm retry pass=1 path=/', result.stderr)
                self.assertIn('warm pass=2 path=/ status=200', result.stdout)

    def test_candidate_warmup_second_pass_stays_strict(self):
        # Pass 1 fails then recovers, pass 2 returns 5xx: no retry, deployment fails.
        result = self.run_warm_script([500, 200, 503, 200], paths=['/'])
        self.assertEqual(result.returncode, 1)
        self.assertNotIn('warm retry pass=2', result.stderr)
        self.assertIn('warm pass=2 path=/ status=503', result.stdout)
        failure = self.run_warm_script([500, 500], paths=['/'])
        self.assertEqual(failure.returncode, 1)
        self.assertNotIn('warm pass=2', failure.stdout)

    def test_candidate_warmup_waits_for_ready_and_times_out(self):
        result = self.run_warm_script([200], ready_statuses=[503, 'error', 200, 200], paths=['/'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('candidate ready wait attempts=3', result.stdout)
        timeout = self.run_warm_script([200], ready_statuses=[503], paths=['/'], ready_timeout='5000')
        self.assertEqual(timeout.returncode, 1)
        self.assertIn('candidate ready wait timed out', timeout.stderr)
        self.assertNotIn('warm pass=1', timeout.stdout)

    def warm_deployer(self, script_result):
        obj = deploy.Deployment.__new__(deploy.Deployment)
        obj.warm_paths = ['/']
        obj.candidate_webs = lambda: ['abcdef1234567890']
        calls = []
        def fake_run(args, **kwargs):
            calls.append(args)
            return subprocess.CompletedProcess(args, script_result.returncode,
                                               script_result.stdout.encode(), script_result.stderr.encode())
        return obj, calls, fake_run

    def test_warm_candidates_logs_script_output_on_failure_without_secrets(self):
        script = self.run_warm_script([503], paths=['/'])
        script = subprocess.CompletedProcess(script.args, script.returncode, script.stdout,
                                             script.stderr + 'node:internal stack TOKEN=supersecret\n')
        obj, calls, fake_run = self.warm_deployer(script)
        output = io.StringIO()
        with patch.object(deploy, 'command', return_value='healthy'), \
                patch.object(deploy.subprocess, 'run', fake_run), \
                contextlib.redirect_stdout(output):
            with self.assertRaisesRegex(RuntimeError, 'candidate warmup failed'):
                obj.warm_candidates()
        log = output.getvalue()
        self.assertIn('warmup stdout:', log)
        self.assertIn('warm pass=1 path=/ status=503', log)
        self.assertIn('warmup stderr:', log)
        self.assertIn('warm retry pass=1 path=/', log)
        self.assertIn('other line(s) hidden', log)
        self.assertNotIn('supersecret', log)
        self.assertEqual(calls[0][-1], str(deploy.WARM_READY_TIMEOUT_MS))

    def test_warm_candidates_passes_after_first_pass_retry(self):
        script = self.run_warm_script([500, 200], paths=['/'])
        obj, _, fake_run = self.warm_deployer(script)
        output = io.StringIO()
        with patch.object(deploy, 'command', return_value='healthy'), \
                patch.object(deploy.subprocess, 'run', fake_run), \
                contextlib.redirect_stdout(output):
            obj.warm_candidates()
        self.assertIn('All candidate web replicas warmed and ready.', output.getvalue())

    def test_warm_candidates_logs_retries_and_summary_on_success(self):
        script = self.run_warm_script([503, 200], paths=['/'])
        obj, _, fake_run = self.warm_deployer(script)
        output = io.StringIO()
        with patch.object(deploy, 'command', return_value='healthy'), \
                patch.object(deploy.subprocess, 'run', fake_run), \
                contextlib.redirect_stdout(output):
            obj.warm_candidates()
        log = output.getvalue()
        self.assertRegex(log, r'warm retry path=/ first=503 first_ms=\d+ retry=200 retry_ms=\d+')
        self.assertIn('warm summary: 1 first-pass retry across 1 path(s)', log)

        script = self.run_warm_script([200], paths=['/', '/zh'])
        obj, _, fake_run = self.warm_deployer(script)
        output = io.StringIO()
        with patch.object(deploy, 'command', return_value='healthy'), \
                patch.object(deploy.subprocess, 'run', fake_run), \
                contextlib.redirect_stdout(output):
            obj.warm_candidates()
        self.assertIn('warm summary: no retries (2 path(s), 2 pass(es))', output.getvalue())

    def test_warm_retry_summary_reports_request_errors(self):
        lines = deploy.warm_retry_summary(
            b'warm pass=1 path=/a status=200 ms=10\nwarm pass=1 path=/b status=200 ms=900\n',
            b'warm failed pass=1 path=/b: TypeError ms=12\nwarm retry pass=1 path=/b\nTOKEN=x\n')
        self.assertEqual(lines[0], 'warm retry path=/b first=TypeError first_ms=12 retry=200 retry_ms=900')
        self.assertTrue(all(line.startswith(deploy.WARM_OUTPUT_PREFIXES) for line in lines))

    def test_warm_output_truncates(self):
        text = deploy.warm_output(('warm pass=1 path=/x status=200 ms=1\n' * 500).encode())
        self.assertTrue(text.startswith('...(truncated)'))
        self.assertLessEqual(len(text), deploy.WARM_OUTPUT_LIMIT + 20)

    def test_drain_script_falls_back_to_the_existing_load_shed_metric(self):
        script = """
globalThis.fetch = async () => ({ ok: true, text: async () => '# HELP opennavo_web_in_flight\\nopennavo_web_in_flight 0\\n' });
""" + deploy.DRAIN_SCRIPT
        result = subprocess.run(
            ['node', '--input-type=module', '-e', script, deploy.WARM_INTERNAL_ORIGIN],
            capture_output=True, text=True, timeout=10
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), '0')

    def test_monitoring_default_dns_config_release_and_rollback(self):
        source = Path(__file__).with_name('prometheus').joinpath('prometheus.yml').read_text()
        after = deploy.monitoring_targets(source, {'api': 'api_new', 'web': 'web_new'})
        expected = source.replace('names: [api]', 'names: [api_new]').replace('names: [web]', 'names: [web_new]')
        self.assertEqual(after, expected)
        self.assertEqual(deploy.monitoring_targets(after, {'api': 'api_new', 'web': 'web_new'}), after)
        self.assertEqual(deploy.monitoring_targets(after, {'api': 'api', 'web': 'web'}), source)

    def test_monitoring_validation_or_scrape_failure_restores_config_without_leaking_it(self):
        for failure in ['validate', 'scrape', None]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as root:
                directory = Path(root)
                config = directory / 'prometheus.yml'
                original = "global:\n  scrape_interval: 15s\nscrape_configs:\n  - job_name: api\n    static_configs:\n      - targets: ['api_old:9090']\n"
                config.write_text(original)
                inode = config.stat().st_ino
                obj = object.__new__(deploy.Deployment)
                obj.directory, obj.config = directory, {'project': 'example-prod'}
                def command(args):
                    if args[1] == 'ps': return 'monitor'
                    if args[1] == 'inspect':
                        return json.dumps([{'Mounts': [{'Source': str(config), 'Destination': '/etc/prometheus/prometheus.yml', 'Type': 'bind'}]}])
                    if 'promtool' in args:
                        self.assertIn('names: [web_new]', config.read_text())
                        if failure == 'validate': raise RuntimeError('validation failed')
                    if 'node' in args and failure == 'scrape': raise RuntimeError('scrape failed')
                    return ''
                with patch.object(deploy, 'command', side_effect=command) as run:
                    if failure:
                        with self.assertRaises(RuntimeError):
                            obj.update_monitoring({'api': 'api_new', 'web': 'web_new'})
                        self.assertEqual(config.read_text(), original)
                    else:
                        obj.update_monitoring({'api': 'api_new', 'web': 'web_new'})
                        self.assertIn('names: [web_new]', config.read_text())
                        self.assertTrue(any('node' in call.args[0] for call in run.call_args_list))
                    self.assertEqual(config.stat().st_ino, inode)
                    self.assertEqual((directory / 'before-prometheus.yml').read_text(), original)
                    self.assertTrue(any(call.args[0][:3] == ['docker', 'kill', '--signal=HUP'] for call in run.call_args_list))

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
            if args[1] == 'exec': return '0'
            return ''
        with patch.object(deploy, 'command', side_effect=cmd) as run:
            obj.cleanup(previous)
        mutations = [call.args[0] for call in run.call_args_list if call.args[0][1] in ['stop', 'rm']]
        self.assertEqual(mutations, [
            ['docker', 'stop', '--timeout', '90', *previous['services'].values(), 'api_older', 'api'],
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

    def test_cleanup_stops_undrained_containers_by_default(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'project': 'example-prod'}
        obj.services = {'api': 'api_new', 'web': 'web_new', 'mcp': 'mcp_new', 'admin': 'admin_new'}
        containers = [{
            'Id': 'web_old', 'State': {'Running': True},
            'Config': {'Labels': {'com.docker.compose.project': 'example-prod', 'com.docker.compose.service': 'web_old'}}
        }]
        def command(args):
            if args[1] == 'ps': return 'web_old'
            if args[1] == 'inspect': return json.dumps(containers)
            if args[1] == 'exec': raise RuntimeError('old metric unavailable')
            return ''
        with patch.object(deploy, 'command', side_effect=command) as run, contextlib.redirect_stdout(io.StringIO()) as output:
            obj.cleanup({})
        self.assertTrue(any(call.args[0] == ['docker', 'stop', '--timeout', '90', 'web_old']
                            for call in run.call_args_list))
        self.assertIn('stopping retired containers', output.getvalue())

    def test_cleanup_can_retain_undrained_containers_only_when_explicitly_configured(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'project': 'example-prod', 'retain_undrained_containers': True}
        obj.services = {'api': 'api_new', 'web': 'web_new', 'mcp': 'mcp_new', 'admin': 'admin_new'}
        containers = [{
            'Id': 'web_old', 'State': {'Running': True},
            'Config': {'Labels': {'com.docker.compose.project': 'example-prod', 'com.docker.compose.service': 'web_old'}}
        }]
        def command(args):
            if args[1] == 'ps': return 'web_old'
            if args[1] == 'inspect': return json.dumps(containers)
            if args[1] == 'exec': raise RuntimeError('old metric unavailable')
            return ''
        with patch.object(deploy, 'command', side_effect=command) as run:
            obj.cleanup({})
        self.assertFalse(any(call.args[0][1] in ('stop', 'rm') for call in run.call_args_list))

    def test_rollback_stops_undrained_candidates_by_default(self):
        for sha in ['c' * 40, None]:
            with self.subTest(sha=sha):
                self.check_rollback_stops_undrained_candidates(sha)

    def check_rollback_stops_undrained_candidates(self, sha):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {'project': 'example-prod'}
            obj.state = {'services': {k: k + '_previous' for k in ['api', 'web', 'admin', 'mcp']}}
            if sha is not None:
                obj.state['sha'] = sha
            obj.services = {k: k + '_new' for k in ['api', 'web', 'admin', 'mcp']}
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            deploy.write_private(directory / 'before-state.json', obj.state)
            obj.root, obj.release, obj.payload = directory, 'gh-test', payload()
            obj.live_caddy = directory / 'live'
            obj.env = directory / 'env'
            obj.rolling = directory / 'rolling'
            obj.live_caddy.write_text('candidate')
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            events = []
            obj.compose = lambda *args, **kwargs: events.append(args)
            obj.reload = lambda: events.append('reload')
            obj.verify = lambda expected=None: events.append(('verify', expected))
            obj.update_monitoring = lambda services: events.append('monitoring')
            obj.drain_retired = lambda *args, **kwargs: False
            with patch.object(deploy, 'command', return_value='candidate'):
                obj.rollback()
            self.assertIn(('verify', sha[:12] if sha else ''), events)
            self.assertIn(('stop', '--timeout', '90', *obj.services.values()), events)

    def test_failed_old_service_start_restores_candidate_config_and_reports_degraded(self):
        for promoted in [False, True]:
            with self.subTest(promoted=promoted), tempfile.TemporaryDirectory() as root:
                obj, _ = prepare_on_host(root, {})
                candidate_env = (obj.directory / 'release.env').read_bytes()
                candidate_rolling = (obj.directory / 'rolling.compose.json').read_bytes()
                if promoted:
                    obj.env.write_bytes(candidate_env)
                    obj.rolling.write_bytes(candidate_rolling)
                obj.live_caddy.write_text('candidate route')
                # Not promoted: USR1 failed after the live write. Promoted: a later step failed.
                obj.record_switch(deploy.SWITCH_CONFIG_PROMOTED if promoted else deploy.SWITCH_CADDY_WRITTEN)

                def fail_old_start(*args, **kwargs):
                    self.assertEqual(obj.env.read_bytes(), (obj.directory / 'before-release.env').read_bytes())
                    self.assertEqual(obj.rolling.read_bytes(), (obj.directory / 'before-rolling.json').read_bytes())
                    self.assertFalse(kwargs['candidate'])
                    raise RuntimeError('old services unavailable')

                obj.compose = unittest.mock.Mock(side_effect=fail_old_start)
                obj.reload = unittest.mock.Mock()
                obj.verify = unittest.mock.Mock()
                obj.update_monitoring = unittest.mock.Mock()
                obj.drain_retired = unittest.mock.Mock()
                with contextlib.redirect_stdout(io.StringIO()) as output, \
                        patch.object(deploy, 'command') as run, \
                        self.assertRaisesRegex(RuntimeError, 'degraded rollback'):
                    obj.rollback()

                self.assertEqual(obj.env.read_bytes(), candidate_env)
                self.assertEqual(obj.rolling.read_bytes(), candidate_rolling)
                self.assertEqual(obj.live_caddy.read_text(), 'candidate route')
                state = json.loads(obj.state_path.read_text())
                self.assertEqual((state['services'], state['release'], state['unverified']),
                                 (obj.services, obj.release, True))
                self.assertIn('DEGRADED', output.getvalue())
                self.assertIn('all name the candidate release', output.getvalue())
                self.assertIn('may still run the previous release' if not promoted
                              else 'USR1 sent to Caddy; routing not verified', output.getvalue())
                marker = json.loads(obj.degraded_path.read_text())
                self.assertEqual(marker['release'], obj.release)
                self.assertEqual(marker['sha'], obj.payload['sha'])
                self.assertIn('previous services failed to start', marker['reason'])
                self.assertEqual(obj.degraded_path.stat().st_mode & 0o777, 0o600)
                obj.compose.assert_called_once_with(
                    'up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                    *obj.state['services'].values(), 'worker', candidate=False)
                for mock in [obj.reload, obj.verify, obj.update_monitoring, obj.drain_retired, run]:
                    mock.assert_not_called()

                # A new receiver process must reject the next deployment without touching Docker.
                retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
                with patch.object(deploy, 'command') as run, \
                        self.assertRaisesRegex(RuntimeError, 'Manual intervention required') as failure:
                    retry.apply(archive())
                self.assertIn(f'remove {obj.degraded_path}', str(failure.exception))
                run.assert_not_called()

    def test_degraded_marker_blocks_new_deploys_and_same_sha_retries_until_manually_removed(self):
        for same_sha in [False, True]:
            for content in ['', 'not valid JSON', '{"reason":"routing unconfirmed"}']:
                with self.subTest(same_sha=same_sha, content=content), tempfile.TemporaryDirectory() as root:
                    obj, _ = prepare_on_host(root, {})
                    if same_sha:
                        obj.state['sha'] = obj.payload['sha']
                        deploy.write_private(obj.state_path, obj.state)
                    obj.degraded_path.write_text(content)
                    retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
                    with patch.object(retry, 'capacity_gate', side_effect=RuntimeError('deployment resumed')) as capacity, \
                            patch.object(retry, 'verify', side_effect=RuntimeError('deployment resumed')) as verify, \
                            patch.object(retry, 'prepare') as prepare, \
                            patch.object(retry, 'cleanup') as cleanup, \
                            patch.object(deploy, 'command') as run:
                        with self.assertRaisesRegex(RuntimeError, 'Manual intervention required') as failure:
                            retry.apply(archive())
                        self.assertIn(f'remove {obj.degraded_path}', str(failure.exception))
                        self.assertEqual(obj.degraded_path.read_text(), content)
                        for mock in [capacity, verify, prepare, cleanup, run]:
                            mock.assert_not_called()
                        obj.degraded_path.unlink()
                        with self.assertRaisesRegex(RuntimeError, 'deployment resumed'):
                            retry.apply(archive())
                        (verify if same_sha else capacity).assert_called_once_with()

    def test_caddy_reload_failure_marks_routing_unconfirmed_and_blocks_next_deploy(self):
        for fail_validation in [False, True]:
            with self.subTest(fail_validation=fail_validation), tempfile.TemporaryDirectory() as root:
                obj, _ = prepare_on_host(root, {})
                obj.config['caddy_container'] = 'fixture-caddy'
                error = RuntimeError('fixture Caddy failure')
                # Invalid live file (caddy exit 1, caddy runnable) with no backup, or a failed signal.
                commands = ([RuntimeError('command failed: docker (exit 1); inspect privately on host'), '']
                            if fail_validation else ['', error])
                with patch.object(deploy, 'command', side_effect=commands), \
                        contextlib.redirect_stdout(io.StringIO()) as output, \
                        self.assertRaisesRegex(RuntimeError, 'Live Caddyfile validation failed'
                                               if fail_validation else 'fixture Caddy failure'):
                    obj.reload()
                self.assertIn('routing unconfirmed', output.getvalue())
                self.assertNotIn('Caddy still routes to the candidate', output.getvalue())
                self.assertFalse(hasattr(obj, 'switch_at'))
                self.assertEqual(json.loads(obj.degraded_path.read_text())['reason'],
                                 'Caddy reload failed; routing unconfirmed')
                retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
                with patch.object(deploy, 'command') as run, \
                        self.assertRaisesRegex(RuntimeError, 'Manual intervention required'):
                    retry.apply(archive())
                run.assert_not_called()

    def test_rollback_caddy_reload_failure_retains_candidates_and_requires_manual_recovery(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {})
            obj.config['caddy_container'] = 'fixture-caddy'
            obj.compose = unittest.mock.Mock()
            obj.verify = unittest.mock.Mock()
            obj.update_monitoring = unittest.mock.Mock()
            obj.drain_retired = unittest.mock.Mock()
            with patch.object(deploy, 'command', side_effect=['', RuntimeError('fixture Caddy failure')]), \
                    contextlib.redirect_stdout(io.StringIO()) as output, \
                    self.assertRaisesRegex(RuntimeError, 'fixture Caddy failure'):
                obj.rollback()
            self.assertIn('routing unconfirmed', output.getvalue())
            self.assertTrue(obj.degraded_path.exists())
            obj.compose.assert_called_once_with(
                'up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                *obj.state['services'].values(), 'worker', candidate=False)
            for mock in [obj.verify, obj.update_monitoring, obj.drain_retired]:
                mock.assert_not_called()

    def test_failed_forward_reload_allows_next_deploy_after_verified_rollback(self):
        for forward_failure in ['validate', 'signal']:
            with self.subTest(forward_failure=forward_failure):
                self.check_forward_reload_recovery(forward_failure)

    def test_failed_forward_reload_keeps_marker_when_rollback_cannot_confirm_recovery(self):
        for forward_failure in ['validate', 'signal']:
            for rollback_failure in ['validate', 'signal', 'verify']:
                with self.subTest(forward_failure=forward_failure, rollback_failure=rollback_failure):
                    self.check_forward_reload_recovery(forward_failure, rollback_failure)

    def check_forward_reload_recovery(self, forward_failure, rollback_failure=None):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {})
            obj.config['caddy_container'] = 'fixture-caddy'
            obj.state['sha'] = 'c' * 40
            deploy.write_private(obj.state_path, obj.state)
            # prepare() snapshots the live state; keep the snapshot in step with the edit above.
            deploy.write_private(obj.directory / 'before-state.json', obj.state)
            before_state = obj.state_path.read_bytes()
            before_caddy = (obj.directory / 'before-caddy').read_bytes()
            (obj.directory / 'Caddyfile.next').write_text('candidate route')

            def stub_host_steps(target):
                for name in ['capacity_gate', 'backup', 'compose', 'warm_candidates', 'verify',
                             'update_monitoring', 'cleanup', 'prune']:
                    setattr(target, name, unittest.mock.Mock())
                target.drain_retired = unittest.mock.Mock(return_value=True)
                # This test injects failures into reload, after staged validation succeeds.
                target.install_caddy = lambda content, before_write=None: (before_write and before_write(), deploy.write_caddy_in_place(target.live_caddy, content))
                target.verify_public_mcp = unittest.mock.Mock()

            stub_host_steps(obj)
            # The fixture has already prepared this run's release and rollback snapshots.
            obj.prepare = unittest.mock.Mock()

            def verify_rollback(expected=None):
                self.assertEqual(expected, obj.state['sha'][:12])
                self.assertEqual(obj.live_caddy.read_bytes(), before_caddy)
                self.assertTrue(obj.degraded_path.exists())
                if rollback_failure == 'verify':
                    raise RuntimeError('rollback verify failed')

            obj.verify.side_effect = verify_rollback
            reload_attempt = 0
            starting_deploy = True

            def command(args):
                nonlocal reload_attempt, starting_deploy
                if args[:3] == ['docker', 'network', 'inspect']:
                    return json.dumps([{'Containers': {}}])
                if args[:3] == ['docker', 'image', 'inspect']:
                    return json.dumps({'org.opencontainers.image.revision': obj.payload['sha']})
                if args[:2] == ['docker', 'create']:
                    return 'fixture-container'
                step = None
                if 'validate' in args:
                    if starting_deploy:
                        starting_deploy = False
                        return ''
                    reload_attempt += 1
                    step = 'validate'
                    if reload_attempt == 2:
                        self.assertEqual(obj.live_caddy.read_bytes(), before_caddy)
                        self.assertTrue(obj.degraded_path.exists())
                        obj.compose.assert_called_with(
                            'up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                            *obj.state['services'].values(), 'worker', candidate=False)
                elif '--signal=USR1' in args:
                    step = 'signal'
                if step and reload_attempt == 1 and step == forward_failure:
                    raise RuntimeError('forward ' + step + ' failed')
                if step and reload_attempt == 2 and step == rollback_failure:
                    raise RuntimeError('rollback ' + step + ' failed')
                return ''

            with patch.object(deploy, 'command', side_effect=command), \
                    contextlib.redirect_stdout(io.StringIO()):
                failure = 'rollback ' + rollback_failure if rollback_failure else 'forward ' + forward_failure
                error = 'Live Caddyfile validation failed' if failure.endswith('validate') else failure + ' failed'
                with self.assertRaisesRegex(RuntimeError, error):
                    obj.apply(archive())
                self.assertEqual(reload_attempt, 2)
                state = json.loads(obj.state_path.read_text())
                if rollback_failure:
                    # Unverified switch back: previous state plus the rolling-back record for reconcile.
                    self.assertEqual(state.pop('switch')['phase'], deploy.SWITCH_ROLLING_BACK)
                self.assertEqual(state, json.loads(before_state))
                self.assertEqual(obj.live_caddy.read_bytes(), before_caddy)
                obj.cleanup.assert_not_called()
                obj.prune.assert_not_called()

                next_payload = {**payload(), 'run_id': '124'}
                retry = deploy.Deployment(obj.config, next_payload)
                stub_host_steps(retry)
                if rollback_failure:
                    self.assertTrue(obj.degraded_path.exists())
                    obj.drain_retired.assert_not_called()
                    with self.assertRaisesRegex(RuntimeError, 'Manual intervention required'):
                        retry.apply(archive())
                    retry.capacity_gate.assert_not_called()
                    retry.compose.assert_not_called()
                else:
                    self.assertFalse(obj.degraded_path.exists())
                    obj.verify.assert_called_once_with(expected=obj.state['sha'][:12])
                    obj.update_monitoring.assert_called_once_with(obj.state['services'])
                    starting_deploy = True
                    retry.apply(archive())
                    self.assertEqual(reload_attempt, 3)
                    self.assertEqual(json.loads(retry.state_path.read_text())['release'], retry.release)
                    retry.capacity_gate.assert_called_once_with()
                    retry.cleanup.assert_called_once()
                    retry.prune.assert_called_once()

    def test_verify_checks_legacy_routes_without_sha_and_requires_known_release_headers(self):
        obj = object.__new__(deploy.Deployment)
        obj.payload = payload()
        obj.config = {'local_origin': 'https://example.org', 'checks': [{'path': '/', 'host': 'example.org'}]}
        cases = [('', 200, '', True), ('', 503, '', False),
                 (None, 200, 'a' * 12, True), (None, 200, '', False),
                 ('c' * 12, 200, 'a' * 12, False)]
        for expected, status, header, success in cases:
            with self.subTest(expected=expected, status=status, header=header):
                def response(*args, **kwargs):
                    body = io.BytesIO(b'healthy route')
                    body.status = status
                    body.headers = {'X-OpenNavo-Release': header}
                    return body
                with patch.object(deploy.urllib.request, 'urlopen', side_effect=response), \
                        patch.object(deploy.time, 'sleep'):
                    if success:
                        obj.verify(expected=expected)
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'post-deployment route checks failed'):
                            obj.verify(expected=expected)

    def test_same_sha_short_circuit_skips_capacity_gate(self):
        obj = object.__new__(deploy.Deployment)
        obj.root = Path('/fixture')
        obj.payload = payload()
        obj.state = {'sha': obj.payload['sha'], 'services': {'api': 'api_live', 'web': 'web_live'}}
        events = []
        obj.validate_live_caddy = lambda: events.append('validate live Caddy')
        obj.verify = lambda: events.append('verify')
        obj.update_monitoring = lambda services: events.append('monitoring')
        obj.cleanup = lambda previous: events.append('cleanup')
        obj.prune = lambda previous: events.append('prune')
        obj.capacity_gate = lambda: (_ for _ in ()).throw(AssertionError('short circuit must skip capacity'))
        obj.apply(archive())
        self.assertEqual(events, ['validate live Caddy', 'verify', 'monitoring', 'cleanup', 'prune'])

    def test_drain_timeout_warns_and_reports_failure(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'project': 'example-prod'}
        obj.switch_at = None
        containers = [{
            'Id': 'old-web', 'State': {'Running': True},
            'Config': {'Labels': {'com.docker.compose.service': 'web_old'}}
        }]
        with patch.object(deploy, 'command', return_value='1'), \
                patch.object(deploy.time, 'monotonic', side_effect=[0, 0, 3]), \
                patch.object(deploy.time, 'sleep'), \
                contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertFalse(obj.drain_retired(containers, timeout=2))
        self.assertIn('drain timed out', output.getvalue())

    def test_retry_verifies_existing_release_before_resuming_cleanup(self):
        obj = object.__new__(deploy.Deployment)
        obj.root = Path('/fixture')
        obj.payload = payload()
        previous = {'services': {'api': 'api_previous'}}
        obj.state = {'sha': obj.payload['sha'], 'services': {'api': 'api_live'}, 'previous': previous}
        events = []
        obj.validate_live_caddy = lambda: events.append('validate live Caddy')
        obj.verify = lambda: events.append('verify')
        obj.update_monitoring = lambda services: events.append('monitoring')
        obj.cleanup = lambda state: events.append(('cleanup', state))
        obj.prune = lambda state: events.append(('prune', state))
        obj.apply(archive())
        self.assertEqual(obj.services, obj.state['services'])
        self.assertEqual(events, ['validate live Caddy', 'verify', 'monitoring', ('cleanup', previous), ('prune', previous)])

    def test_retry_continues_when_live_release_directory_is_missing(self):
        with tempfile.TemporaryDirectory() as root:
            obj = object.__new__(deploy.Deployment)
            obj.root = Path(root)
            obj.payload = payload()
            previous = {'services': {'api': 'api_previous'}}
            obj.state = {
                'sha': obj.payload['sha'],
                'release': 'missing-release',
                'services': {'api': 'api_live'},
                'previous': previous,
            }
            events = []
            obj.validate_live_caddy = lambda: events.append('validate live Caddy')
            obj.verify = lambda: events.append('verify')
            obj.update_monitoring = lambda services: events.append('monitoring')
            obj.cleanup = lambda state: events.append(('cleanup', state))
            obj.prune = lambda state: events.append(('prune', state))
            with patch('builtins.print') as output:
                obj.apply(archive())

            self.assertEqual(events, ['validate live Caddy', 'verify', 'monitoring', ('cleanup', previous), ('prune', previous)])
            output.assert_any_call('Live release manifest repair skipped; continuing deployment.', flush=True)

    def test_retry_rebuilds_a_missing_live_manifest_without_sensitive_fields(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            release = root / 'releases/live'
            (release / 'web-assets').mkdir(parents=True)
            (release / 'web-assets/live.js').write_text('live')
            obj = object.__new__(deploy.Deployment)
            obj.root = root
            obj.payload = {**payload(), 'registry_token': 'secret-token', 'admin_archive': 'secret-archive'}
            obj.state = {'sha': obj.payload['sha'], 'release': 'live', 'services': {'api': 'api_live'}}
            obj.validate_live_caddy = unittest.mock.Mock()
            obj.verify = lambda: None
            obj.update_monitoring = lambda services: None
            obj.cleanup = lambda state: None
            obj.prune = lambda state: None

            obj.apply(archive())

            obj.validate_live_caddy.assert_called_once_with()
            manifest = json.loads((release / 'manifest.json').read_text())
            self.assertEqual(manifest['web_assets'], ['live.js'])
            self.assertNotIn('registry_token', manifest)
            self.assertNotIn('admin_archive', manifest)

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
            with self.subTest(revision=revision), tempfile.TemporaryDirectory() as root:
                obj = object.__new__(deploy.Deployment)
                obj.root = Path(root)
                obj.validate_live_caddy = unittest.mock.Mock()
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
            obj.root = directory
            obj.config = {'project': 'fixture', 'caddy_container': 'fixture-caddy'}
            obj.state = {'sha': 'c' * 40, 'services': {k: k + '_previous' for k in ['api', 'web', 'mcp', 'public-mcp', 'admin']}}
            obj.payload = payload()
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            obj.release = 'test'
            obj.services = {k: k + '_new' for k in ['api', 'web', 'mcp', 'public-mcp', 'admin']}
            obj.current_web_assets = ['legacy.js']
            obj.live_caddy = directory / 'live'
            obj.env = directory / 'env'
            obj.rolling = directory / 'rolling'
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            (directory / 'Caddyfile.next').write_text('new')
            deploy.write_private(directory / 'before-state.json', obj.state)
            deploy.write_private(obj.state_path, obj.state)
            obj.config['root'] = str(directory)
            (directory / 'deploy').mkdir()
            obj.live_caddy.write_text('old')
            events = []
            obj.prepare = lambda _: events.append('prepare')
            obj.backup = lambda: events.append('backup')
            obj.compose = lambda *args, **kwargs: events.append(args)
            obj.reload = lambda: events.append('reload')
            obj.install_caddy = lambda content, before_write=None: (before_write and before_write(), deploy.write_caddy_in_place(obj.live_caddy, content))
            obj.verify_public_mcp = lambda: events.append('public-mcp handshake')
            obj.capacity_gate = lambda: None
            obj.warm_candidates = lambda: events.append('warm')
            obj.update_monitoring = lambda services: events.append(('monitoring', services))
            obj.cleanup = lambda _: events.append('cleanup')
            obj.prune = lambda _: events.append('prune')
            def verify(expected=None):
                if expected is None:
                    raise RuntimeError('route check failed')
                events.append(('verify', expected))
            obj.verify = verify
            def cmd(args, output=None):
                if args[:3] == ['docker', 'image', 'inspect']:
                    return json.dumps({'org.opencontainers.image.revision': 'a' * 40})
                return ''
            with patch.object(deploy, 'command', side_effect=cmd), self.assertRaises(RuntimeError):
                obj.apply(archive())
            manifest = json.loads((directory / 'manifest.json').read_text())
            self.assertEqual(manifest['web_assets'], ['legacy.js'])
            self.assertEqual(obj.live_caddy.read_text(), 'old')
            self.assertEqual(obj.env.read_text(), 'old')
            self.assertEqual(obj.rolling.read_text(), 'old')
            self.assertLess(events.index('backup'), events.index(('run', '--rm', '--no-deps', 'migrate', 'up')))
            old_up = ('up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                      'api_previous', 'web_previous', 'mcp_previous', 'public-mcp_previous', 'admin_previous', 'worker')
            self.assertIn(old_up, events)
            self.assertLess(events.index(old_up), len(events) - 1 - events[::-1].index('reload'))
            # Verified rollback restores prepare()'s state snapshot exactly (no candidate, no switch record).
            self.assertEqual(json.loads(obj.state_path.read_text()), obj.state)
            self.assertNotIn('down', str(events))
            self.assertNotIn('cleanup', events)
            self.assertNotIn('prune', events)

            # If monitoring cannot recover, rollback leaves candidate containers for inspection.
            events.clear()
            def fail_monitoring(_): raise RuntimeError('monitoring unavailable')
            obj.update_monitoring = fail_monitoring
            with patch.object(obj, 'mark_degraded') as marked, \
                    self.assertRaisesRegex(RuntimeError, 'monitoring unavailable'):
                obj.rollback()
            marked.assert_called_once()
            self.assertFalse(any(event and event[0] == 'stop' for event in events if isinstance(event, tuple)))
            self.assertEqual(obj.live_caddy.read_text(), 'old')

            # A successful retry must persist the verified release before cleanup.
            obj.state['previous'] = {'sha': 'd' * 40}
            (directory / 'release.env').write_text('new-env')
            (directory / 'rolling.compose.json').write_text('{}')
            events.clear()
            obj.verify = lambda expected=None: events.append('verify')
            obj.warm_candidates = lambda: events.append('warm')
            obj.update_monitoring = lambda services: events.append('monitoring')
            def cleanup(previous):
                state = json.loads(obj.state_path.read_text())
                self.assertEqual(state['sha'], obj.payload['sha'])
                self.assertEqual(state['previous'], {'sha': 'c' * 40, 'services': {k: k + '_previous' for k in ['api', 'web', 'mcp', 'public-mcp', 'admin']}})
                self.assertEqual(previous, state['previous'])
                events.append('cleanup')
            obj.cleanup = cleanup
            with patch.object(deploy, 'command', side_effect=cmd):
                obj.apply(archive())
            manifest = json.loads((directory / 'manifest.json').read_text())
            self.assertEqual(manifest['web_assets'], ['legacy.js'])
            self.assertEqual(events.count('verify'), 2)
            self.assertEqual(events[-2:], ['cleanup', 'prune'])
            self.assertLess(events.index(('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker')), len(events) - 1)


    def test_sweep_stale_temp_files_removes_only_old_write_temp_files(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            (root / 'deploy').mkdir()
            (root / 'releases/gh-aaaaaaaaaaaa-1-1').mkdir(parents=True)
            stale = [root / 'deploy/.github-state.json-abc12345',
                     root / 'releases/gh-aaaaaaaaaaaa-1-1/.manifest.json-abc12345']
            fresh = root / 'deploy/.current-release.env-abc12345'
            kept = [root / 'deploy/.keep', root / '.Caddyfile.intent']
            for path in [*stale, fresh, *kept]:
                path.write_text('x')
            for path in stale:
                os.utime(path, (0, 0))
            self.assertEqual(deploy.sweep_stale_temp_files(root, 'Caddyfile'), 2)
            for path in stale:
                self.assertFalse(path.exists())
            for path in [fresh, *kept]:
                self.assertTrue(path.exists())

    def test_retry_after_accepts_http_date(self):
        now = datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc)
        self.assertEqual(deploy._retry_after_seconds(
            {'Retry-After': 'Thu, 01 Jan 2026 00:00:12 GMT'}, now=now), 12)
        self.assertEqual(deploy._retry_after_seconds(
            {'Retry-After': 'Thu, 01 Jan 2026 00:00:12 GMT'}, now=now.replace(minute=1)), 0)
        self.assertEqual(deploy._retry_after_seconds({'Retry-After': 'not-a-date'}), None)
        self.assertEqual(deploy._retry_after_seconds({'Retry-After': '7'}), 7)

    def test_public_mcp_host_requires_config_or_web_domain(self):
        obj = deploy.Deployment.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80',
                      'checks': [{'host': 'admin.opennavo.com', 'path': '/'}]}
        with self.assertRaisesRegex(RuntimeError, 'public_mcp_host or WEB_DOMAIN'):
            obj.verify_public_mcp()

    def test_public_mcp_host_derives_from_web_domain_in_host_env(self):
        with tempfile.TemporaryDirectory() as root:
            (Path(root) / '.env').write_text('# host configuration\nWEB_DOMAIN=opennavo.com\n')
            obj = deploy.Deployment.__new__(deploy.Deployment)
            obj.root = Path(root)
            obj.config = {'local_origin': 'http://127.0.0.1:80',
                          'checks': [{'host': 'admin.opennavo.com', 'path': '/'}]}
            seen = []
            fake = fake_public_mcp()

            def urlopen(request, timeout):
                seen.append(request)
                return fake(request, timeout)
            with patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen):
                obj.verify_public_mcp()
            self.assertTrue(seen)
            self.assertEqual({request.get_header('Host') for request in seen}, {'opennavo.com'})

    def test_redact_preview_masks_short_provider_keys(self):
        # Assembled at runtime so secret scanners do not flag the fixtures.
        secrets = ['sk-' + 'a' * 16, 'ghp_' + 'b' * 16, 'AKIA' + 'C' * 16, 'xoxb-' + '1' * 10 + '-abc']
        text = deploy.redact_preview(' '.join(secrets).encode())
        for secret in secrets:
            self.assertNotIn(secret, text)

    def test_successful_apply_clears_switch_and_unverified_state(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
            obj.state = {'sha': 'c' * 40, 'services': {k: k + '_previous' for k in deploy.APPLICATION_SERVICES}}
            deploy.write_private(obj.state_path, obj.state)
            deploy.write_private(obj.directory / 'before-state.json', obj.state)
            for name in ['capacity_gate', 'backup', 'warm_candidates', 'verify', 'update_monitoring',
                         'cleanup', 'prune', 'prepare']:
                setattr(obj, name, unittest.mock.Mock())
            obj.install_caddy = lambda content, before_write=None: (
                before_write and before_write(), deploy.write_caddy_in_place(obj.live_caddy, content))
            obj.write_manifest = unittest.mock.Mock()
            obj.compose = unittest.mock.Mock()
            obj.reload = unittest.mock.Mock()

            def cmd(args):
                if args[:3] == ['docker', 'image', 'inspect']:
                    return json.dumps({'org.opencontainers.image.revision': obj.payload['sha']})
                if args[:3] == ['docker', 'network', 'inspect']:
                    return json.dumps([{'Containers': {}}])
                return ''
            with patch.object(deploy, 'command', side_effect=cmd), contextlib.redirect_stdout(io.StringIO()):
                obj.apply(archive())
            state = json.loads(obj.state_path.read_text())
            self.assertEqual(state['sha'], obj.payload['sha'])
            self.assertNotIn('switch', state)
            self.assertNotIn('unverified', state)
            # The next deployment takes the already-deployed shortcut instead of a full rollout.
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            with patch.object(retry, 'capacity_gate') as capacity:
                retry.validate_live_caddy = unittest.mock.Mock()
                retry.ensure_live_manifest = unittest.mock.Mock()
                retry.verify = unittest.mock.Mock()
                retry.update_monitoring = unittest.mock.Mock()
                retry.cleanup = unittest.mock.Mock()
                retry.prune = unittest.mock.Mock()
                with contextlib.redirect_stdout(io.StringIO()) as out:
                    retry.apply(archive())
                capacity.assert_not_called()
            self.assertIn('already deployed', out.getvalue())

    def test_not_switched_reconcile_keeps_the_verified_previous_release(self):
        """A run killed before the live write leaves a verified previous release, not an unverified one."""
        with tempfile.TemporaryDirectory() as root:
            obj, _ = prepare_on_host(root, {}, include_public_mcp=True)
            obj.state['sha'] = 'c' * 40
            deploy.write_private(obj.state_path, obj.state)
            deploy.write_private(obj.directory / 'before-state.json', obj.state)
            obj.record_switch(deploy.SWITCH_NONE)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            retry.reload = unittest.mock.Mock()
            retry.stop_interrupted_candidates = unittest.mock.Mock()
            with contextlib.redirect_stdout(io.StringIO()):
                retry.reconcile_interrupted_switch()
            self.assertNotIn('switch', retry.state)
            self.assertNotIn('unverified', retry.state)
            self.assertEqual(retry.state['sha'], 'c' * 40)
            retry.reload.assert_not_called()

    def test_reconcile_reloads_caddy_after_an_interrupted_switch(self):
        """Files alone do not prove the process: a killed reload must be re-issued before trusting state."""
        with tempfile.TemporaryDirectory() as root:
            obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            obj.degraded_path.unlink(missing_ok=True)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            events = []
            retry.reload = lambda: events.append('reload')
            retry.stop_interrupted_candidates = lambda: events.append('stop candidates')
            with contextlib.redirect_stdout(io.StringIO()):
                retry.reconcile_interrupted_switch()
            self.assertEqual(events, ['reload', 'stop candidates'])
            self.assertEqual(obj.live_caddy.read_bytes(), candidate_caddy)

    def test_reconcile_reload_failure_marks_degraded(self):
        with tempfile.TemporaryDirectory() as root:
            obj, candidate_caddy = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            obj.degraded_path.unlink(missing_ok=True)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            def fail_reload():
                retry.mark_degraded('reload failed')
                raise RuntimeError('reload failed')
            retry.reload = unittest.mock.Mock(side_effect=fail_reload)
            retry.stop_interrupted_candidates = unittest.mock.Mock()
            with contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, 'reload failed'):
                retry.reconcile_interrupted_switch()
            self.assertTrue(retry.degraded_path.exists())
            retry.stop_interrupted_candidates.assert_not_called()

    def test_reconcile_removes_candidate_containers_no_release_claims(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            obj.degraded_path.unlink(missing_ok=True)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            retry.reload = unittest.mock.Mock()
            retry.state = {**retry.state, 'services': {'api': 'api_live'}, 'previous': {'services': {'api': 'api_old'}}}
            def container(name, service):
                return {'Id': name, 'Config': {'Labels': {
                    'com.docker.compose.project': retry.config['project'],
                    'com.docker.compose.service': service}}}
            containers = [container('live', 'api_live'), container('old', 'api_old'),
                          container('orphan', 'api_ghdeadbeef12'), container('foreign', 'worker')]
            calls = []
            def command(args):
                calls.append(args)
                if args[:2] == ['docker', 'ps']:
                    return '\n'.join(c['Id'] for c in containers)
                if args[:2] == ['docker', 'inspect']:
                    return json.dumps(containers)
                return ''
            with patch.object(deploy, 'command', side_effect=command), contextlib.redirect_stdout(io.StringIO()):
                retry.stop_interrupted_candidates()
            self.assertIn(['docker', 'stop', '--timeout', '30', 'orphan'], calls)
            self.assertIn(['docker', 'rm', 'orphan'], calls)
            mutations = [call for call in calls if call[:2] in (['docker', 'stop'], ['docker', 'rm'])]
            self.assertEqual(mutations, [['docker', 'stop', '--timeout', '30', 'orphan'],
                                         ['docker', 'rm', 'orphan']])

    def test_reconcile_candidate_cleanup_failure_warns_without_failing(self):
        with tempfile.TemporaryDirectory() as root:
            obj, _ = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            retry.reload = unittest.mock.Mock()
            retry.state = {**retry.state, 'services': {'api': 'api_live'}, 'previous': {}}
            def command(args):
                if args[:2] == ['docker', 'ps']:
                    raise RuntimeError('docker unavailable')
                return ''
            with patch.object(deploy, 'command', side_effect=command), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                retry.stop_interrupted_candidates()
            self.assertIn('could not list candidate containers', output.getvalue())

    def test_candidate_cleanup_never_runs_without_a_claimed_release(self):
        """An empty claimed set would make every application container an orphan."""
        for state in [{}, {'services': {}}, {'previous': {}}]:
            with self.subTest(state=state), tempfile.TemporaryDirectory() as root:
                obj, _ = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
                retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
                retry.state = state
                with patch.object(deploy, 'command') as command, \
                        contextlib.redirect_stdout(io.StringIO()) as output:
                    retry.stop_interrupted_candidates()
                command.assert_not_called()
                self.assertIn('skipping candidate container cleanup', output.getvalue())

    def test_candidate_cleanup_skips_oneoff_compose_run_containers(self):
        """`docker compose run` containers carry a bare service label that no release claims."""
        with tempfile.TemporaryDirectory() as root:
            obj, _ = self._switched(root, GOOD_CADDY, deploy.SWITCH_CADDY_RELOADED)
            retry = deploy.Deployment(obj.config, {**payload(), 'run_id': '124'})
            retry.state = {'services': {'api': 'api_live'}, 'previous': {}}
            def container(name, service, oneoff=False):
                labels = {'com.docker.compose.project': retry.config['project'],
                          'com.docker.compose.service': service}
                if oneoff:
                    labels['com.docker.compose.oneoff'] = 'True'
                return {'Id': name, 'Config': {'Labels': labels}}
            containers = [container('manual', 'api', oneoff=True),
                          container('orphan', 'api_ghdeadbeef12')]
            calls = []
            def command(args):
                calls.append(args)
                if args[:2] == ['docker', 'ps']:
                    # The receiver also filters by label; keep the fake honest about both filters.
                    self.assertIn('label=com.docker.compose.oneoff=False', args)
                    return '\n'.join(c['Id'] for c in containers)
                if args[:2] == ['docker', 'inspect']:
                    return json.dumps(containers)
                return ''
            with patch.object(deploy, 'command', side_effect=command), contextlib.redirect_stdout(io.StringIO()):
                retry.stop_interrupted_candidates()
            mutations = [call for call in calls if call[:2] in (['docker', 'stop'], ['docker', 'rm'])]
            self.assertEqual(mutations, [['docker', 'stop', '--timeout', '30', 'orphan'],
                                         ['docker', 'rm', 'orphan']])

    def test_install_caddy_validates_before_refreshing_the_backup(self):
        with tempfile.TemporaryDirectory() as root:
            obj = self._caddy_obj(root, live=GOOD_CADDY, backup=b'previous-backup')
            with patch.object(deploy, 'command', side_effect=FakeCaddy(obj.live_caddy)), \
                    self.assertRaisesRegex(RuntimeError, 'invalid staged Caddyfile'):
                obj.install_caddy(TRUNCATED_CADDY)
            self.assertEqual((obj.root / 'Caddyfile.bak').read_bytes(), b'previous-backup')
            self.assertEqual(obj.live_caddy.read_bytes(), GOOD_CADDY)

    def test_public_mcp_subpath_requires_the_route_marker(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        for body in ['', '404 page not found', '{"error":"not found"}']:
            with self.subTest(body=body), patch.object(
                    deploy.urllib.request, 'urlopen',
                    side_effect=fake_public_mcp(subpath_body=body)), \
                    self.assertRaises(deploy.RouteCheckError) as raised:
                obj.verify_public_mcp()
            # The deploy log must say what the probe wanted, not only what it got.
            self.assertIn(deploy.PUBLIC_MCP_SUBPATH_MARKER, str(raised.exception))
            self.assertIn('expected body containing', str(raised.exception))
            self.assertEqual(raised.exception.status, 404)

    def test_public_mcp_subpath_marker_is_not_required_for_a_legacy_live_caddyfile(self):
        """A rollback to a release that answers /mcp/* with a bare 404 must still verify."""
        with tempfile.TemporaryDirectory() as root:
            live = Path(root) / 'Caddyfile'
            live.write_text('{$WEB_DOMAIN} {\n\thandle /mcp/* {\n\t\trespond 404\n\t}\n}\n')
            obj = object.__new__(deploy.Deployment)
            obj.live_caddy = live
            obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
            with patch.object(deploy.urllib.request, 'urlopen',
                              side_effect=fake_public_mcp(subpath_body='404')):
                obj.verify_public_mcp()
            live.write_text(deploy.public_mcp_route('public-mcp-proxy-x'))
            with patch.object(deploy.urllib.request, 'urlopen',
                              side_effect=fake_public_mcp(subpath_body='404')), \
                    self.assertRaises(deploy.RouteCheckError):
                obj.verify_public_mcp()

    def test_verify_backs_off_on_public_mcp_failures(self):
        obj = object.__new__(deploy.Deployment)
        obj.payload = payload()
        obj.services = {'public-mcp': 'public-mcp_new'}
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        sleeps = []
        with patch.object(deploy.urllib.request, 'urlopen', side_effect=RuntimeError('cold start')), \
                patch.object(deploy.time, 'sleep', side_effect=sleeps.append), \
                contextlib.redirect_stdout(io.StringIO()), \
                self.assertRaisesRegex(RuntimeError, 'route checks failed'):
            obj.verify()
        self.assertEqual(sleeps[:4], [2, 4, 8, 16])

    def test_verify_logs_a_missing_public_mcp_host_in_full_and_stops(self):
        """The fixed configuration message must reach the deploy log, not just the type name."""
        obj = object.__new__(deploy.Deployment)
        obj.payload = payload()
        obj.services = {'public-mcp': 'public-mcp_new'}
        obj.config = {'local_origin': 'http://127.0.0.1:80',
                      'checks': []}
        sleeps = []
        output = io.StringIO()
        with patch.object(deploy.urllib.request, 'urlopen',
                          side_effect=fake_public_mcp()), \
                patch.object(deploy.time, 'sleep', side_effect=sleeps.append), \
                contextlib.redirect_stdout(output), \
                self.assertRaisesRegex(RuntimeError, 'requires public_mcp_host or WEB_DOMAIN'):
            obj.verify()
        # A misconfiguration cannot heal by waiting: one attempt, no retry, no sleep.
        self.assertEqual(sleeps, [])
        self.assertEqual(output.getvalue().count('Post-deployment check attempt'), 1)
        self.assertIn('requires public_mcp_host or WEB_DOMAIN', output.getvalue())
        self.assertNotIn('RuntimeError\n', output.getvalue())

    def test_subpath_probe_connection_failure_is_a_route_check_error(self):
        """A refused connection on the probe kept only the type name; keep the endpoint instead."""
        obj = object.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        fake = fake_public_mcp()

        def urlopen(request, timeout):
            if request.data is None:
                raise deploy.urllib.error.URLError(ConnectionRefusedError('refused'))
            return fake(request, timeout)
        with patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen), \
                self.assertRaises(deploy.RouteCheckError) as raised:
            obj.verify_public_mcp()
        message = str(raised.exception)
        self.assertIn('/mcp/x', message)
        self.assertIn('request error (ConnectionRefusedError)', message)

    def test_subpath_probe_timeout_is_a_route_check_error(self):
        obj = object.__new__(deploy.Deployment)
        obj.config = {'local_origin': 'http://127.0.0.1:80', 'public_mcp_host': 'opennavo.com', 'checks': []}
        fake = fake_public_mcp()

        def urlopen(request, timeout):
            if request.data is None:
                raise TimeoutError('timed out')
            return fake(request, timeout)
        with patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen), \
                self.assertRaises(deploy.RouteCheckError) as raised:
            obj.verify_public_mcp()
        self.assertIn('/mcp/x', str(raised.exception))
        self.assertIn('request error (TimeoutError)', str(raised.exception))

    def test_rollback_drains_exactly_the_candidates_it_listed(self):
        """Discarding the probe result would stop still-serving candidates without draining them."""
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {'project': 'example-prod'}
            obj.state = {'sha': 'c' * 40, 'services': {'api': 'api_previous', 'web': 'web_previous'}}
            obj.services = {'api': 'api_new', 'web': 'web_new'}
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            deploy.write_private(directory / 'before-state.json', obj.state)
            obj.root, obj.release, obj.payload = directory, 'gh-test', payload()
            obj.live_caddy, obj.env, obj.rolling = (directory / name for name in ['live', 'env', 'rolling'])
            (directory / 'deploy').mkdir()
            obj.live_caddy.write_text('candidate')
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            obj.reload = unittest.mock.Mock()
            obj.verify = unittest.mock.Mock()
            obj.update_monitoring = unittest.mock.Mock()
            obj.compose = unittest.mock.Mock()
            obj.drain_retired = unittest.mock.Mock(return_value=True)
            listed = {'api_new': ['cand-api-1', 'cand-api-2'], 'web_new': ['cand-web-1']}

            def command(args):
                for flag, value in zip(args, args[1:]):
                    if flag == '--filter' and value.startswith('label=com.docker.compose.service='):
                        return '\n'.join(listed[value.removeprefix('label=com.docker.compose.service=')])
                return ''
            with patch.object(deploy, 'command', side_effect=command), contextlib.redirect_stdout(io.StringIO()):
                obj.rollback()
            obj.drain_retired.assert_called_once_with(
                [{'Id': 'cand-api-1', 'State': {'Running': True},
                  'Config': {'Labels': {'com.docker.compose.service': 'api_new'}}},
                 {'Id': 'cand-api-2', 'State': {'Running': True},
                  'Config': {'Labels': {'com.docker.compose.service': 'api_new'}}},
                 {'Id': 'cand-web-1', 'State': {'Running': True},
                  'Config': {'Labels': {'com.docker.compose.service': 'web_new'}}}],
                preserve_stream_grace=False, timeout=deploy.ROLLBACK_DRAIN_TIMEOUT_SECONDS)

    def test_rollback_candidate_listing_skips_oneoff_containers(self):
        """`docker compose run` containers must never be listed as rollback candidates."""
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {'project': 'example-prod'}
            obj.state = {'sha': 'c' * 40, 'services': {k: k + '_previous' for k in ['api', 'web']}}
            obj.services = {k: k + '_new' for k in ['api', 'web']}
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            deploy.write_private(directory / 'before-state.json', obj.state)
            obj.root, obj.release, obj.payload = directory, 'gh-test', payload()
            obj.live_caddy, obj.env, obj.rolling = (directory / name for name in ['live', 'env', 'rolling'])
            (directory / 'deploy').mkdir()
            obj.live_caddy.write_text('candidate')
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            obj.reload = unittest.mock.Mock()
            obj.verify = unittest.mock.Mock()
            obj.update_monitoring = unittest.mock.Mock()
            obj.compose = unittest.mock.Mock()
            obj.drain_retired = unittest.mock.Mock(return_value=True)
            commands = []

            def command(args):
                commands.append(args)
                return ''
            with patch.object(deploy, 'command', side_effect=command), contextlib.redirect_stdout(io.StringIO()):
                obj.rollback()
            # Prove this exercised rollback() itself, not another cleanup path.
            obj.verify.assert_called_once_with(expected='c' * 12)
            obj.compose.assert_any_call('stop', '--timeout', '90', *obj.services.values())
            obj.compose.assert_any_call('rm', '--force', '--stop', *obj.services.values())
            probes = [args for args in commands if args[:3] == ['docker', 'ps', '-q']]
            self.assertEqual(len(probes), len(obj.services))
            probed = []
            for args in probes:
                self.assertIn('label=com.docker.compose.oneoff=False', args)
                self.assertIn('label=com.docker.compose.project=example-prod', args)
                services = [value for flag, value in zip(args, args[1:])
                            if flag == '--filter' and value.startswith('label=com.docker.compose.service=')]
                self.assertEqual(len(services), 1)
                probed.append(services[0].removeprefix('label=com.docker.compose.service='))
            self.assertEqual(sorted(probed), sorted(obj.services.values()))

    def test_rollback_candidate_cleanup_failure_marks_degraded(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            obj = object.__new__(deploy.Deployment)
            obj.config = {'project': 'example-prod'}
            obj.state = {'sha': 'c' * 40, 'services': {'api': 'api_previous'}}
            obj.services = {'api': 'api_new'}
            obj.directory = directory
            obj.state_path = directory / 'state.json'
            obj.root, obj.release, obj.payload = directory, 'gh-test', payload()
            obj.live_caddy, obj.env, obj.rolling = (directory / name for name in ['live', 'env', 'rolling'])
            (directory / 'deploy').mkdir()
            obj.live_caddy.write_text('candidate')
            for name in ['before-caddy', 'before-release.env', 'before-rolling.json']:
                (directory / name).write_text('old')
            deploy.write_private(directory / 'before-state.json', obj.state)
            obj.reload = unittest.mock.Mock()
            obj.verify = unittest.mock.Mock()
            obj.update_monitoring = unittest.mock.Mock()
            obj.drain_retired = unittest.mock.Mock(return_value=True)
            def compose(*args, **kwargs):
                if args[0] == 'stop':
                    raise RuntimeError('docker stop failed')
            obj.compose = compose
            with patch.object(deploy, 'command', return_value='candidate-id'), \
                    contextlib.redirect_stdout(io.StringIO()), \
                    self.assertRaisesRegex(RuntimeError, 'docker stop failed'):
                obj.rollback()
            reason = json.loads(obj.degraded_path.read_text())['reason']
            self.assertIn('candidate cleanup failed', reason)
            self.assertIn('rollback restore was verified', reason)


if __name__ == '__main__': unittest.main()
