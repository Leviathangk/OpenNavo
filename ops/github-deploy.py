#!/usr/bin/env python3
"""Restricted SSH receiver for GitHub builds; configuration and secrets stay on the host.

Install with a root-owned /etc/opennavo/github-deploy.json. This receiver is
intended for the existing versioned Compose/Caddy deployment, not first install.
"""
import base64
import concurrent.futures
import contextlib
import copy
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

# DNS targets expose IP scrape URLs; the discovered DNS name identifies the release.
# Require every configured replica of each job to be healthy, not just one matching endpoint.
MONITORING_HEALTH_SCRIPT = """const base = 'http://prometheus:9090/api/v1/targets';
const expected = JSON.parse(process.argv[1]);
const webCount = Number(process.argv[2] || 1);
for(let i=0;i<30;i++) {
  try {
    const d=await fetch(base,{signal:AbortSignal.timeout(3000)}).then(r=>r.json());
    if(d.status==='success' && Object.entries(expected).every(([job,address])=>{
      const targets=d.data.activeTargets.filter(t=>(t.discoveredLabels?.job ?? t.labels.job)===job);
      const wanted=new URL('http://'+address+'/metrics');
      const required = job === 'web' ? webCount : 1;
      return targets.length >= required && targets.every(t=>{
        const actual=new URL(t.scrapeUrl);
        const dnsName=t.discoveredLabels?.__meta_dns_name;
        const identity=dnsName===undefined ? actual.host===wanted.host : dnsName===wanted.hostname;
        return identity && t.health==='up' && actual.protocol===wanted.protocol &&
          actual.port===wanted.port && actual.pathname===wanted.pathname && !actual.search;
      });
    })) process.exit(0);
  } catch {}
  await new Promise(r=>setTimeout(r,2000));
}
process.exit(1);"""

MAX_ARCHIVE = 40 * 1024 * 1024
RELEASE_NAME = re.compile(r'gh-[0-9a-f]{12}-[0-9]{1,20}-[0-9]{1,4}')
# Each release snapshot holds that deployment's pre-migration database dump.
RELEASE_RETENTION = 5
WEB_ASSET_RETENTION_SECONDS = 7 * 24 * 60 * 60
GIB = 1024 ** 3
DEFAULT_WEB_REPLICAS = 1
MIN_MEM_AVAILABLE_BASE = 1 * GIB
MIN_MEM_AVAILABLE_PER_EXTRA_REPLICA = 2 * GIB
DRAIN_GRACE_SECONDS = 300
DRAIN_POLL_TIMEOUT_SECONDS = 90
ROLLBACK_DRAIN_TIMEOUT_SECONDS = 30
WARM_HOST = 'opennavo.com'
WARM_ORIGIN = 'http://127.0.0.1:3000'
WARM_INTERNAL_ORIGIN = 'http://127.0.0.1:9091'
WARM_PATHS = [
    '/', '/zh', '/ja', '/es', '/ru', '/download', '/zh/download', '/ja/download',
    '/es/download', '/ru/download', '/apps', '/recent', '/ru/apps', '/es/recent',
    '/apps/airbuild', '/apps/agentide'
]
PUBLIC_WARM_PATH = re.compile(r'/[A-Za-z0-9/_@+.,~:-]*\Z')
PRIVATE_WARM_PREFIXES = (
    '/api', '/admin', '/agent-api', '/auth', '/oauth', '/login', '/reset', '/callback',
    '/.', '/_', '/assets', '/icons', '/screenshots', '/sitemap', '/robots'
)
WARM_SCRIPT = r'''const paths=JSON.parse(process.argv[1]);
const origin=process.argv[2];
const host=process.argv[3];
const readyOrigin=process.argv[4];
const secondPassDurations=[];
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
for(let pass=0;pass<2;pass++) {
  for(const path of paths) {
    const start=performance.now();
    try {
      const response=await fetch(origin+path,{
        redirect:'manual',signal:AbortSignal.timeout(15000),
        headers:{host,accept:'text/html','x-forwarded-proto':'https','user-agent':'OpenNavo-Warmup/1.0'}});
      await response.arrayBuffer();
      const elapsed=performance.now()-start;
      console.log(`warm pass=${pass+1} path=${path} status=${response.status} ms=${Math.round(elapsed)}`);
      if(response.status>=500) process.exit(1);
      if(pass===1) secondPassDurations.push(elapsed);
    } catch(error) {
      console.error(`warm failed pass=${pass+1} path=${path}: ${error?.name ?? 'request error'}`);
      process.exit(1);
    }
    await delay(500);
  }
}
secondPassDurations.sort((a,b)=>a-b);
const middle=Math.floor(secondPassDurations.length/2);
const median=secondPassDurations.length%2 ? secondPassDurations[middle] :
  (secondPassDurations[middle-1]+secondPassDurations[middle])/2;
if(median>3000) {
  console.error(`warm second-pass median exceeded 3000ms: ${Math.round(median)}ms`);
  process.exit(1);
}
for(let i=0;i<3;i++) {
  try {
    const response=await fetch(readyOrigin+'/readyz',{signal:AbortSignal.timeout(2500)});
    await response.arrayBuffer();
    console.log(`candidate ready check=${i+1} status=${response.status}`);
    if(response.status>=500) process.exit(1);
  } catch(error) {
    console.error(`candidate ready check failed: ${error?.name ?? 'request error'}`);
    process.exit(1);
  }
  await delay(1000);
}'''
DRAIN_SCRIPT = r'''const response=await fetch(process.argv[1]+'/metrics',{signal:AbortSignal.timeout(2500)});
if(!response.ok) process.exit(1);
const text=await response.text();
const match=text.match(/^opennavo_web_http_in_flight (\d+)$/m) ?? text.match(/^opennavo_web_in_flight (\d+)$/m);
if(!match) process.exit(1);
console.log(match[1]);'''


def configured_web_replicas(config):
    value = config.get('web_replicas', DEFAULT_WEB_REPLICAS)
    if isinstance(value, bool):
        raise ValueError('web_replicas must be a positive integer')
    if isinstance(value, int):
        replicas = value
    elif isinstance(value, str) and re.fullmatch(r'[1-9][0-9]*', value):
        replicas = int(value)
    else:
        raise ValueError('web_replicas must be a positive integer')
    if replicas < 1 or replicas > 8:
        raise ValueError('web_replicas must be between 1 and 8')
    return replicas


def required_mem_available(config, replicas=None):
    replicas = configured_web_replicas(config) if replicas is None else replicas
    base = int(config.get('web_mem_available_base_mib', MIN_MEM_AVAILABLE_BASE // (1024 ** 2))) * 1024 ** 2
    extra = int(config.get('web_mem_available_per_extra_replica_mib', MIN_MEM_AVAILABLE_PER_EXTRA_REPLICA // (1024 ** 2))) * 1024 ** 2
    if base < 0 or extra < 0:
        raise ValueError('web memory capacity thresholds must be non-negative')
    return base + max(0, replicas - 1) * extra


def mem_available_bytes(text):
    match = re.search(r'^MemAvailable:\s*(\d+)\s*kB\s*$', text, re.M)
    if not match:
        raise RuntimeError('capacity gate: MemAvailable is unavailable')
    return int(match.group(1)) * 1024


def public_warm_path(path):
    if not isinstance(path, str) or len(path) > 300 or not PUBLIC_WARM_PATH.fullmatch(path):
        return False
    # Reject query strings, fragments, encoded separators and duplicate path separators.
    if '//' in path or '..' in path or '%' in path:
        return False
    stripped = re.sub(r'^/(?:zh|zh-hant|ja|es|ru|de|fr|it|pt)(?=/|$)', '', path) or '/'
    if stripped.startswith(PRIVATE_WARM_PREFIXES):
        return False
    return not re.search(r'\.(?:ico|png|svg|webp|jpe?g|js|css|xml|txt|json|map|woff2?|ttf)$', stripped, re.I)


def warm_paths(payload):
    extra = payload.get('warm_paths', [])
    if extra is None:
        extra = []
    if not isinstance(extra, list) or len(extra) > 66:
        raise ValueError('invalid public warm paths')
    if not all(public_warm_path(path) for path in extra):
        raise ValueError('invalid public warm path')
    return list(dict.fromkeys([*WARM_PATHS, *extra]))[:66]


def web_asset_paths(directory):
    return sorted(path.relative_to(directory).as_posix()
                  for path in directory.rglob('*') if path.is_file())


def previous_web_asset_paths(destination):
    """Read the previous release's current asset list, with a legacy fallback.

    Releases created before the manifest was added mark current assets with the
    newest mtime. That lets the first deployment after this change preserve the
    live release while leaving older retained assets on their original deadline.
    """
    manifest = destination.parent / 'manifest.json'
    try:
        data = json.loads(manifest.read_text())
        assets = data.get('web_assets') if isinstance(data, dict) else None
    except (OSError, TypeError, ValueError):
        assets = None
    if isinstance(assets, list):
        return {asset for asset in assets
                if isinstance(asset, str) and asset and not PurePosixPath(asset).is_absolute()
                and '..' not in PurePosixPath(asset).parts
                and PurePosixPath(asset).as_posix() == asset}
    files = list(destination.rglob('*'))
    files = [path for path in files if path.is_file()]
    if not files:
        return set()
    newest = max(path.stat().st_mtime for path in files)
    return {path.relative_to(destination).as_posix()
            for path in files if path.stat().st_mtime == newest}


def merge_web_assets(current, destination, now=None, previous_current=None):
    """Keep old fingerprinted assets for seven days and mark current assets as fresh."""
    timestamp = time.time() if now is None else now
    destination.mkdir(parents=True, exist_ok=True)
    current_paths = {source.relative_to(current).as_posix()
                     for source in current.rglob('*') if source.is_file()}
    if previous_current is None:
        previous_current = previous_web_asset_paths(destination)
    # Only the assets that were current in the release being replaced receive a
    # new retention deadline. Older retained assets keep their original mtime.
    for relative in set(previous_current) - current_paths:
        target = destination / relative
        if target.is_file():
            os.utime(target, (timestamp, timestamp))
    for source in current.rglob('*'):
        if not source.is_file():
            continue
        target = destination / source.relative_to(current)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        os.utime(target, (timestamp, timestamp))
    cutoff = timestamp - WEB_ASSET_RETENTION_SECONDS
    for path in destination.rglob('*'):
        if path.is_file() and path.stat().st_mtime < cutoff:
            path.unlink()


def validate(payload, registry):
    if not re.fullmatch(r'[0-9a-f]{40}', payload.get('sha', '')):
        raise ValueError('invalid source revision')
    if not re.fullmatch(r'[0-9]{1,20}', payload.get('run_id', '')):
        raise ValueError('invalid workflow run')
    if not re.fullmatch(r'[0-9]{1,4}', payload.get('run_attempt', '1')):
        raise ValueError('invalid workflow attempt')
    if set(payload.get('images', {})) != {'server', 'web', 'mcp'}:
        raise ValueError('all three application images are required')
    for kind, image in payload['images'].items():
        if not re.fullmatch(re.escape(f'{registry}/opennavo-{kind}@sha256:') + r'[0-9a-f]{64}', image):
            raise ValueError('images must use approved registry digests')
    archive = base64.b64decode(payload['admin_archive'], validate=True)
    if len(archive) > MAX_ARCHIVE or hashlib.sha256(archive).hexdigest() != payload.get('admin_sha256'):
        raise ValueError('invalid admin archive checksum or size')
    # Optional for compatibility with older senders and receivers; unknown fields are ignored by
    # the previous receiver, while this receiver validates the new field before touching the host.
    warm_paths(payload)
    return archive


def extract_admin(data, destination):
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        members = archive.getmembers()
        if sum(m.size for m in members) > 160 * 1024 * 1024:
            raise ValueError('admin archive expands beyond the limit')
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir()):
                raise ValueError('unsafe admin archive member')
        # Extract only validated regular files/directories; supports older host Python
        # without applying archive ownership, modes, symlinks or device metadata.
        for member in members:
            target = destination.joinpath(*PurePosixPath(member.name).parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, target.open('wb') as output:
                    shutil.copyfileobj(source, output)
    if not (destination / 'index.html').is_file():
        raise ValueError('admin index is missing')


def command(args, output=None):
    result = subprocess.run(args, stdout=output or subprocess.PIPE, stderr=subprocess.PIPE, timeout=1200)
    if result.returncode:
        # Docker/Compose diagnostics can contain expanded credentials.
        raise RuntimeError(f'command failed: {args[0]} (exit {result.returncode}); inspect privately on host')
    return result.stdout.decode().strip() if output is None else ''


@contextlib.contextmanager
def phase(name):
    # Durations go to the public workflow log, so it shows where a deployment spends its time.
    start = time.monotonic()
    yield
    print(f'{name} took {time.monotonic() - start:.0f} s', flush=True)


def write_private(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def update_env(source, updates):
    lines = []
    remaining = dict(updates)
    for line in source.splitlines():
        key = line.split('=', 1)[0]
        if key in remaining:
            line = key + '=' + remaining.pop(key)
        lines.append(line)
    lines.extend(key + '=' + value for key, value in remaining.items())
    return '\n'.join(lines) + '\n'


def monitoring_targets(source, services):
    """Replace only simple API/web scrape jobs; preserve globals, rules and unrelated jobs.

    Refuse unfamiliar layouts rather than silently discard relabeling/auth settings.
    The host configuration remains operator-owned; no credentials enter release logs.
    """
    targets = {kind: name + ':' + ('9090' if kind == 'api' else '9091')
               for kind, name in services.items() if kind in ('api', 'web')}
    if set(targets) != {'api', 'web'} or any(not re.fullmatch(r'[A-Za-z0-9_-]+:[0-9]+', t) for t in targets.values()):
        raise ValueError('invalid monitoring services')
    start = re.search(r'^scrape_configs:\s*\n', source, re.M)
    if not start:
        raise ValueError('monitoring requires a block scrape_configs section')
    end = re.search(r'^[A-Za-z_][A-Za-z0-9_]*:', source[start.end():], re.M)
    finish = start.end() + end.start() if end else len(source)
    body = source[start.end():finish]
    jobs = list(re.finditer(r'^  - job_name: *[\'"]?([\w-]+)[\'"]? *\n', body, re.M))
    if not jobs or body[:jobs[0].start()].strip():
        raise ValueError('unsupported monitoring job layout')
    blocks, seen = [], set()
    for i, job in enumerate(jobs):
        block = body[job.start():jobs[i + 1].start() if i + 1 < len(jobs) else len(body)]
        name = job.group(1)
        if name in targets:
            if name in seen:
                raise ValueError('duplicate monitoring job')
            # Support the repository DNS layout without removing replica discovery,
            # honor_labels, refresh intervals or comments. Reject unfamiliar layouts.
            clean = re.sub(r'^ *#.*\n', '', block, flags=re.M)
            header = r"  - job_name: *['\"]?" + name + r"['\"]? *\n"
            static = header + r"    static_configs: *\n      - targets: *\[[^\n]*\] *\n\s*"
            dns = (header + r"(?:    honor_labels: (?:true|false) *\n)?"
                   r"    dns_sd_configs: *\n      - names: *\[[^\n]*\] *\n"
                   r"        type: A *\n        port: " + ('9090' if name == 'api' else '9091') +
                   r" *\n        refresh_interval: [0-9]+[smh] *\n\s*")
            if re.fullmatch(dns, clean):
                block = re.sub(r'(      - names: *)\[[^\n]*\]',
                               lambda m: m[1] + '[' + services[name] + ']', block)
            elif re.fullmatch(static, clean):
                if name == 'web':
                    # Web has one DNS A target per replica. A static target would silently scrape
                    # only one container after an operator scales the service.
                    block = (f"  - job_name: web\n"
                             f"    dns_sd_configs:\n"
                             f"      - names: [{services[name]}]\n"
                             f"        type: A\n"
                             f"        port: 9091\n"
                             f"        refresh_interval: 5s\n")
                else:
                    block = re.sub(r'(      - targets: *)\[[^\n]*\]',
                                   lambda m: m[1] + "['" + targets[name] + "']", block)
            else:
                raise ValueError('unsupported API/web monitoring job; review host config')
            seen.add(name)
        blocks.append(block)
    for name in targets.keys() - seen:
        if name == 'web':
            blocks.append(f"  - job_name: web\n    dns_sd_configs:\n      - names: [{services[name]}]\n"
                          "        type: A\n        port: 9091\n        refresh_interval: 5s\n")
        else:
            blocks.append(f"  - job_name: {name}\n    static_configs:\n      - targets: ['{targets[name]}']\n")
    return source[:start.end()] + ''.join(blocks) + source[finish:]


class Deployment:
    def __init__(self, config, payload):
        self.config, self.payload = config, payload
        self.root = Path(config['root'])
        self.state_path = self.root / 'deploy/github-state.json'
        self.state = json.loads(self.state_path.read_text())
        self.release = 'gh-' + payload['sha'][:12] + '-' + payload['run_id'] + '-' + payload.get('run_attempt', '1')
        self.directory = self.root / 'releases' / self.release
        self.live_caddy = self.root / config['caddy_file']
        self.rolling = self.root / 'deploy/rolling.compose.json'
        self.env = self.root / 'current-release.env'
        self.services = {kind: kind + '_' + self.release.replace('-', '_') for kind in ['api', 'web', 'mcp', 'admin']}
        self.aliases = {}
        self.current_web_assets = []
        self.web_replicas = configured_web_replicas(config)
        self.warm_paths = warm_paths(payload)

    def compose(self, *args, candidate=True):
        directory = self.directory if candidate else self.root
        return command(['docker', 'compose', '--project-directory', str(self.root), '-p', self.config['project'],
                        '--env-file', str(self.root / '.env'), '--env-file', str(directory / ('release.env' if candidate else 'current-release.env')),
                        '-f', str(self.root / self.config['compose_file']),
                        '-f', str(self.directory / 'rolling.compose.json' if candidate else self.rolling),
                        '--profile', 'ops', '--profile', 'automation', *args])

    def prepare(self, archive):
        self.directory.mkdir(mode=0o700)
        for source, name in [(self.live_caddy, 'before-caddy'), (self.rolling, 'before-rolling.json'), (self.env, 'before-release.env'), (self.state_path, 'before-state.json')]:
            shutil.copyfile(source, self.directory / name)
        overlay = json.loads(self.rolling.read_text())
        # Keep only the live release as the rollback candidate in the next overlay.
        live_services = set(self.state['services'].values())
        overlay['services'] = {name: service for name, service in overlay['services'].items()
                               if not re.match(r'^(api|web|mcp|admin)(?:_|$)', name) or name in live_services}
        routes = self.live_caddy.read_text()
        for kind, new in self.services.items():
            old = self.state['services'][kind]
            service = copy.deepcopy(overlay['services'][old])
            for network, options in service.get('networks', {}).items():
                if not options:
                    continue
                aliases = options.get('aliases', [])
                for index, alias in enumerate(aliases):
                    replacement = f'{kind}-{network}-{self.release}'
                    if kind == 'admin':
                        replacement = 'admin-static-' + self.release
                    self.aliases[alias] = replacement
                    aliases[index] = replacement
            if kind != 'admin':
                service['image'] = self.payload['images']['server' if kind == 'api' else kind]
            for mount in service.get('volumes', []):
                if isinstance(mount, dict) and '/releases/' in mount.get('source', ''):
                    mount['source'] = str(self.directory / Path(mount['source']).name)
            service.setdefault('labels', {})['org.opencontainers.image.revision'] = self.payload['sha']
            if kind == 'web':
                service.setdefault('deploy', {})['replicas'] = self.web_replicas
                service['stop_grace_period'] = '90s'
                # Check both the internal listener and a cheap Nitro route; readiness must not flap
                # when the API is briefly restarting during a rolling deployment.
                service['healthcheck'] = {
                    'test': ['CMD', 'node', '-e',
                             "const signal=AbortSignal.timeout(2500);Promise.all([fetch('http://127.0.0.1:9091/healthz',{signal}),fetch('http://127.0.0.1:3000/robots.txt',{signal})]).then(async ([health,robots])=>{await Promise.all([health.arrayBuffer(),robots.arrayBuffer()]);process.exit(health.status===200&&robots.status===200?0:1)}).catch(()=>process.exit(1))"],
                    'interval': '30s', 'timeout': '3s', 'retries': 3,
                    'start_period': '60s', 'start_interval': '2s'}
            overlay['services'][new] = service
        for kind, new in self.services.items():
            service = overlay['services'][new]
            for key in ['NUXT_API_BASE_INTERNAL', 'OPENNAVO_AGENT_API_BASE']:
                if key in service.get('environment', {}):
                    for old, replacement in self.aliases.items():
                        service['environment'][key] = service['environment'][key].replace(old, replacement)
        for old, replacement in self.aliases.items():
            routes = routes.replace(old, replacement)
        routes = re.sub(r'X-OpenNavo-Release "[^"]+"', f'X-OpenNavo-Release "{self.payload["sha"][:12]}"', routes)
        overlay['services'].setdefault('worker', {})['image'] = self.payload['images']['server']
        # The cache notifier must follow the new web service when the previous release retires.
        worker_env = overlay['services']['worker'].get('environment', {})
        if 'WEB_INTERNAL_URL' in worker_env:
            for old, replacement in {**self.aliases, self.state['services']['web']: self.services['web']}.items():
                worker_env['WEB_INTERNAL_URL'] = worker_env['WEB_INTERNAL_URL'].replace(old, replacement)
        # Disposable isolated OG storage. Also provision it on existing hosts whose base compose
        # predates this service; no ports/volumes and no changes to business Redis.
        overlay['services'].setdefault('image-cache', {
            'image': '${REDIS_IMAGE:-registry.hub.docker.com/library/redis:8}',
            'restart': 'unless-stopped',
            'command': ['redis-server', '--save', '', '--appendonly', 'no', '--maxmemory', '384mb', '--maxmemory-policy', 'allkeys-lru'],
            'deploy': {'resources': {'limits': {'memory': '512M'}}},
            'healthcheck': {'test': ['CMD', 'redis-cli', 'ping'], 'interval': '10s', 'timeout': '3s', 'retries': 3}})
        overlay['services'][self.services['web']].setdefault('environment', {})['NUXT_IMAGE_REDIS_URL'] = 'redis://image-cache:6379/0'
        # Usage stats read CF-IPCountry only from these peers. The new API copies the previous release's
        # service, so an overlay that predates the setting gets the base compose value; a host value, even
        # an empty one, is kept.
        overlay['services'][self.services['api']].setdefault('environment', {}).setdefault(
            'USAGE_COUNTRY_TRUSTED_PROXIES', '${CADDY_PROXY_IP:-172.30.80.2}')
        write_private(self.directory / 'rolling.compose.json', overlay)
        (self.directory / 'release.env').write_text(update_env(self.env.read_text(), {
            'SERVER_IMAGE': self.payload['images']['server'], 'WEB_IMAGE': self.payload['images']['web'],
            'MCP_IMAGE': self.payload['images']['mcp'], 'ADMIN_DIST': str(self.directory / 'admin')}))
        (self.directory / 'Caddyfile.next').write_text(routes)
        old_admin = overlay['services'][self.state['services']['admin']]
        old_web = overlay['services'][self.state['services']['web']]
        def mount_source(service, target):
            return Path(next(m['source'] for m in service['volumes'] if m['target'] == target))
        shutil.copyfile(mount_source(old_admin, '/etc/caddy/Caddyfile'), self.directory / 'Caddyfile.admin')
        extract_admin(archive, self.directory / 'admin')
        old_assets = mount_source(old_admin, '/srv/admin') / 'assets'
        if old_assets.exists():
            for source in old_assets.rglob('*'):
                if source.is_file():
                    target = self.directory / 'admin/assets' / source.relative_to(old_assets)
                    if not target.exists():
                        target.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copyfile(source, target)
        old_web_assets = mount_source(old_web, '/app/.output/public/_nuxt')
        shutil.copytree(old_web_assets, self.directory / 'web-assets')
        container = command(['docker', 'create', self.payload['images']['web']])
        try:
            with tempfile.TemporaryDirectory(dir=self.directory) as incoming:
                command(['docker', 'cp', container + ':/app/.output/public/_nuxt/.', incoming])
                self.current_web_assets = web_asset_paths(Path(incoming))
                merge_web_assets(Path(incoming), self.directory / 'web-assets',
                                 previous_current=previous_web_asset_paths(old_web_assets))
        finally:
            command(['docker', 'rm', container])
        # Release directories are private; bind-mounted public assets must be readable by container users.
        for public in [self.directory / 'admin', self.directory / 'web-assets']:
            for path in [public, *public.rglob('*')]:
                path.chmod(0o755 if path.is_dir() else 0o644)
        (self.directory / 'Caddyfile.admin').chmod(0o644)
        self.compose('config', '--quiet')

    def backup(self):
        dump = self.directory / 'before-migration.dump'
        args = ['docker', 'exec', self.config['postgres_container'], 'pg_dump', '-U', self.config['postgres_user'], '-d', self.config['postgres_database'], '-Fc']
        # zstd compresses several times faster than pg_dump's default gzip, and the
        # dump shares a two-core host with production traffic. Servers built
        # without zstd fall back to the default.
        try:
            with dump.open('wb') as output:
                command([*args, '--compress=zstd'], output=output)
        except RuntimeError:
            with dump.open('wb') as output:
                command(args, output=output)
        if dump.stat().st_size < 1024:
            raise RuntimeError('database backup is unexpectedly small')
        with dump.open('rb') as source:
            result = subprocess.run(['docker', 'exec', '-i', self.config['postgres_container'], 'pg_restore', '--list'], stdin=source, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=120)
        if result.returncode:
            raise RuntimeError('database backup archive validation failed')
        print('Database backup created and archive validated.', flush=True)

    def capacity_gate(self):
        """Reject an overlap that the host cannot afford before touching the release."""
        if not hasattr(self, 'config'):
            # Test doubles and older callers construct Deployment with object.__new__; the real
            # receiver always has a host config and therefore always executes the gate.
            return
        replicas = getattr(self, 'web_replicas', None) or configured_web_replicas(self.config)
        required = required_mem_available(self.config, replicas)
        try:
            available = mem_available_bytes(Path('/proc/meminfo').read_text())
        except OSError as error:
            raise RuntimeError('capacity gate: cannot read /proc/meminfo') from error
        if available < required:
            raise RuntimeError(
                f'capacity gate: web_replicas={replicas} requires at least '
                f'{required // (1024 ** 2)} MiB MemAvailable; host has {available // (1024 ** 2)} MiB'
            )
        print(
            f'Capacity gate passed: web_replicas={replicas}, '
            f'MemAvailable={available // (1024 ** 2)} MiB, '
            f'required={required // (1024 ** 2)} MiB.',
            flush=True,
        )

    def candidate_webs(self):
        replicas = getattr(self, 'web_replicas', None) or configured_web_replicas(self.config)
        ids = command([
            'docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + self.config['project'],
            '--filter', 'label=com.docker.compose.service=' + self.services['web']
        ]).splitlines()
        if len(ids) != replicas:
            raise RuntimeError(f'expected {replicas} candidate web replicas, found {len(ids)}')
        return ids

    def candidate_up_args(self):
        """Build the candidate command without changing the one-replica legacy invocation."""
        replicas = getattr(self, 'web_replicas', None) or configured_web_replicas(getattr(self, 'config', {}))
        args = ['up', '-d', '--no-deps', '--wait', '--wait-timeout', '240']
        if replicas > 1:
            args.extend(['--scale', f"{self.services['web']}={replicas}"])
        return [*args, *self.services.values()]

    def warm_candidates(self):
        """Warm each candidate independently before Caddy can see it."""
        for container in self.candidate_webs():
            health = command(['docker', 'inspect', '-f', '{{.State.Health.Status}}', container])
            if health != 'healthy':
                raise RuntimeError('candidate web is not healthy')
            command([
                'docker', 'exec', container, 'node', '--input-type=module', '-e', WARM_SCRIPT,
                json.dumps(self.warm_paths), WARM_ORIGIN, WARM_HOST, WARM_INTERNAL_ORIGIN
            ])
            print(f'Warmed candidate web replica {container[:12]}.', flush=True)
        print('All candidate web replicas warmed and ready.', flush=True)

    def drain_retired(self, containers, *, preserve_stream_grace=True, timeout=None):
        """Wait for the MCP stream grace period and prove retired web requests drained.

        The caller decides whether to retain or stop containers after a failed drain. The normal
        cleanup keeps the existing five-minute stream-close semantics; rollback uses only a short,
        bounded poll because candidates are no longer on the public route.
        """
        running = [container for container in containers if container.get('State', {}).get('Running')]
        if not running:
            return True
        if preserve_stream_grace and getattr(self, 'switch_at', None) is not None:
            deadline = self.switch_at + DRAIN_GRACE_SECONDS
            remaining = max(0, deadline - time.monotonic())
            print(f'Draining retired containers; preserving {DRAIN_GRACE_SECONDS}s MCP stream grace '
                  f'({remaining:.0f}s remaining).', flush=True)
            if remaining:
                time.sleep(remaining)
        elif preserve_stream_grace:
            print('Drain start time unavailable; release was already switched, so polling immediately.', flush=True)
        else:
            print('Draining rollback candidates with a bounded grace period.', flush=True)
        timeout = DRAIN_POLL_TIMEOUT_SECONDS if timeout is None else timeout
        webs = [
            container['Id'] for container in running
            if (container.get('Config', {}).get('Labels', {}).get('com.docker.compose.service', '')
                .startswith('web_'))
        ]
        if not webs:
            print('No retired web replicas require in-flight polling.', flush=True)
            return True
        poll_deadline = time.monotonic() + timeout
        streak = 0
        while time.monotonic() < poll_deadline:
            try:
                counts = [command(['docker', 'exec', container, 'node', '--input-type=module', '-e', DRAIN_SCRIPT,
                                   WARM_INTERNAL_ORIGIN])
                          for container in webs]
                if any(not re.fullmatch(r'\d+', count) for count in counts):
                    print('WARNING: retired web metrics returned an invalid sample; drain cannot be proved.', flush=True)
                    return False
                empty = all(count == '0' for count in counts)
            except Exception:
                print('WARNING: retired web metrics could not be read; drain cannot be proved.', flush=True)
                return False
            streak = streak + 1 if empty else 0
            if streak >= 3:
                print('Retired web replicas drained (three consecutive zero samples).', flush=True)
                return True
            time.sleep(1)
        print('WARNING: retired container drain timed out; applying the configured undrained policy.', flush=True)
        return False

    def update_monitoring(self, services):
        # Find the optional installed monitor by Compose labels, not a release-specific name.
        ids = command(['docker', 'ps', '-q', '--filter',
                       'label=com.docker.compose.project=' + self.config['project'],
                       '--filter', 'label=com.docker.compose.service=prometheus']).splitlines()
        if not ids:
            print('No running Prometheus service; monitoring update skipped.', flush=True)
            return
        if len(ids) != 1:
            raise RuntimeError('expected one Prometheus instance')
        monitor = json.loads(command(['docker', 'inspect', ids[0]]))[0]
        mount = next((m for m in monitor['Mounts'] if m['Destination'] == '/etc/prometheus/prometheus.yml'), None)
        if not mount or mount['Type'] != 'bind':
            raise RuntimeError('monitoring config must be a host bind mount')
        path = Path(mount['Source'])
        before = path.read_text()
        after = monitoring_targets(before, services)
        backup = self.directory / 'before-prometheus.yml'
        if self.directory.is_dir() and not backup.exists():
            backup.write_text(before)
            backup.chmod(0o600)
        try:
            # Preserve the inode of the existing read-only container bind mount.
            path.write_text(after)
            command(['docker', 'exec', ids[0], 'promtool', 'check', 'config', '/etc/prometheus/prometheus.yml'])
            command(['docker', 'kill', '--signal=HUP', ids[0]])
            # Verify ingestion, not just YAML syntax. The web image already contains Node.
            web = self.config['project'] + '-' + services['web'] + '-1'
            command(['docker', 'exec', web, 'node', '--input-type=module', '-e', MONITORING_HEALTH_SCRIPT,
                     json.dumps({k: services[k] + (':9090' if k == 'api' else ':9091') for k in ('api', 'web')}),
                     str(getattr(self, 'web_replicas', None) or configured_web_replicas(self.config))])
        except Exception:
            path.write_text(before)
            command(['docker', 'kill', '--signal=HUP', ids[0]])
            raise
        print('API and web monitoring targets updated and healthy.', flush=True)

    def reload(self):
        command(['docker', 'exec', self.config['caddy_container'], 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile'])
        command(['docker', 'kill', '--signal=USR1', self.config['caddy_container']])
        self.switch_at = time.monotonic()

    def verify(self, expected=None):
        expected = expected or self.payload['sha'][:12]
        last_error = None
        for _ in range(30):
            try:
                for check in self.config['checks']:
                    request = urllib.request.Request(self.config['local_origin'] + check['path'], headers={'Host': check['host']})
                    with urllib.request.urlopen(request, timeout=15) as response:
                        if response.status != 200 or response.headers.get('X-OpenNavo-Release') != expected:
                            raise RuntimeError('route is not serving the new release')
                        while response.read(65536):
                            pass
                return
            except Exception as error:
                last_error = type(error).__name__
                time.sleep(2)
        raise RuntimeError('post-deployment route checks failed: ' + str(last_error))

    def rollback(self):
        self.env.write_bytes((self.directory / 'before-release.env').read_bytes())
        self.rolling.write_bytes((self.directory / 'before-rolling.json').read_bytes())
        # Restore and verify the old services before changing the public route. This keeps a failed
        # candidate from being the only service available during rollback.
        self.compose('up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                     *self.state['services'].values(), 'worker', candidate=False)
        print('Previous release services are healthy; switching Caddy back.', flush=True)
        self.live_caddy.write_bytes((self.directory / 'before-caddy').read_bytes())
        self.reload()
        try:
            self.verify(expected=self.state['sha'][:12])
            self.update_monitoring(self.state['services'])
        except Exception:
            print('WARNING: rollback verification or monitoring failed; candidate containers retained.', flush=True)
            raise
        candidates = []
        for service in self.services.values():
            ids = command([
                'docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + self.config['project'],
                '--filter', 'label=com.docker.compose.service=' + service
            ]).splitlines()
            candidates.extend({'Id': container, 'State': {'Running': True},
                               'Config': {'Labels': {'com.docker.compose.service': service}}}
                              for container in ids)
        drained = self.drain_retired(candidates, preserve_stream_grace=False, timeout=ROLLBACK_DRAIN_TIMEOUT_SECONDS)
        if not drained and self.config.get('retain_undrained_containers') is True:
            print('WARNING: retain_undrained_containers=true; rollback candidates were kept.', flush=True)
        else:
            if not drained:
                print('WARNING: rollback drain failed; stopping candidates with a 90s grace period.', flush=True)
            self.compose('stop', '--timeout', '90', *self.services.values())
        print('Previous routes and worker restored; database was not rolled back.', flush=True)

    def cleanup(self, previous):
        """Stop retired application containers and retain only one rollback release."""
        current = set(self.services.values())
        retained = set(previous.get('services', {}).values())
        ids = command(['docker', 'ps', '-aq', '--filter',
                       'label=com.docker.compose.project=' + self.config['project']]).splitlines()
        if not ids:
            return
        containers = json.loads(command(['docker', 'inspect', *ids]))
        retired = []
        for container in containers:
            labels = container['Config'].get('Labels') or {}
            service = labels.get('com.docker.compose.service', '')
            if (labels.get('com.docker.compose.project') != self.config['project']
                    or not re.fullmatch(r'(api|web|mcp|admin)(?:_[A-Za-z0-9_]+)?', service)
                    or service in current):
                continue
            retired.append((container, service))
        running = [container['Id'] for container, _ in retired if container['State']['Running']]
        if running:
            drained = self.drain_retired([container for container, _ in retired if container['State']['Running']])
            if not drained and self.config.get('retain_undrained_containers') is True:
                print('WARNING: retain_undrained_containers=true; retired containers were kept.', flush=True)
                return
            if not drained:
                print('WARNING: drain failed; stopping retired containers with a 90s grace period.', flush=True)
            command(['docker', 'stop', '--timeout', '90', *running])
        obsolete = [container['Id'] for container, service in retired if service not in retained]
        if obsolete:
            # Never remove volumes, images, or release files during container cleanup.
            command(['docker', 'rm', *obsolete])
        print('Retired containers stopped; at most one previous release retained.', flush=True)

    def prune(self, previous):
        """Bound disk use after container cleanup; failures are reported, never fatal."""
        try:
            ids = command(['docker', 'ps', '-aq']).split()
            containers = json.loads(command(['docker', 'inspect', *ids])) if ids else []
            self.prune_releases(previous, containers)
            self.prune_images(containers)
        except Exception as error:
            print('Retention cleanup incomplete; the next deployment retries it: ' + str(error), flush=True)

    def prune_releases(self, previous, containers):
        """Keep the newest snapshots plus the live, previous and any mounted release."""
        keep = max(2, int(self.config.get('release_retention', RELEASE_RETENTION)))
        protected = {self.release, self.state.get('release'), previous.get('release')}
        mounted = [Path(mount['Source']) for container in containers
                   for mount in container.get('Mounts') or [] if mount.get('Source')]
        snapshots = [path for path in (self.root / 'releases').iterdir()
                     if RELEASE_NAME.fullmatch(path.name) and not path.is_symlink() and path.is_dir()]
        snapshots.sort(key=lambda path: path.stat().st_mtime, reverse=True)
        for path in snapshots[keep:]:
            if path.name in protected or any(source == path or path in source.parents for source in mounted):
                continue
            shutil.rmtree(path)
            print('Removed release snapshot ' + path.name, flush=True)

    def prune_images(self, containers):
        """Protect container and rollback images before selecting unused application images."""
        used = {container['Image'] for container in containers}
        # On a same-SHA retry, the payload's run ID may differ from the deployed release.
        release = self.state.get('release') if self.state.get('sha') == self.payload['sha'] else self.release
        snapshot = self.root / 'releases' / release
        references = command([
            'docker', 'compose', '--project-directory', str(self.root), '-p', self.config['project'],
            '--env-file', str(self.root / '.env'), '--env-file', str(snapshot / 'before-release.env'),
            '-f', str(self.root / self.config['compose_file']), '-f', str(snapshot / 'before-rolling.json'),
            '--profile', 'ops', '--profile', 'automation', 'config', '--images']).splitlines()
        # Resolve every reference first; if configuration or image lookup fails, delete nothing.
        for reference in dict.fromkeys(references):
            used.add(command(['docker', 'image', 'inspect', '--format', '{{.Id}}', reference]))
        for kind in ['server', 'web', 'mcp']:
            listed = command(['docker', 'image', 'ls', '--no-trunc', '--quiet', self.config['registry'] + '/opennavo-' + kind])
            for image in dict.fromkeys(listed.split()):
                if image in used:
                    continue
                short = image.split(':')[-1][:12]
                try:
                    command(['docker', 'image', 'rm', image])
                    print(f'Removed unused {kind} image {short}', flush=True)
                except RuntimeError:
                    print(f'Kept {kind} image {short}: Docker refused removal', flush=True)

    def apply(self, archive):
        if self.state.get('sha') == self.payload['sha']:
            self.services = self.state['services']
            self.verify()
            self.update_monitoring(self.services)
            self.cleanup(self.state.get('previous', {}))
            self.prune(self.state.get('previous', {}))
            print('This source revision is already deployed.'); return
        # This read-only gate runs before image pulls, release directories, migrations or Caddy
        # changes. A second web replica therefore fails cleanly when the host cannot overlap it.
        self.capacity_gate()
        with tempfile.TemporaryDirectory() as auth:
            token = self.payload.get('registry_token', '')
            if token:
                result = subprocess.run(['docker', '--config', auth, 'login', 'ghcr.io', '--username', self.payload['registry_username'], '--password-stdin'], input=token.encode(), capture_output=True, timeout=60)
                if result.returncode:
                    raise RuntimeError('registry authentication failed')
            def pull(image):
                command(['docker', '--config', auth, 'pull', image])
                labels = json.loads(command(['docker', 'image', 'inspect', '--format', '{{json .Config.Labels}}', image]))
                if labels.get('org.opencontainers.image.revision') != self.payload['sha']:
                    raise RuntimeError('image source revision does not match the deployment')
            with phase('Pulling images'), concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                for pulled in [pool.submit(pull, image) for image in self.payload['images'].values()]:
                    pulled.result()
        with phase('Preparing the release'):
            self.prepare(archive)
        with phase('Database backup'):
            self.backup()
        print('Applying migrations and starting isolated candidates.', flush=True)
        with phase('Migrations'):
            self.compose('run', '--rm', '--no-deps', 'migrate', 'up')
            self.compose('run', '--rm', '--no-deps', 'db-permissions')
        try:
            with phase('Starting candidates'):
                self.compose('up', '-d', '--no-deps', '--wait', '--wait-timeout', '120', 'image-cache')
                self.compose(*self.candidate_up_args())
            self.warm_candidates()
        except Exception:
            # Caddy still points at the old release, so no rollback is needed. Keep the stop bounded.
            try:
                self.compose('stop', '--timeout', '90', *self.services.values())
            finally:
                raise
        print('All candidates healthy and warmed; switching routes.', flush=True)
        try:
            with phase('Switching routes and worker'):
                # Preserve the inode because Caddy bind-mounts this file.
                self.live_caddy.write_bytes((self.directory / 'Caddyfile.next').read_bytes())
                self.reload()
                self.verify()
                self.env.write_bytes((self.directory / 'release.env').read_bytes())
                self.rolling.write_bytes((self.directory / 'rolling.compose.json').read_bytes())
                self.compose('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker', candidate=False)
                self.verify()
                self.update_monitoring(self.services)
        except Exception:
            self.rollback()
            raise
        previous = {key: value for key, value in self.state.items() if key != 'previous'}
        write_private(self.state_path, {'sha': self.payload['sha'], 'release': self.release, 'services': self.services, 'previous': previous})
        manifest = {key: value for key, value in self.payload.items() if key not in ['admin_archive', 'registry_token']}
        manifest['web_assets'] = getattr(self, 'current_web_assets', [])
        write_private(self.directory / 'manifest.json', manifest)
        print('Production deployed and verified: ' + self.payload['sha'], flush=True)
        # Cleanup failure must not roll back a verified release; a retry resumes cleanup.
        with phase('Cleanup'):
            self.cleanup(previous)
            self.prune(previous)


def main():
    os.umask(0o077)
    config = json.loads(Path('/etc/opennavo/github-deploy.json').read_text())
    with (Path(config['root']) / 'deploy/github-deploy.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        raw = sys.stdin.buffer.read(MAX_ARCHIVE * 2 + 1)
        if len(raw) > MAX_ARCHIVE * 2:
            raise ValueError('deployment payload exceeds the limit')
        payload = json.loads(raw)
        if payload.get('action') == 'publish-desktop':
            version = payload.get('version', '')
            if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?', version):
                raise ValueError('invalid desktop version')
            state = json.loads((Path(config['root']) / 'deploy/github-state.json').read_text())
            container = config['project'] + '-' + state['services']['api'] + '-1'
            print(command(['docker', 'exec', container, '/desktop-publish', '--version', version, '--apply']))
            return
        archive = validate(payload, config['registry'])
        Deployment(config, payload).apply(archive)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # Never echo input payloads, environment or captured subprocess diagnostics.
        print('Deployment failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
