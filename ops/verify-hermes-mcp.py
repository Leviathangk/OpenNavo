#!/usr/bin/env python3
""" G: reuse port 18082 and simulate Hermes on owned port 8790; do not restart the API or read development configuration."""
import asyncio
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

from fastmcp import Client
from opennavo_mcp.tools.manifest import TOOLS, RESOURCE_PERMISSIONS

ROOT = Path(__file__).resolve().parent.parent
BASE = 'http://127.0.0.1:18082'
MCP = 'http://127.0.0.1:8790/mcp'
OUT = ROOT / 'apps/server/tmp/hermes-g'
ICON = 'https://raw.githubusercontent.com/github/explore/main/topics/python/python.png'
_checks = importlib.util.spec_from_file_location('mcp_verification', ROOT / 'ops/verify-mcp.py')
CHECKS = importlib.util.module_from_spec(_checks)
_checks.loader.exec_module(CHECKS)


class VerificationError(RuntimeError):
    """ Allow only credential-free messages constructed by the test driver."""


class Verification:
    def __init__(self):
        self.admin = ''
        self.gateway = (ROOT / 'apps/server/tmp/e2e/agent-gateway.key').read_text().strip()
        self.prefix = 'G-' + uuid.uuid4().hex[:10]
        self.tokens = []
        self.secrets = [self.gateway]
        self.passed = set()
        self.modes = {}
        self.steps = []
        self.denials = []
        self.report = {'prefix': self.prefix, 'steps': self.steps, 'denials': self.denials}
        self.client_id = None
        self.fixture = {}

    def http(self, method, path, data=None, *, token=None, gateway=True, expected='0000'):
        headers = {'X-Request-ID': str(uuid.uuid4())}
        if path.startswith('/admin-api') and self.admin:
            headers['Authorization'] = 'Bearer ' + self.admin
        if token is not None:
            headers.update({'Authorization': 'Bearer ' + token, 'X-Agent-Tool': 'g_verification',
                            'X-Agent-Client-IP': '127.0.0.1'})
            if gateway:
                headers['X-Agent-Gateway-Key'] = self.gateway
        if data is not None:
            headers['Content-Type'] = 'application/json'
        req = urllib.request.Request(BASE + path, headers=headers, method=method,
                                     data=None if data is None else json.dumps(data).encode())
        try:
            with urllib.request.urlopen(req, timeout=30) as reply:
                result = json.load(reply)
        except urllib.error.HTTPError as error:
            result = json.load(error)
        if result.get('code') != expected:
            raise VerificationError(f'{method} {path}: expected {expected}, got {result.get("code")}')
        return result.get('data')

    def backend(self, method, path, data=None, **kwargs):
        return self.http(method, '/admin-api' + path, data, **kwargs)

    def issue(self, permissions, *, delete=True, ips=None):
        issued = self.backend('POST', f'/agent/clients/{self.client_id}/tokens', {
            'name': self.prefix, 'permissions': permissions, 'allowDelete': delete,
            'ipAllowlist': ['127.0.0.1/32'] if ips is None else ips})
        self.tokens.append(issued['token']['id'])
        self.secrets.append(issued['plaintext'])
        return issued

    async def tool(self, client, name, **args):
        # The default limit is 120 requests/minute; tools may query tokens/objects again, so do not consume the shared E2E quota.
        await asyncio.sleep(0.65)
        result = await client.call_tool(name, args, raise_on_error=False)
        if result.is_error:
            # Extract only business codes from FastMCP errors; never include raw messages in verification artifacts.
            import re
            codes = re.findall(r'\b(?:100[1-6]|5000|7777|888[89])\b', str(result.content))
            raise VerificationError(f'tool {name} failed; codes={codes}')
        if result.structured_content is None or 'untrusted' not in result.structured_content:
            raise VerificationError('missing untrusted envelope: ' + name)
        self.passed.add(name)
        self.modes.setdefault(name, set()).add('dryRun' if args.get('dry_run') else 'execute')
        return result.structured_content['untrusted']

    def step(self, number, description):
        self.steps.append({'step': number, 'result': 'passed', 'description': description})
        print(f'G step {number}: passed', flush=True)

    async def denied(self, client, name, args, label):
        result = await client.call_tool(name, args, raise_on_error=False)
        if not result.is_error:
            raise VerificationError('denial failed: ' + label)
        self.denials.append(label)

    async def scenario(self):
        defaults = dict(line.split('=', 1) for line in (ROOT / 'apps/server/.env.example').read_text().splitlines()
                        if line and not line.startswith('#') and '=' in line)
        login = self.backend('POST', '/auth/login', {'userName': 'e2e-super',
                             'password': os.environ.get('E2E_ADMIN_PASSWORD', defaults['E2E_ADMIN_PASSWORD']).strip('"\'')})
        self.admin = login['token']
        self.secrets.append(self.admin)
        self.client_id = self.backend('POST', '/agent/clients', {'name': self.prefix,
                                      'notes': 'G MCP 18082 only'})['id']
        permissions = self.backend('GET', '/agent/settings')['grantablePermissions']
        issued = self.issue(permissions)
        token = issued['plaintext']
        for missing in [None, 'invalid-g-verification']:
            headers = {'Accept': 'application/json, text/event-stream', 'Content-Type': 'application/json'}
            if missing:
                headers['Authorization'] = 'Bearer ' + missing
            request = urllib.request.Request(MCP, headers=headers, method='POST',
                data=json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'tools/list'}).encode())
            try:
                urllib.request.urlopen(request, timeout=10).close()
            except urllib.error.HTTPError as error:
                assert error.code == 401
            else:
                raise VerificationError('MCP accepted missing or invalid token')
        self.denials.extend(['MCP missing token: 401', 'MCP invalid token: 401'])
        async with Client(MCP, auth=token) as client:
            assert {x.name for x in await client.list_tools()} == {x.name for x in TOOLS}
            assert {str(x.uri) for x in await client.list_resources()} == set(RESOURCE_PERMISSIONS)
            for uri in RESOURCE_PERMISSIONS:
                assert await client.read_resource(uri)
            identity = await self.tool(client, 'whoami')
            assert identity['active'] and len(identity['permissions']) == 21
            self.step(1, 'Issue token in admin, connect over real HTTP, read daily guide and seven resources')
            changes = await self.tool(client, 'catalog_changes', size=100)
            assert any(x['token'] == self.fixture['newToken'] and x['type'] == 'created' for x in changes['records'])
            assert any(x['token'] == self.fixture['updatedToken'] and x['type'] == 'updated' for x in changes['records'])
            packages = await self.tool(client, 'packages_search', size=100, q=self.prefix)
            selected = next(x for x in packages['records'] if x['token'] == self.fixture['newToken'])
            updated = next(x for x in packages['records'] if x['token'] == self.fixture['updatedToken'])
            release_pid = updated['id']
            pid, package_token = selected['id'], selected['token']
            self.report['packageId'] = pid
            await self.tool(client, 'package_get', package=pid, include=['display', 'translations', 'screenshots'])
            versions = await self.tool(client, 'package_versions', package=release_pid)
            version = versions['records'][0]['version']
            await self.tool(client, 'listing_gaps', size=1)
            self.step(2, 'Catalog changes and version queries')
            text = {'package': pid, 'source_locale': 'zh-CN', 'display_name': self.prefix + ' 应用',
                    'summary': self.prefix + ' 中文简介', 'description': '## G 功能\n保留 `code` 与 https://example.com 链接。'}
            before = await self.tool(client, 'package_get', package=pid)
            preview = await self.tool(client, 'package_set_text', **text, dry_run=True)
            assert preview['dryRun']
            assert await self.tool(client, 'package_get', package=pid) == before
            self.denials.append('dryRun makes no business changes')
            await self.tool(client, 'package_set_text', **text)
            await self.tool(client, 'translation_fix', ref={'entity': 'package', 'id': pid}, locale='en-US',
                            fields={'summary': self.prefix + ' English summary'})
            public = self.http('GET', f'/api/v1/packages/cask/{package_token}?locale=en-US')
            assert public['summary'] == self.prefix + ' English summary'
            status = await self.tool(client, 'translation_status', entity='package', id=pid)
            assert status
            self.report['automaticTranslations'] = 'E2E worker disabled; five-language fake-LLM testcontainers evidence required separately'
            self.step(3, 'Public reads of Chinese source and manual English; other languages are verified separately with a fake LLM')
            tree = await self.tool(client, 'categories_tree')
            category = next(x for x in tree if x.get('appliesTo') != 'formula')
            await self.tool(client, 'package_set_categories', package=pid,
                            items=[{'categoryId': category['id'], 'isPrimary': True}])
            await self.tool(client, 'package_update_meta', package=pid, tags=[self.prefix])
            self.step(4, 'Categories and tags')
            asset = await self.tool(client, 'asset_upload_from_url', url=ICON, kind='icon')
            aid = asset['id']
            await self.tool(client, 'package_set_icon', package=pid, asset_id=aid)
            colored = await self.tool(client, 'package_get', package=pid)
            assert colored['meta']['accentColor'] and colored['meta']['accentColor'].startswith('#')
            await self.tool(client, 'package_update_meta', package=pid, accent_color='#2468AC')
            assert (await self.tool(client, 'package_get', package=pid))['meta']['accentColor'].lower() == '#2468ac'
            self.step(5, 'Icon upload by URL, automatic and manual primary colors')
            await self.tool(client, 'package_update_meta', package=pid, download_size=12345678)
            assert (await self.tool(client, 'package_get', package=pid))['downloadSize'] == 12345678
            self.step(6, 'Write download size')
            mutation = await self.tool(client, 'release_write_notes', package=release_pid, version=version,
                                      source_locale='zh-CN', summary=self.prefix + ' 更新说明',
                                      sections=[{'area': '改进', 'items': ['保留 `code` 与 https://example.com。']}])
            assert mutation['revisionId']
            releases = await self.tool(client, 'releases_list', package_id=release_pid, view='releases')
            rid = next(x['id'] for x in releases['records'] if x['source'] == 'editorial')
            await self.tool(client, 'release_get', id=rid)
            await self.tool(client, 'release_update', id=rid, hidden=False)
            self.step(7, 'Chinese editorial release notes; automatic translation into five languages is verified separately with a fake LLM')
            cat = await self.tool(client, 'category_create', slug=self.prefix.lower(), icon='lucide:code-xml',
                                  applies_to='cask', visible=True, hidden_by_default=False,
                                  source_locale='zh-CN', i18n={'zh-CN': {'name': self.prefix + ' 分类'}})
            cid = cat['id']
            await self.tool(client, 'category_update', id=cid, i18n={'zh-CN': {'name': self.prefix + ' 分类更新'}})
            await self.tool(client, 'categories_reorder', items=[{'id': cid, 'sort': 999}])
            collection = await self.tool(client, 'collection_create', slug=self.prefix.lower(), sort=999,
                                        source_locale='zh-CN', i18n={'zh-CN': {'title': self.prefix + ' 合集'}})
            coid = collection['id']
            await self.tool(client, 'collection_update', id=coid, i18n={'zh-CN': {'title': self.prefix + ' 合集更新'}})
            await self.tool(client, 'collection_set_items', id=coid, items=[{'packageId': pid}])
            await self.tool(client, 'collection_publish', id=coid)
            await self.tool(client, 'collections_list', q=self.prefix)
            feature = await self.tool(client, 'feature_upsert', placement='home_secondary', target_type='package',
                                     package_id=pid, status='published', sort=999, source_locale='zh-CN',
                                     i18n={'zh-CN': {'title': self.prefix + ' 精选'}})
            fid = feature['id']
            await self.tool(client, 'features_list', id=fid)
            await self.tool(client, 'collection_delete', id=coid)
            trash = await self.tool(client, 'logs_search', kind='trash', entity='collection', object_key=str(coid))
            trash_id = next(x['id'] for x in trash['records'] if not x.get('restoredAt'))
            await self.tool(client, 'revision_restore', kind='trash', id=trash_id)
            assert (await self.tool(client, 'collection_get', id=coid))['id'] == coid
            self.step(8, 'Publish categories/collections/features; trash and restore a collection with its original ID')
            await self.tool(client, 'announcement_get')
            await self.tool(client, 'announcement_set', enabled=True, source_locale='zh-CN',
                            title=self.prefix + ' 公告', body=self.prefix + ' 公告正文')
            announcement = await self.tool(client, 'announcement_get')
            assert announcement['enabled'] is True
            assert announcement['sourceLocale'] == 'zh-CN'
            assert set(announcement['i18n']) == {'zh-CN', 'en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'}
            for locale, translation in announcement['i18n'].items():
                assert translation['status'] == ('source' if locale == 'zh-CN' else 'pending')
            public_announcement = self.http('GET', '/api/v1/config/client?platform=desktop&version=1.0.0&locale=zh-CN')['announcement']
            assert public_announcement['title'] == self.prefix + ' 公告'
            assert public_announcement['id'] and public_announcement['level'] == 'info'
            self.report['announcementEnabledAndFivePending'] = True
            # Admin prepares a G-specific draft; the Agent writes only that draft's notes, leaving the F3 release untouched.
            desktop = self.backend('POST', '/desktop-releases', {'version': f'99.0.{int(time.time())}',
                'channel': 'stable', 'artifacts': [{'target': 'darwin-aarch64',
                    'url': 'https://example.com/g-artifact.tar.gz', 'bytes': 1, 'sha256': 'a' * 64}],
                'sourceLocale': 'zh-CN', 'i18n': {'zh-CN': {'notes': self.prefix}}})
            did = desktop['id']
            await self.tool(client, 'desktop_releases_list', id=did)
            await self.tool(client, 'desktop_release_write_notes', id=did, source_locale='zh-CN', notes=self.prefix + ' 客户端说明')
            await self.tool(client, 'dashboard_overview')
            self.step(9, 'Announcements, G desktop draft notes, dashboard')
            change = await self.tool(client, 'package_set_text', package=pid, source_locale='zh-CN', summary=self.prefix + ' 撤回前')
            await self.tool(client, 'revision_restore', kind='request', request_id=change['requestId'])
            detail = await self.tool(client, 'package_get', package=pid)
            assert next(x['summary'] for x in detail['i18n'] if x['locale'] == 'zh-CN') == text['summary']
            self.step(10, 'Revert by request ID and confirm restored Chinese content')
            await self.remaining(client, pid, aid, cid, coid, fid)
            await self.refusals(client, token, pid, release_pid, version, did, coid)
            self.step(11, 'Protected system/remote-config/mirror/desktop-release rollback boundaries and permission denials')
            started = time.monotonic()
            self.backend('POST', f'/agent/tokens/{issued["token"]["id"]}/revoke')
            await self.denied(client, 'whoami', {}, 'Calls denied after token revocation')
            assert time.monotonic() - started < 30
            self.report['revocationSeconds'] = round(time.monotonic() - started, 3)
            self.step(12, 'Deny within 30 seconds of revocation')
        assert self.passed == {x.name for x in TOOLS}, 'tool coverage incomplete: ' + str(sorted({x.name for x in TOOLS} - self.passed))

    def upload(self, url, expected='0000'):
        # Post the file the way an Agent shell would; the image bytes never pass through MCP.
        body, content_type = CHECKS.icon_upload()
        req = urllib.request.Request(url, data=body, method='POST', headers={'Content-Type': content_type})
        try:
            with urllib.request.urlopen(req, timeout=30) as reply:
                result = json.load(reply)
        except urllib.error.HTTPError as error:
            result = json.load(error)
        if result.get('code') != expected:
            raise VerificationError(f'ticket upload: expected {expected}, got {result.get("code")}')
        return result.get('data')

    async def remaining(self, client, pid, aid, cid, coid, fid):
        await self.tool(client, 'assets_list', kind='icon')
        ticket = await self.tool(client, 'asset_upload_ticket', kind='screenshot', source_url=ICON)
        self.secrets.append(ticket['uploadUrl'].rsplit('/', 1)[1])
        local = self.upload(ticket['uploadUrl'])
        self.upload(ticket['uploadUrl'], expected='8888')
        self.denials.append('Upload ticket reuse')
        await self.tool(client, 'package_screenshot_add', package=pid, asset_id=local['id'], theme='dark',
                        source_locale='zh-CN', i18n={'zh-CN': {'caption': self.prefix + ' local'}})
        shot_asset = await self.tool(client, 'asset_upload_from_url', url=ICON, kind='screenshot')
        shot = await self.tool(client, 'package_screenshot_add', package=pid, asset_id=shot_asset['id'], theme='light',
                               source_locale='zh-CN', i18n={'zh-CN': {'caption': self.prefix}})
        sid = shot['id']
        await self.tool(client, 'package_screenshot_update', package=pid, screenshot_id=sid, theme='dark')
        shots = (await self.tool(client, 'package_get', package=pid, include=['screenshots']))['screenshots']
        await self.tool(client, 'package_screenshots_reorder', package=pid, ids=[x['id'] for x in shots])
        await self.tool(client, 'package_screenshot_delete', package=pid, screenshot_id=sid)
        await self.tool(client, 'feature_delete', id=fid)
        await self.tool(client, 'category_delete', id=cid)
        glossary = await self.tool(client, 'glossary_upsert', term=self.prefix, translations={'en-US': self.prefix}, do_not_translate=True)
        await self.tool(client, 'glossary_list', q=self.prefix)
        await self.tool(client, 'glossary_delete', id=glossary['id'])
        await self.tool(client, 'translation_list', entity='package', size=1)
        await self.tool(client, 'translation_retranslate', ref={'entity': 'package', 'id': pid}, locales=['ja-JP'])
        await self.tool(client, 'jobs_runs', size=1)
        await self.tool(client, 'queues_status')
        await self.tool(client, 'package_refresh', package=pid, dry_run=True)
        await self.tool(client, 'job_trigger', job_type='snapshot_build', dry_run=True)
        await self.tool(client, 'search_insights')
        synonyms = await self.tool(client, 'synonym_upsert', terms=[self.prefix, self.prefix + '-en'], enabled=True)
        await self.tool(client, 'synonyms_list', q=self.prefix)
        await self.tool(client, 'synonym_delete', id=synonyms['id'])
        feedback = self.http('POST', '/api/v1/feedback', {'type': 'wrong_info', 'packageKind': 'cask',
                         'packageToken': (await self.tool(client, 'package_get', package=pid))['token'],
                         'content': self.prefix + ' 测试反馈', 'platform': 'web', 'website': ''})
        await self.tool(client, 'feedback_list', size=1)
        await self.tool(client, 'feedback_get', id=feedback['id'])
        await self.tool(client, 'feedback_update', id=feedback['id'], status='resolved', handler_note=self.prefix)
        self.report['tools'] = sorted(self.passed)
        print('G successful tools: ' + str(len(self.passed)), flush=True)

    async def refusals(self, client, token, pid, release_pid, version, did, coid):
        for method, path in [('GET', '/system/users'), ('GET', '/app-config'), ('GET', '/mirrors'),
                             ('POST', '/desktop-releases'), ('POST', f'/desktop-releases/{did}/publish'),
                             ('POST', f'/desktop-releases/{did}/rollback'), ('GET', '/agent/clients')]:
            self.http(method, '/agent-api' + path, {} if method == 'POST' else None, token=token, expected='1004')
            self.denials.append(method + ' ' + path)
        self.http('POST', '/agent-api/introspect', token=token, gateway=False, expected='1004')
        self.denials.append('Missing gateway secret')
        self.http('POST', '/agent-api/introspect', token='invalid-g-verification', expected='8888')
        self.denials.append('Invalid token')
        for url in ['https://127.0.0.1/icon.png', 'https://169.254.169.254/icon.png']:
            await self.denied(client, 'asset_upload_from_url', {'url': url, 'kind': 'icon'}, 'SSRF ' + url)
        read = self.issue(['catalog:package:view'], delete=False)
        async with Client(MCP, auth=read['plaintext']) as reader:
            names = {x.name for x in await reader.list_tools()}
            assert 'package_set_text' not in names and 'packages_search' in names
            await self.denied(reader, 'package_set_text', {'package': pid, 'source_locale': 'zh-CN', 'summary': 'denied'}, 'Discovery and invocation without granted permission')
        nodelete = self.issue(self.backend('GET', '/agent/settings')['grantablePermissions'], delete=False)
        async with Client(MCP, auth=nodelete['plaintext']) as protected:
            assert 'collection_delete' not in {x.name for x in await protected.list_tools()}
            await self.denied(protected, 'collection_delete', {'id': coid}, 'Collection deletion forbidden')
            await self.denied(protected, 'package_set_icon', {'package': pid, 'remove': True}, 'Icon removal branch forbidden')
            await self.denied(protected, 'release_write_notes', {'package': release_pid, 'version': version, 'clear': True}, 'Release-note clear branch forbidden')
        badip = self.issue(['catalog:package:view'], ips=['192.0.2.1/32'])
        self.http('POST', '/agent-api/introspect', token=badip['plaintext'], expected='1004')
        self.denials.append('IP allowlist mismatch')
        self.backend('PUT', f'/agent/clients/{self.client_id}', {'enabled': False})
        try:
            self.http('POST', '/agent-api/introspect', token=token, expected='8889')
            self.denials.append('Disable G client')
        finally:
            self.backend('PUT', f'/agent/clients/{self.client_id}', {'enabled': True})
        await self.denied(client, 'package_set_text', {'package': pid, 'source_locale': 'zh-CN', 'unexpected': True}, 'Reject unknown arguments')

    def cleanup(self):
        for token_id in self.tokens:
            self.backend('POST', f'/agent/tokens/{token_id}/revoke')
        if self.client_id:
            self.backend('PUT', f'/agent/clients/{self.client_id}', {'enabled': False})


async def main():
    OUT.mkdir(parents=True, exist_ok=True)
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', 8790))
    verification = Verification()
    verification.http('GET', '/readyz')
    # Fixture preparation belongs to the test driver; the MCP service itself calls only /agent-api.
    spec = importlib.util.spec_from_file_location('g_e2e_environment', ROOT / 'ops/e2e.py')
    e2e = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(e2e)
    fixture_env = e2e.environment(False)
    fixture_env['HERMES_E2E_PREFIX'] = verification.prefix
    binary = OUT / 'fixture'
    subprocess.run(['go', 'build', '-o', str(binary), './cmd/hermes-e2e-fixture'],
                   cwd=ROOT / 'apps/server', env=fixture_env, check=True, capture_output=True)
    prepared = subprocess.run([str(binary)], cwd=OUT, env=fixture_env, check=True, capture_output=True)
    verification.fixture = json.loads(prepared.stdout)
    verification.report['catalogFixture'] = verification.fixture
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'SYSTEMROOT')}
    env.update(AGENT_GATEWAY_SECRET=verification.gateway, OPENNAVO_AGENT_API_BASE=BASE + '/agent-api',
               OPENNAVO_MCP_HOST='127.0.0.1', OPENNAVO_MCP_PORT='8790')
    with (OUT / 'mcp.log').open('wb') as log:
        process = subprocess.Popen([str(ROOT / 'apps/mcp/.venv/bin/opennavo-mcp')], cwd=ROOT / 'apps/mcp', env=env, stdout=log, stderr=log)
        try:
            for _ in range(100):
                if process.poll() is not None:
                    raise VerificationError('owned MCP exited during startup')
                try:
                    with urllib.request.urlopen('http://127.0.0.1:8790/healthz', timeout=1):
                        break
                except OSError:
                    await asyncio.sleep(0.2)
            else:
                raise VerificationError('MCP health timeout')
            await verification.scenario()
            verification.report['passed'] = True
        finally:
            try:
                verification.cleanup()
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
                verification.report['ownedMCPStopped'] = process.poll() is not None
                verification.report['tools'] = sorted(verification.passed)
                verification.report['toolModes'] = {name: sorted(modes) for name, modes in sorted(verification.modes.items())}
                logs = (OUT / 'mcp.log').read_bytes() + (ROOT / 'apps/server/tmp/e2e/api.log').read_bytes()
                matches = sum(secret.encode() in logs for secret in verification.secrets if secret)
                verification.report['credentialLogMatches'] = matches
                (OUT / 'report.json').write_text(json.dumps(verification.report, ensure_ascii=False, indent=2))
                if matches:
                    raise VerificationError('credential detected in API/MCP logs; content withheld')
    print('G MCP verification passed; 12 steps, 61 tools; credentials revoked; owned MCP stopped', flush=True)


if __name__ == '__main__':
    try:
        asyncio.run(main())
    except Exception as error:
        # Print only short messages controlled by this script; third-party errors may contain authentication responses, so omit tracebacks.
        print(str(error) if type(error) is VerificationError else 'G verification failed: ' + type(error).__name__, file=sys.stderr)
        import traceback
        print('script lines: ' + str([frame.lineno for frame in traceback.extract_tb(error.__traceback__)
                                     if frame.filename == __file__]), file=sys.stderr)
        sys.exit(1)
