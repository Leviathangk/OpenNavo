#!/usr/bin/env python3
""" Verify production backend, routing, monitoring, and backup/restore in a fixed isolated project."""
import argparse
import base64
from html.parser import HTMLParser
import importlib.util
import json
import os
import secrets
import socket
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
PROJECT = 'opennavo-prod-verify'


def environment():
    # These are public verification fixture values; never read or copy local development secrets.
    inherited = {key: value for key, value in os.environ.items()
                 if key in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG',
                            'TMPDIR', 'SYSTEMROOT', 'SSH_AUTH_SOCK')}
    return dict(inherited, SERVER_IMAGE='opennavo-server:verify', OPS_IMAGE='opennavo-ops:verify',
        WEB_IMAGE='opennavo-web:verify', MCP_IMAGE='opennavo-mcp:verify',
        POSTGRES_IMAGE='opennavo-postgres:verify', ASYNQMON_IMAGE='opennavo-asynqmon:verify',
        REDIS_IMAGE='opennavo-redis:verify', CADDY_IMAGE='opennavo-caddy:verify',
        AGENT_GATEWAY_SECRET=secrets.token_urlsafe(48),
        CADDY_PROXY_SUBNET='10.250.90.0/24', CADDY_PROXY_IP_RANGE='10.250.90.128/25',
        CADDY_PROXY_IP='10.250.90.2', SSR_PROXY_SUBNET='10.250.91.0/24', ADMIN_DIST=str(ROOT / 'apps/admin/dist'),
        NUXT_OG_IMAGE_SECRET=secrets.token_hex(32),
        WEB_DOMAIN='http://web.opennavo.localhost', API_DOMAIN='http://api.opennavo.localhost',
        ADMIN_DOMAIN='http://admin.opennavo.localhost', CADDY_GLOBAL_OPTIONS='auto_https off',
        HTTP_BIND='127.0.0.1', HTTP_PORT='28180', HTTPS_PORT='28443',
        PUBLIC_BASE_URL='http://api.opennavo.localhost:28180', WEB_BASE_URL='http://web.opennavo.localhost:28180',
        NUXT_PUBLIC_SITE_URL='http://web.opennavo.localhost:28180',
        NUXT_PUBLIC_I18N_BASE_URL='http://web.opennavo.localhost:28180',
        ADMIN_ORIGIN='http://admin.opennavo.localhost:28180', CDN_BASE_URL='http://cdn.opennavo.localhost:29000/opennavo',
        DATABASE_URL='postgres://opennavo_app:verification-app-password@postgres:5432/opennavo?sslmode=disable',
        MIGRATION_DATABASE_URL='postgres://opennavo_migrate:verification-migration-password@postgres:5432/opennavo?sslmode=disable',
        MIGRATION_DATABASE_PASSWORD='verification-migration-password', APP_DATABASE_PASSWORD='verification-app-password',
        REDIS_URL='redis://redis:6379/0', S3_ENDPOINT='minio:9000', S3_USE_SSL='false',
        S3_ACCESS_KEY='verification-minio', S3_SECRET_KEY='verification-storage-password',
        S3_BUCKET='opennavo', S3_REGION='us-east-1', JWT_SECRET='verification-only-jwt-32-characters-minimum',
        CI_RELEASE_TOKEN='verification-ci-only', LLM_ENABLED='false',
        LLM_API_KEY='', LLM_BASE_URL='http://127.0.0.1:1/v1', LLM_CONCURRENCY='4', HOMEBREW_API_BASE='http://web:3000/api',
        ADMIN_BOOTSTRAP_USERNAME='verification-admin', ADMIN_BOOTSTRAP_PASSWORD='verification-admin-password',
        GRAFANA_ADMIN_PASSWORD='verification-grafana-password',
        ASYNQMON_PORT='29881', PROMETHEUS_PORT='29091', GRAFANA_PORT='29092')


ENV = environment()
BROWSER_DIRECTORY = ROOT / 'apps/server/tmp/review/production_browser'
BROWSER_PAGES = ['/release/desktop', '/release/mirror', '/release/config', '/changelog/release', '/changelog/review']
COMPOSE = ['docker', 'compose', '-p', PROJECT, '-f', 'ops/docker-compose.prod.yml',
           '-f', 'ops/docker-compose.verify.yml', '--profile', 'local-db', '--profile', 'monitoring',
           '--profile', 'ops', '--profile', 'backup']


def run(args, capture=True, check=True, env=None, input=None):
    r = subprocess.run(args, cwd=ROOT, env=env or ENV, capture_output=capture, check=False, input=input)
    if check and r.returncode:
        # Do not print full commands or expanded environment configuration; expose only safe failure-stage details.
        detail = (r.stderr or b'').decode(errors='replace')
        for value in (env or ENV).values():
            if len(value) >= 8:
                detail = detail.replace(value, '[redacted]')
        raise RuntimeError(f'{args[0]} operation failed ({r.returncode}): {detail[-1500:]}')
    return r


def compose(*args, **kwargs):
    return run(COMPOSE + list(args), **kwargs)


def sql(query, app=False, check=True):
    env = dict(ENV, PGUSER='opennavo_app' if app else 'opennavo_migrate',
               PGPASSWORD=ENV['APP_DATABASE_PASSWORD' if app else 'MIGRATION_DATABASE_PASSWORD'])
    return compose('exec', '-T', '-e', 'PGUSER', '-e', 'PGPASSWORD', 'postgres',
                   'psql', '-h', '127.0.0.1', '-d', 'opennavo', '-At', '-v', 'ON_ERROR_STOP=1', '-c', query,
                   env=env, check=check)


def fingerprint():
    names = sql("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename").stdout.decode().splitlines()
    result = {}
    for name in names:
        if not name.replace('_', '').isalnum():
            raise RuntimeError('unexpected table identifier')
        result[name] = sql(f'SELECT count(*),md5(COALESCE(string_agg(to_jsonb(t)::text,\',\' ORDER BY to_jsonb(t)::text),\'\')) FROM public."{name}" t').stdout.decode().strip()
    return result


def http(path='/', host='api.opennavo.localhost', method='GET', data=None, authorization=None, port=28180, extra_headers=None):
    headers = {'Host': host, **(extra_headers or {})}
    if authorization:
        headers['Authorization'] = authorization
    # Raw bytes (such as multipart uploads) are sent as-is with the caller's Content-Type.
    if data is not None and not isinstance(data, bytes):
        headers['Content-Type'] = 'application/json'
        data = json.dumps(data).encode()
    req = urllib.request.Request(f'http://127.0.0.1:{port}{path}', headers=headers, method=method, data=data)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, dict(r.headers), r.read()
    except urllib.error.HTTPError as r:
        return r.code, dict(r.headers), r.read()


def wait_for(description, check, timeout=120):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        try:
            if check():
                print(description + ': passed', flush=True)
                return
        except (OSError, urllib.error.URLError, AssertionError, KeyError):
            pass
        time.sleep(1)
    raise RuntimeError(description + ' timed out')


class HTMLDocument(HTMLParser):
    def __init__(self, body):
        super().__init__()
        self.visible = []
        self.scripts = []
        self.og_images = []
        self.og_urls = []
        self.canonicals = []
        self.hreflangs = []
        self.script_depth = 0
        self.feed(body.decode())

    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        if tag == 'meta' and attributes.get('property') == 'og:image':
            self.og_images.append(attributes.get('content', ''))
        if tag == 'meta' and attributes.get('property') == 'og:url':
            self.og_urls.append(attributes.get('content', ''))
        if tag == 'link' and attributes.get('rel') == 'canonical':
            self.canonicals.append(attributes.get('href', ''))
        if tag == 'link' and attributes.get('rel') == 'alternate' and attributes.get('hreflang'):
            self.hreflangs.append(attributes.get('href', ''))
        if tag == 'script':
            self.script_depth += 1
            src = dict(attrs).get('src')
            if src:
                self.scripts.append(src)

    def handle_endtag(self, tag):
        if tag == 'script':
            self.script_depth = max(0, self.script_depth - 1)

    def handle_data(self, data):
        if not self.script_depth:
            self.visible.append(data)


def web_containers():
    return run(['docker', 'ps', '-q', '--filter', f'label=com.docker.compose.project={PROJECT}',
        '--filter', 'label=com.docker.compose.service=web']).stdout.decode().splitlines()


def web_transmitted(containers):
    # Read isolated container network counters to identify real Nuxt responses without adding test headers to production responses.
    code = """
const fs = require('node:fs');
const root = '/sys/class/net';
let total = 0n, counters = 0;
for (const name of fs.readdirSync(root)) {
    if (name === 'lo') continue;
    const path = root + '/' + name + '/statistics/tx_bytes';
    // Class entries can be interface symlinks or regular files such as bonding_masters.
    try {
        if (!fs.statSync(path).isFile()) continue;
    } catch (error) {
        if (error.code === 'ENOENT' || error.code === 'ENOTDIR') continue;
        throw error;
    }
    const value = fs.readFileSync(path, 'utf8').trim();
    if (!/^\\d+$/.test(value)) throw new Error('Invalid interface transmission counter');
    total += BigInt(value);
    counters++;
}
if (!counters) throw new Error('No non-loopback transmission counters available');
process.stdout.write(String(total));
"""
    return {c: int(run(['docker', 'exec', c, 'node', '-e', code]).stdout) for c in containers}


def check_web_round_robin():
    containers = web_containers()
    assert len(containers) == 2, 'two real Nuxt replicas'
    responders = []
    for _ in range(12):
        before = web_transmitted(containers)
        status, _, body = http('/', 'web.opennavo.localhost')
        assert status == 200
        after = web_transmitted(containers)
        witnesses = [c for c in containers if after[c] - before[c] > max(1024, len(body) // 4)]
        assert len(witnesses) == 1, 'response bytes must identify one Nuxt replica'
        responders.append(witnesses[0])
    assert set(responders) == set(containers), 'both Nuxt replicas must answer'
    assert all(a != b for a, b in zip(responders, responders[1:])), 'Caddy must alternate replicas'
    return {c: responders.count(c) for c in containers}


def verify_routes():
    # Retry only unauthenticated readiness probes. Contract failures must retain
    # their first diagnostic and must not consume the login rate limit.
    wait_for('production route readiness', lambda: all(
        http(path, host)[0] == 200 for path, host in [
            ('/readyz', 'api.opennavo.localhost'),
            ('/', 'web.opennavo.localhost'), ('/zh', 'web.opennavo.localhost'),
            ('/', 'admin.opennavo.localhost')]))
    check_routes()
    print('production routes, headers and authentication: passed', flush=True)


def check_routes():
    for path in ['/', '/zh']:
        status, headers, body = http(path, 'web.opennavo.localhost')
        assert status == 200 and 'text/html' in headers.get('Content-Type', '')
        visible = ' '.join(HTMLDocument(body).visible)
        name = 'Visual Studio Code' if path == '/' else 'VS Code 编辑器'
        assert name in visible, 'database data must be server-rendered outside scripts'
        prefix = '' if path == '/' else '/zh'
        assert f'href="{prefix}/apps/visual-studio-code"' in body.decode()
        assert '/cli/ripgrep' not in body.decode(), 'web catalog must remain cask-only'
        for name in ['Content-Security-Policy', 'Strict-Transport-Security', 'X-Content-Type-Options', 'Referrer-Policy']:
            assert any(k.lower() == name.lower() for k in headers), name
    status, _, body = http('/', 'admin.opennavo.localhost')
    assert status == 200 and b'id="app"' in body
    scripts = HTMLDocument(body).scripts
    assert scripts and all(src.startswith('/') for src in scripts)
    for src in scripts:
        status, headers, script = http(src, 'admin.opennavo.localhost')
        assert status == 200 and 'javascript' in headers.get('Content-Type', '') and script
    assert all(http(path, 'admin.opennavo.localhost')[0] == 200 for path in BROWSER_PAGES), 'admin SPA fallback'
    for path in ['/api/health', '/readyz']:
        status, headers, body = http(path, 'admin.opennavo.localhost')
        assert status == 200 and headers.get('Content-Type', '').split(';', 1)[0] == 'application/json'
        assert json.loads(body) == {'status': 'ok'}, f'admin health response: {path}'
    status, _, body = http('/api/v1/packages')
    assert status == 200 and json.loads(body)['code'] == '0000'
    status, _, body = http('/api/v1/config/client?platform=desktop&version=0.3.0')
    assert status == 200 and json.loads(body)['data']['links']['download'] == ENV['WEB_BASE_URL'] + '/download'
    status, _, body = http('/admin-api/auth/getUserInfo', 'admin.opennavo.localhost')
    assert status == 200 and json.loads(body)['code'] == '8888'
    assert http('/admin-api/auth/getUserInfo')[0] == 404
    status, _, body = http('/admin-api/auth/login', 'admin.opennavo.localhost', 'POST',
                          {'userName': ENV['ADMIN_BOOTSTRAP_USERNAME'], 'password': ENV['ADMIN_BOOTSTRAP_PASSWORD']})
    login = json.loads(body)
    assert status == 200 and login['code'] == '0000', f'admin auth/login: HTTP {status}, code {login.get("code")}'
    pair = login['data']
    status, _, body = http('/admin-api/dashboard/overview', 'admin.opennavo.localhost', authorization='Bearer ' + pair['token'])
    assert status == 200 and json.loads(body)['code'] == '0000'
    for path in ['/desktop-releases', '/mirrors', '/app-config', '/releases', '/translations/queue?type=release&status=machine&locale=zh-CN']:
        status, _, body = http('/admin-api' + path, 'admin.opennavo.localhost', authorization='Bearer ' + pair['token'])
        envelope = json.loads(body)
        assert status == 200 and envelope['code'] == '0000', f'admin production endpoint: {path}: HTTP {status}, code {envelope.get("code")}'
        records = envelope['data'].get('records') if isinstance(envelope['data'], dict) else envelope['data']
        assert records, f'admin verification fixtures: {path}'
        if path.startswith('/translations/queue'):
            assert all(record['type'] == 'release' and record['status'] == 'machine'
                       and record['locale'] == 'zh-CN' and record['translated']
                       for record in records), 'release translation fixtures must contain machine translations in zh-CN'


def check_og_image():
    status, _, body = http('/apps/visual-studio-code', 'web.opennavo.localhost')
    assert status == 200, 'detail SSR for share image'
    images = HTMLDocument(body).og_images
    assert images, 'detail must include og:image'
    parsed = urllib.parse.urlsplit(images[0])
    assert parsed.scheme in ('http', 'https') and parsed.hostname == 'web.opennavo.localhost', f'runtime OG origin: {parsed.scheme}://{parsed.netloc}'
    path = parsed.path + ('?' + parsed.query if parsed.query else '')
    status, headers, image = http(path, 'web.opennavo.localhost')
    assert status == 200 and headers.get('Content-Type', '').startswith('image/png'), 'share image HTTP/PNG type'
    assert image.startswith(b'\x89PNG\r\n\x1a\n'), 'share image PNG signature'
    return {'status': status, 'contentType': headers['Content-Type'], 'bytes': len(image)}


def assert_web_origin(url):
    parsed, expected = urllib.parse.urlsplit(url), urllib.parse.urlsplit(ENV['WEB_BASE_URL'])
    assert (parsed.scheme, parsed.netloc) == (expected.scheme, expected.netloc), f'runtime site origin: {parsed.scheme}://{parsed.netloc}'
    return parsed.path + ('?' + parsed.query if parsed.query else '')


def check_seo_origins():
    pages = ['/apps/visual-studio-code', '/zh/apps/visual-studio-code']
    for path in pages:
        status, _, body = http(path, 'web.opennavo.localhost')
        assert status == 200
        document = HTMLDocument(body)
        assert [assert_web_origin(url) for url in document.canonicals] == [path], 'canonical must retain the locale route'
        assert set(pages).issubset({assert_web_origin(url) for url in document.hreflangs}), 'alternates must include English root and Chinese prefix'
        for urls in [document.og_images, document.og_urls, document.canonicals, document.hreflangs]:
            assert urls, 'detail must include image, URL, canonical and alternate language URLs'
            for url in urls:
                assert_web_origin(url)
    status, _, robots = http('/robots.txt', 'web.opennavo.localhost')
    assert status == 200
    indexes = [line.split(':', 1)[1].strip() for line in robots.decode().splitlines() if line.lower().startswith('sitemap:')]
    assert indexes, 'robots must advertise sitemap'
    pending, seen, package_urls = list(indexes), set(), 0
    while pending:
        url = pending.pop()
        if url in seen:
            continue
        assert len(seen) < 16, 'bounded sitemap traversal'
        seen.add(url)
        status, _, xml = http(assert_web_origin(url), 'web.opennavo.localhost')
        assert status == 200
        root = ET.fromstring(xml)
        for location in root.findall('.//{*}loc'):
            assert_web_origin(location.text)
            if root.tag.endswith('sitemapindex'):
                pending.append(location.text)
            elif '/apps/' in location.text:
                package_urls += 1
        for link in root.findall('.//{http://www.w3.org/1999/xhtml}link'):
            assert_web_origin(link.attrib['href'])
    assert package_urls >= 6, 'package sitemap must contain real cask and language URLs'
    return {'detailPages': len(pages), 'sitemapFiles': len(seen), 'packageUrls': package_urls, 'origin': ENV['WEB_BASE_URL']}


def check_monitoring():
    status, _, data = http('/api/v1/targets', host='localhost', port=29091)
    assert status == 200
    targets = json.loads(data)['data']['activeTargets']
    assert sum(t['health'] == 'up' and t['labels']['job'] == 'api' for t in targets) == 2
    assert any(t['health'] == 'up' and t['labels']['job'] == 'asynq' for t in targets)
    status, _, data = http('/api/health', host='localhost', port=29092)
    assert status == 200 and json.loads(data)['database'] == 'ok'
    auth = 'Basic ' + base64.b64encode(('admin:' + ENV['GRAFANA_ADMIN_PASSWORD']).encode()).decode()
    status, _, data = http('/api/dashboards/uid/opennavo-operations', host='localhost', port=29092, authorization=auth)
    assert status == 200 and len(json.loads(data)['dashboard']['panels']) == 12
    status, _, data = http('/api/scheduler_entries', host='localhost', port=29881)
    # enrich:schedule / translate:schedule were retired in plans/hermes-content.md section 3.1.
    assert status == 200 and len(json.loads(data)['entries']) == 6


def wait_for_browser(report, timeout=900):
    # Browser verification must use browser-use; the handshake contains only public URLs/results, never account tokens.
    BROWSER_DIRECTORY.mkdir(parents=True, exist_ok=True)
    ready = BROWSER_DIRECTORY / 'ready.json'
    done = BROWSER_DIRECTORY / 'done.json'
    ready.write_text(json.dumps({'adminUrl': ENV['ADMIN_ORIGIN'], 'pages': BROWSER_PAGES}, indent=2))
    print('production browser-use checkpoint ready: ' + str(ready.relative_to(ROOT)), flush=True)
    end = time.monotonic() + timeout
    try:
        while time.monotonic() < end:
            if done.is_file():
                result = json.loads(done.read_text())
                assert result.get('browser') == 'browser-use' and result.get('loginVerified') is True
                pages = result['pages']
                assert {page['path'] for page in pages} == set(BROWSER_PAGES) and len(pages) == len(BROWSER_PAGES)
                assert all(page.get('passed') is True for page in pages), 'production browser page checks failed'
                report['adminBrowser'] = result
                print('five real production admin pages via browser-use: passed', flush=True)
                return
            time.sleep(1)
        raise RuntimeError('production browser-use checkpoint timed out')
    finally:
        ready.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--skip-build', action='store_true')
    parser.add_argument('--build-concurrency', type=int, default=2, help='limit Go compilation in the local verification build')
    parser.add_argument('--browser-check', action='store_true')
    parser.add_argument('--mcp-only', action='store_true', help='only exercise real production API/MCP/Caddy; no frontend build')
    args = parser.parse_args()
    if not 1 <= args.build_concurrency <= 16:
        parser.error('--build-concurrency must be between 1 and 16')
    if args.mcp_only and args.browser_check:
        parser.error('--mcp-only cannot include frontend browser checks')
    # Existing resources with matching names may belong to others; finally may remove only resources created by this run.
    for resource, arguments in [('containers', ['ps', '-aq']), ('volumes', ['volume', 'ls', '-q']), ('networks', ['network', 'ls', '-q'])]:
        if run(['docker', *arguments, '--filter', f'label=com.docker.compose.project={PROJECT}']).stdout.strip():
            raise RuntimeError('verification project already has ' + resource + '; refusing to reuse or remove them')
    if args.mcp_only:
        directory = ROOT / 'apps/server/tmp/hermes-b5/empty-admin'
        directory.mkdir(parents=True, exist_ok=True)
        ENV['ADMIN_DIST'] = str(directory)
    # Check bind availability only; do not stop processes already using the ports.
    for port in [28180, 28443, 29000, 29091, 29092, 29881]:
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(('127.0.0.1', port))
    if args.browser_check:
        (BROWSER_DIRECTORY / 'ready.json').unlink(missing_ok=True)
        (BROWSER_DIRECTORY / 'done.json').unlink(missing_ok=True)
    if not args.skip_build:
        targets = [(ENV['SERVER_IMAGE'], 'server'), (ENV['POSTGRES_IMAGE'], 'postgres'),
                   (ENV['REDIS_IMAGE'], 'redis'), (ENV['CADDY_IMAGE'], 'caddy')]
        if not args.mcp_only:
            targets.extend([(ENV['OPS_IMAGE'], 'ops'), (ENV['ASYNQMON_IMAGE'], 'asynqmon')])
        # Create this run's build copy only in an ignored directory; do not edit another owner's apps/server/Dockerfile.
        build_directory = ROOT / 'apps/server/tmp/production-build'
        build_directory.mkdir(parents=True, exist_ok=True)
        dockerfile = build_directory / 'Dockerfile'
        source = (ROOT / 'apps/server/Dockerfile').read_text()
        source = source.replace('WORKDIR /src', f'ENV GOMAXPROCS={args.build_concurrency} GOFLAGS=-p={args.build_concurrency}\nWORKDIR /src', 1)
        dockerfile.write_text(source)
        for tag, target in targets:
            run(['docker', 'build', '-f', str(dockerfile), '-t', tag, '--target', target, 'apps/server'], capture=False)
        run(['docker', 'build', '-t', ENV['MCP_IMAGE'], 'apps/mcp'], capture=False)
        if not args.mcp_only:
            run(['docker', 'build', '-f', 'apps/web/Dockerfile', '-t', ENV['WEB_IMAGE'], '.'], capture=False)
            run(['pnpm', '--filter', '@opennavo/admin', 'build'], capture=False)
    if not args.mcp_only and not (ROOT / 'apps/admin/dist/index.html').is_file():
        raise RuntimeError('real admin bundle is required; build it before --skip-build')
    compose('config', '--quiet')
    report = {'project': PROJECT, 'scope': 'backend-mcp' if args.mcp_only else 'full-production',
              'frontend': 'not exercised in backend/MCP mode' if args.mcp_only else 'real Nuxt image and soybean production bundle'}
    try:
        compose('up', '-d', '--wait', 'postgres', 'redis', 'minio')
        compose('run', '--rm', 'migrate', 'up')
        compose('run', '--rm', 'db-permissions')
        compose('run', '--rm', 'storage-init')
        compose('run', '--rm', 'seed')
        # Seed only the fixed verification project; retain seed-e2e's production refusal.
        compose('run', '--rm', '--entrypoint', '/seed-e2e', '-e', 'APP_ENV=dev', 'seed')
        assert sql('SELECT count(*) FROM packages').stdout.strip() == b'60'
        report['fixturePackages'] = 60
        sql('ALTER ROLE opennavo_app SUPERUSER CREATEDB CREATEROLE; GRANT CREATE ON SCHEMA public TO opennavo_app;')
        compose('run', '--rm', 'db-permissions')
        assert sql('CREATE TABLE forbidden_ddl(id integer)', app=True, check=False).returncode != 0
        assert sql("SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname='opennavo_app'").stdout.strip() == b'f'
        assert sql("SELECT has_schema_privilege('opennavo_app','public','CREATE')").stdout.strip() == b'f'
        print('migrations, seed and application DDL restrictions: passed', flush=True)
        if args.mcp_only:
            compose('up', '-d', '--wait', 'api', 'mcp')
            compose('up', '-d', '--no-deps', 'caddy')
        else:
            compose('up', '-d', '--wait', 'api', 'worker', 'web', 'mcp', 'caddy', 'asynqmon', 'prometheus', 'grafana')
        wait_for('production API readiness', lambda: http('/readyz')[0] == 200)
        mcp_spec = importlib.util.spec_from_file_location('mcp_verification', ROOT / 'ops/verify-mcp.py')
        mcp_verification = importlib.util.module_from_spec(mcp_spec)
        mcp_spec.loader.exec_module(mcp_verification)
        report['mcp'] = mcp_verification.verify(sys.modules[__name__])
        if args.mcp_only:
            return 0
        verify_routes()
        report['webReplicaResponses'] = check_web_round_robin()
        cache_keys = compose('exec', '-T', 'web-cache', 'redis-cli', '-n', '0', '--scan', '--pattern', 'opennavo:web:cache:*').stdout.decode().splitlines()
        assert len(cache_keys) >= 2, 'both SSR routes must persist their cache in the dedicated web-cache Redis'
        report['webRedisCacheKeys'] = len(cache_keys)
        report['serverRenderedRoutes'] = ['/', '/zh']
        report['adminBundleAndSeedLogin'] = True
        report['detailOgImage'] = check_og_image()
        report['seoOrigins'] = check_seo_origins()
        for key in cache_keys:
            assert len(key.split(':')) > 4, 'route cache namespace must include build ID'
            ttl = int(compose('exec', '-T', 'web-cache', 'redis-cli', '-n', '0', 'TTL', key).stdout)
            ceiling = 21600 if ':nuxt-og-image:' in key else 3600
            assert 0 < ttl <= ceiling, 'cache entries must respect bounded retention'
        report['webRedisCacheTtlSeconds'] = 3600
        wait_for('two API scrapes, six schedules and Grafana dashboard', lambda: (check_monitoring() or True))
        spec = importlib.util.spec_from_file_location('rollout', ROOT / 'ops/rollout.py')
        rollout = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(rollout)
        rollout.Rollout(PROJECT, ENV, ['ops/docker-compose.prod.yml', 'ops/docker-compose.verify.yml']).apply()
        verify_routes()
        wait_for('monitoring after backend rolling update', lambda: (check_monitoring() or True))
        report['backendRollingUpdate'] = True
        compose('stop', 'api', 'worker')
        before = fingerprint()
        result = compose('run', '--rm', '--no-deps', 'backup', 'create')
        key = result.stdout.decode().strip().splitlines()[-1]
        assert key.startswith('backups/postgres/opennavo/') and key.endswith('.dump')
        with_error = http('/opennavo/' + key, host='localhost', port=29000)
        assert with_error[0] == 403, 'backup object must be private'
        sql('DROP SCHEMA public CASCADE; CREATE SCHEMA public AUTHORIZATION opennavo_migrate;')
        assert sql("SELECT count(*) FROM pg_tables WHERE schemaname='public'").stdout.strip() == b'0'
        compose('run', '--rm', '--no-deps', 'backup', 'restore', key)
        after = fingerprint()
        assert before == after, 'table counts and hashes must survive restore'
        compose('run', '--rm', 'db-permissions')
        compose('up', '-d', '--wait', 'api', 'worker')
        assert http('/readyz')[0] == 200
        assert sql('CREATE TABLE forbidden_after_restore(id integer)', app=True, check=False).returncode != 0
        report.update({'tablesRestored': len(before), 'fingerprints': before, 'backupObject': key,
                       'anonymousBackupStatus': 403, 'readyAfterRestore': True, 'applicationDDLDenied': True})
        print(f'private backup, empty schema and restore: passed; {len(before)} tables match; readyz=200', flush=True)
        run(['node', 'tooling/scripts/tasks/server.mjs', 'alerts-test'])
        if args.browser_check:
            wait_for_browser(report)
    finally:
        cleanup = compose('down', '--volumes', '--remove-orphans', check=False)
        containers = run(['docker', 'ps', '-aq', '--filter', f'label=com.docker.compose.project={PROJECT}'])
        volumes = run(['docker', 'volume', 'ls', '-q', '--filter', f'label=com.docker.compose.project={PROJECT}'])
        networks = run(['docker', 'network', 'ls', '-q', '--filter', f'label=com.docker.compose.project={PROJECT}'])
        report['verificationResourcesRemoved'] = cleanup.returncode == 0 and not containers.stdout.strip() and not volumes.stdout.strip() and not networks.stdout.strip()
        path = ROOT / 'apps/server/tmp/review/production_verification.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(report, ensure_ascii=False, indent=2))
        if not report['verificationResourcesRemoved']:
            raise RuntimeError('verification project cleanup incomplete')
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f'production verification failed: {error}', flush=True)
        raise SystemExit(1)
