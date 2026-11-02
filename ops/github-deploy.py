#!/usr/bin/env python3
"""Restricted SSH receiver for GitHub builds; configuration and secrets stay on the host.

Install with a root-owned /etc/opennavo/github-deploy.json. This receiver is
intended for the existing versioned Compose/Caddy deployment, not first install.
"""
import base64
import concurrent.futures
import contextlib
import copy
import datetime
import fcntl
import hashlib
import io
import ipaddress
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
import urllib.error
import urllib.parse
import urllib.request
from email.utils import parsedate_to_datetime

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
# --probe's exit code for "another deployment holds the lock": busy, not broken.
PROBE_BUSY = 2
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
PUBLIC_MCP_BEGIN = '# BEGIN opennavo public-mcp'
PUBLIC_MCP_END = '# END opennavo public-mcp'
PUBLIC_MCP_PROXY_IP = '172.30.80.3'
PUBLIC_MCP_PROXY_IP_ALT = '172.30.80.4'
PUBLIC_MCP_PROXY_IP_TOKEN = '${PUBLIC_MCP_PROXY_IP:-' + PUBLIC_MCP_PROXY_IP + '}/32'
PUBLIC_MCP_PROXY_IP_ALT_TOKEN = '${PUBLIC_MCP_PROXY_IP_ALT:-' + PUBLIC_MCP_PROXY_IP_ALT + '}/32'
PUBLIC_MCP_PROXY_TOKENS = (PUBLIC_MCP_PROXY_IP_TOKEN, PUBLIC_MCP_PROXY_IP_ALT_TOKEN)
DEFAULT_TRUSTED_PROXIES = ('${CADDY_PROXY_IP:-172.30.80.2},' +
                           PUBLIC_MCP_PROXY_IP_TOKEN + ',' + PUBLIC_MCP_PROXY_IP_ALT_TOKEN +
                           ',${SSR_PROXY_SUBNET:-172.30.81.0/24}')
APPLICATION_SERVICES = ('api', 'web', 'mcp', 'public-mcp', 'admin')
APPLICATION_SERVICE_PATTERN = r'(?:api|web|mcp|public-mcp|admin)(?:_[A-Za-z0-9_]+)?'
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
WARM_READY_TIMEOUT_MS = 60000
WARM_OUTPUT_LIMIT = 4000
WARM_OUTPUT_PREFIXES = ('warm ', 'candidate ')
# The script prints only its own status lines (path, status, duration, error name); never headers,
# bodies, or environment. Pass 1 tolerates one retry per path for cold-start 5xx/request errors;
# pass 2 stays strict: any 5xx or request error fails the deployment.
WARM_SCRIPT = r'''const paths=JSON.parse(process.argv[1]);
const origin=process.argv[2];
const host=process.argv[3];
const readyOrigin=process.argv[4];
const readyTimeoutMs=Number(process.argv[5]||60000);
const secondPassDurations=[];
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const readyStart=Date.now();
for(let attempt=1;;attempt++) {
  let status='request error';
  try {
    const response=await fetch(readyOrigin+'/readyz',{signal:AbortSignal.timeout(2500)});
    await response.arrayBuffer();
    status=response.status;
    if(response.status===200) {
      console.log(`candidate ready wait attempts=${attempt} ms=${Date.now()-readyStart}`);
      break;
    }
  } catch(error) {
    status=error?.name ?? 'request error';
  }
  if(Date.now()-readyStart>=readyTimeoutMs) {
    console.error(`candidate ready wait timed out after ${attempt} attempts ms=${Date.now()-readyStart} last=${status}`);
    process.exit(1);
  }
  await delay(1000);
}
async function warm(pass,path) {
  const start=performance.now();
  try {
    const response=await fetch(origin+path,{
      redirect:'manual',signal:AbortSignal.timeout(15000),
      headers:{host,accept:'text/html','x-forwarded-proto':'https','user-agent':'OpenNavo-Warmup/1.0'}});
    await response.arrayBuffer();
    const elapsed=performance.now()-start;
    console.log(`warm pass=${pass} path=${path} status=${response.status} ms=${Math.round(elapsed)}`);
    return {ok:response.status<500,elapsed};
  } catch(error) {
    const elapsed=performance.now()-start;
    console.error(`warm failed pass=${pass} path=${path}: ${error?.name ?? 'request error'} ms=${Math.round(elapsed)}`);
    return {ok:false,elapsed};
  }
}
for(let pass=1;pass<=2;pass++) {
  for(const path of paths) {
    let result=await warm(pass,path);
    if(!result.ok && pass===1) {
      console.error(`warm retry pass=1 path=${path}`);
      await delay(2000);
      result=await warm(pass,path);
    }
    if(!result.ok) process.exit(1);
    if(pass===2) secondPassDurations.push(result.elapsed);
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

    Releases created before the manifest was added have no reliable way to
    distinguish current assets from retained assets. Treat every existing file
    as current once so the first deployment after this change gives the whole
    legacy release a fresh retention deadline.
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
    return {path.relative_to(destination).as_posix()
            for path in destination.rglob('*') if path.is_file()}


def release_manifest(payload, web_assets):
    manifest = {key: value for key, value in payload.items()
                if key not in ['admin_archive', 'registry_token']}
    manifest['web_assets'] = web_assets
    return manifest


def release_snapshot_sha(directory):
    """Source revision recorded in a release snapshot, or None when it has no readable manifest."""
    try:
        manifest = json.loads((Path(directory) / 'manifest.json').read_text())
    except (OSError, ValueError):
        return None
    sha = manifest.get('sha') if isinstance(manifest, dict) else None
    return sha if isinstance(sha, str) and sha else None


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


def warm_output(raw):
    """Return only the warmup script's own status lines, truncated, for the public deploy log.

    Anything else (Node or Docker diagnostics) is counted but not printed, so expanded
    environment values can never reach the workflow log.
    """
    text = raw.decode(errors='replace') if isinstance(raw, bytes) else (raw or '')
    lines = text.splitlines()
    kept = [line[:300] for line in lines if line.startswith(WARM_OUTPUT_PREFIXES)]
    hidden = len(lines) - len(kept)
    body = '\n'.join(kept)
    if len(body) > WARM_OUTPUT_LIMIT:
        body = '...(truncated)\n' + body[-WARM_OUTPUT_LIMIT:]
    if hidden:
        body += f'\n({hidden} other line(s) hidden; inspect privately on host)'
    return body or '(empty)'


VERIFY_USER_AGENT = 'OpenNavo-Deploy-Verify/1.0'
VERIFY_BODY_PREVIEW_BYTES = 200
VERIFY_ATTEMPTS = 30
# Each public-MCP verification sends three POSTs that count against the public per-client quota
# (60/min). Cap the attempts so retries cannot exhaust it and turn the real failure into a 429.
PUBLIC_MCP_VERIFY_ATTEMPTS = 5
PUBLIC_MCP_RETRY_AFTER_LIMIT = 30
# Public-MCP failures back off exponentially (2/4/8/16 s) so a ~10-20 s post-switch window does not
# exhaust the five attempts, while staying inside the 60/min per-client quota.
PUBLIC_MCP_BACKOFF_BASE = 2
PUBLIC_MCP_BACKOFF_MAX = 16
# The /mcp/x probe must prove the public-MCP handle answered it. A bare 404 also comes from the
# site catch-all, an upstream page or Cloudflare, so the route responds with this body marker and
# the probe requires it; anything else is "a 404 we cannot attribute" and fails.
PUBLIC_MCP_SUBPATH_MARKER = 'opennavo-public-mcp-subpath'
_TOKEN_PATTERNS = (
    re.compile(r'(?i)(bearer\s+)[^\s"\',;]+'),
    re.compile(r'(?i)((?:access_|refresh_|id_)?token|secret|password|passwd|api[_-]?key|authorization|cookie|'
               r'session(?:[_-]?id)?|signature|credential)(["\']?\s*[:=]\s*["\']?)[^\s"\',;&}]+'),
    # Short provider keys: the opaque-token rule below only catches 32+ characters.
    re.compile(r'\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{8,}'),
    re.compile(r'\b(?:gh[pousr]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,})'),
    re.compile(r'\b(?:AKIA|ASIA)[0-9A-Z]{12,}'),
    re.compile(r'\bxox[baprs]-[A-Za-z0-9-]{8,}'),
    re.compile(r'\bAIza[0-9A-Za-z_\-]{20,}'),
    re.compile(r'[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}'),  # JWT-like
    re.compile(r'[A-Za-z0-9+/_\-]{32,}={0,2}'),  # long opaque token / key material
)


def redact_preview(raw, limit=VERIFY_BODY_PREVIEW_BYTES):
    """First `limit` bytes of a response body as one printable line, with token-like values masked."""
    data = raw[:limit] if isinstance(raw, (bytes, bytearray)) else str(raw or '').encode()[:limit]
    text = data.decode('utf-8', 'replace')
    text = ''.join(ch if ch.isprintable() else ' ' for ch in text)
    text = _TOKEN_PATTERNS[0].sub(lambda match: match.group(1) + '[redacted]', text)
    text = _TOKEN_PATTERNS[1].sub(lambda match: match.group(1) + match.group(2) + '[redacted]', text)
    for pattern in _TOKEN_PATTERNS[2:]:
        text = pattern.sub('[redacted]', text)
    return text


def redact_url(url):
    """Drop userinfo, query and fragment: only scheme, host, port and path are logged."""
    try:
        parts = urllib.parse.urlsplit(url)
        host = parts.hostname or ''
        if parts.port:
            host += f':{parts.port}'
        return urllib.parse.urlunsplit((parts.scheme, host, parts.path, '', ''))
    except ValueError:
        return '(invalid url)'


class ConfigurationError(RuntimeError):
    """A host configuration problem whose fixed message is safe to print in the public deploy log.

    Only messages built here from constant text belong in this class; never put a credential, a
    response body or a raw exception message into it.
    """


class RouteCheckError(RuntimeError):
    """A post-deployment request failed; the message is safe for the public deploy log."""

    def __init__(self, method, url, host, status, body=b'', *, retry_after=None, location=None,
                 public_mcp=False, expected=None):
        self.method, self.url, self.host, self.status = method, redact_url(url), host, status
        self.preview = redact_preview(body)
        self.retry_after = retry_after
        self.public_mcp = public_mcp
        detail = f'{method} {self.url} (Host {host}) -> {status}'
        if expected:
            detail += f', expected {expected}'
        if location:
            detail += f' Location {redact_url(location)}'
        detail += f'; body[:{VERIFY_BODY_PREVIEW_BYTES}]={self.preview!r}'
        super().__init__(detail)


def _retry_after_seconds(headers, now=None):
    """Retry-After as delta-seconds or HTTP-date, capped so one header cannot stall a deploy."""
    value = str((headers or {}).get('Retry-After', '')).strip()
    if not value:
        return None
    try:
        seconds = int(value)
    except ValueError:
        try:
            when = parsedate_to_datetime(value)
        except (TypeError, ValueError):
            return None
        if when is None:
            return None
        if when.tzinfo is None:
            when = when.replace(tzinfo=datetime.timezone.utc)
        reference = now or datetime.datetime.now(datetime.timezone.utc)
        seconds = max(0, (when - reference).total_seconds())
    return max(0, min(int(seconds), PUBLIC_MCP_RETRY_AFTER_LIMIT))


def route_check_error(error, method, url, host, *, public_mcp=False):
    """Convert an urllib failure into a RouteCheckError with status and a redacted body preview."""
    if isinstance(error, RouteCheckError):
        return error
    if isinstance(error, urllib.error.HTTPError):
        try:
            body = error.read(VERIFY_BODY_PREVIEW_BYTES) if error.fp is not None else b''
        except Exception:
            body = b''
        headers = error.headers or {}
        return RouteCheckError(method, url, host, error.code, body, retry_after=_retry_after_seconds(headers),
                               location=headers.get('Location'), public_mcp=public_mcp)
    if isinstance(error, urllib.error.URLError):
        reason = getattr(error, 'reason', None)
        return RouteCheckError(method, url, host, f'request error ({type(reason).__name__ if reason else "URLError"})',
                               public_mcp=public_mcp)
    if isinstance(error, OSError):
        # A bare socket timeout or connection reset reaches these request loops as well; keep the
        # endpoint and the failure class instead of losing both in verify()'s type-name logging.
        return RouteCheckError(method, url, host, f'request error ({type(error).__name__})',
                               public_mcp=public_mcp)
    return None


def warm_retry_summary(stdout, stderr):
    """Summarize first-pass warmup retries from the script's own status lines (prefix-safe)."""
    lines = []
    for raw in (stdout, stderr):
        text = raw.decode(errors='replace') if isinstance(raw, bytes) else (raw or '')
        lines.extend(text.splitlines())
    attempts = {}
    retried = []
    passes = set()
    for line in lines:
        match = re.match(r'^warm (?:failed )?pass=(\d+) path=(\S+?)(?::)? (?:status=(\S+) )?.*?ms=(\d+)', line)
        if match:
            passes.add(match.group(1))
            if match.group(1) == '1':
                status = match.group(3) or line.split(': ', 1)[-1].rsplit(' ms=', 1)[0]
                attempts.setdefault(match.group(2), []).append((status, match.group(4)))
            continue
        match = re.match(r'^warm retry pass=1 path=(\S+)', line)
        if match:
            retried.append(match.group(1))
    out = []
    for path in retried:
        tries = attempts.get(path, [])
        # stdout and stderr are captured separately, so order the attempts by outcome: on a
        # passing warmup the failed attempt came first and the passing one was the retry.
        failed = [item for item in tries if not (item[0].isdigit() and int(item[0]) < 500)]
        passed = [item for item in tries if item not in failed]
        ordered = failed[:1] + passed[:1] if failed else tries
        first = ordered[0] if ordered else ('unknown', '?')
        second = ordered[1] if len(ordered) > 1 else ('unknown', '?')
        out.append(f'warm retry path={path} first={first[0]} first_ms={first[1]} '
                   f'retry={second[0]} retry_ms={second[1]}'[:300])
    paths = len(attempts)
    if retried:
        out.append(f'warm summary: {len(retried)} first-pass retr{"y" if len(retried) == 1 else "ies"} '
                   f'across {paths} path(s)')
    else:
        out.append(f'warm summary: no retries ({paths} path(s), {len(passes) or 0} pass(es))')
    return out


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
    """Atomically replace a private JSON file (state, degraded marker); a failed write keeps the old file."""
    write_atomic(path, (json.dumps(value, indent=2) + '\n').encode(), mode=0o600)


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


def merge_public_mcp_trusted_proxy(value, proxy_ips=None):
    """Keep the existing proxy list and add both reserved public-MCP peers once."""
    # No ownership ledger exists for old entries: even stale /32s may be operator-owned.
    # Preserve them rather than infer ownership from their address or position.
    proxy_ips = tuple(proxy_ips or (PUBLIC_MCP_PROXY_IP, PUBLIC_MCP_PROXY_IP_ALT))
    if len(proxy_ips) != 2:
        raise ValueError('two public-MCP proxy addresses are required')
    tokens = (PUBLIC_MCP_PROXY_IP_TOKEN, PUBLIC_MCP_PROXY_IP_ALT_TOKEN) if proxy_ips == (PUBLIC_MCP_PROXY_IP, PUBLIC_MCP_PROXY_IP_ALT) else tuple(ip + '/32' for ip in proxy_ips)
    if value is None:
        return ('${CADDY_PROXY_IP:-172.30.80.2},' + ','.join(tokens) +
                ',${SSR_PROXY_SUBNET:-172.30.81.0/24}')
    if not isinstance(value, str):
        raise ValueError('TRUSTED_PROXIES must be a string')
    entries = []
    seen = set()
    for raw in value.split(','):
        entry = raw.strip()
        if entry and entry not in seen:
            entries.append(entry)
            seen.add(entry)
    variables = ('PUBLIC_MCP_PROXY_IP', 'PUBLIC_MCP_PROXY_IP_ALT')
    for index, (ip, token) in enumerate(zip(proxy_ips, tokens)):
        # Preserve an already configured literal or token. Older hosts used the literal .3
        # without /32; retaining it avoids rewriting operator configuration while the new
        # default always emits explicit host routes.
        variable = variables[index] if proxy_ips == (PUBLIC_MCP_PROXY_IP, PUBLIC_MCP_PROXY_IP_ALT) else None
        if not any(entry in {ip, ip + '/32', token} or
                   (variable and entry.startswith('${' + variable + ':-') and entry.endswith('}/32'))
                   for entry in entries):
            entries.append(token)
    return ','.join(entries)


def write_caddy_in_place(path, content):
    """Keep the inode of the single-file Caddy bind mount and durably write its contents.

    docker-compose.prod.yml bind-mounts ./caddy/Caddyfile as a single file, so a rename would
    swap the host inode and the container would keep reading the old one. The write is therefore
    in place, bracketed by an atomically written intent record (expected SHA-256) that lets the
    next run detect an interrupted write, and verified by reading the file back.
    """
    intent = caddy_intent_path(path)
    digest = hashlib.sha256(content).hexdigest()
    write_atomic(intent, (digest + '\n').encode())
    with path.open('wb') as target:
        target.write(content)
        target.flush()
        os.fsync(target.fileno())
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise OSError(f'{path} does not match the content just written; intent kept for recovery')
    intent.unlink(missing_ok=True)
    fsync_directory(path.parent)


def caddy_intent_path(path):
    return path.with_name('.' + path.name + '.intent')


def fsync_directory(directory):
    fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def write_atomic(path, content, mode=None):
    """Write a non-mounted sidecar file: same-directory temp, fsync, rename, fsync directory."""
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix='.' + path.name + '-', delete=False) as staged:
        try:
            if mode is not None:
                os.chmod(staged.name, mode)
            staged.write(content)
            staged.flush()
            os.fsync(staged.fileno())
        except BaseException:
            Path(staged.name).unlink(missing_ok=True)
            raise
    try:
        os.replace(staged.name, path)
    except BaseException:
        Path(staged.name).unlink(missing_ok=True)
        raise
    fsync_directory(path.parent)


STALE_TEMP_NAME = re.compile(r'^\..+?-[a-z0-9_]{8}$')
STALE_TEMP_AGE_SECONDS = 3600


def sweep_stale_temp_files(root, caddy_file=None, max_age=STALE_TEMP_AGE_SECONDS):
    """Remove temp files left by a killed write_atomic(), outside any in-progress write.

    The receiver holds its lock while this runs, so no other deployment is writing. Only
    `.<name>-<random>` files older than `max_age` are removed; current writes, release
    snapshots, intent records and anything outside the known directories are left alone.
    """
    root = Path(root)
    directories = {root, root / 'deploy'}
    if caddy_file:
        directories.add((root / caddy_file).parent)
    releases = root / 'releases'
    if releases.is_dir():
        directories.update(path for path in releases.iterdir() if path.is_dir())
    cutoff = time.time() - max_age
    removed = 0
    for directory in directories:
        try:
            entries = list(directory.iterdir())
        except OSError:
            continue
        for path in entries:
            if (path.is_symlink() or not path.is_file()
                    or not STALE_TEMP_NAME.match(path.name)):
                continue
            try:
                if path.stat().st_mtime > cutoff:
                    continue
                path.unlink()
                removed += 1
            except OSError:
                continue
    return removed


def interrupted_caddy_write(path):
    """True when an intent record exists and the live file does not hold its expected content."""
    intent = caddy_intent_path(path)
    if not intent.is_file():
        return False
    expected = intent.read_text().strip()
    if path.is_file() and hashlib.sha256(path.read_bytes()).hexdigest() == expected:
        intent.unlink(missing_ok=True)
        return False
    return True


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None  # surface the 3xx as HTTPError instead of following it


def open_without_redirects(request, timeout):
    return urllib.request.build_opener(_NoRedirect).open(request, timeout=timeout)


class CaddyValidatorUnavailable(RuntimeError):
    """caddy validate could not run, so the configuration's validity is unknown."""


def caddy_validate(container, config_path):
    """Return True/False for a valid/invalid Caddyfile; raise CaddyValidatorUnavailable otherwise.

    Only a clean exit 1 from caddy itself counts as "invalid". Timeouts, OS errors, other exit
    codes, or an exit 1 while the container cannot run caddy at all are "unknown".
    """
    try:
        command(['docker', 'exec', container, 'caddy', 'validate', '--config', config_path,
                 '--adapter', 'caddyfile'])
        return True
    except RuntimeError as error:
        text = str(error)
        if '(exit ' in text and '(exit 1)' not in text:
            raise CaddyValidatorUnavailable(f'caddy validate could not run: {text}') from error
    except (OSError, subprocess.SubprocessError) as error:
        raise CaddyValidatorUnavailable(f'caddy validate could not run: {type(error).__name__}') from error
    try:
        command(['docker', 'exec', container, 'caddy', 'version'])
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        raise CaddyValidatorUnavailable('caddy validate failed and caddy is not runnable in the container') from error
    return False


def public_mcp_route(alias):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+', alias):
        raise ValueError('invalid public-MCP proxy alias')
    return (f'{PUBLIC_MCP_BEGIN}\n'
            '\t@publicMcp path /mcp\n'
            '\thandle @publicMcp {\n'
            '\t\trequest_body {\n'
            '\t\t\tmax_size 2MB\n'
            '\t\t}\n'
            f'\t\treverse_proxy {alias}:8790 {{\n'
            '\t\t\tflush_interval -1\n'
            '\t\t}\n'
            '\t}\n'
            '\t@publicMcpOther path /mcp/*\n'
            '\thandle @publicMcpOther {\n'
            f'\t\trespond "{PUBLIC_MCP_SUBPATH_MARKER}" 404\n'
            '\t}\n'
            f'{PUBLIC_MCP_END}\n')


def _caddy_site_block(source, site_pattern):
    """Return the unique Caddy site block matching site_pattern, or fail closed."""
    starts = list(re.finditer(site_pattern, source, re.M))
    if len(starts) != 1:
        raise ValueError('WEB_DOMAIN Caddy site block is missing or ambiguous')
    start = starts[0]
    depth = 1
    position = start.end()
    for line in source[position:].splitlines(keepends=True):
        # {$VAR} is a Caddy placeholder, not a block delimiter.
        syntax = re.sub(r'\{\$[A-Za-z_][A-Za-z0-9_]*\}', '', line)
        depth += syntax.count('{') - syntax.count('}')
        position += len(line)
        if depth == 0:
            return start.start(), position
        if depth < 0:
            break
    raise ValueError('WEB_DOMAIN Caddy site block has unbalanced braces')


def install_public_mcp_route(source, alias):
    """Install the public MCP route in the WEB_DOMAIN block before its catch-all proxy."""
    block_start, block_end = _caddy_site_block(
        source, r'^[ \t]*(?:(?:https?://)?\{\$WEB_DOMAIN\})[ \t]*\{[ \t]*$')
    block = source[block_start:block_end]
    if block.count(PUBLIC_MCP_BEGIN) != block.count(PUBLIC_MCP_END):
        raise ValueError('public-MCP Caddy route markers are unbalanced')
    markers = list(re.finditer(
        re.escape(PUBLIC_MCP_BEGIN) + r'.*?' + re.escape(PUBLIC_MCP_END) + r'\n?',
        block, re.S))
    if len(markers) > 1:
        raise ValueError('public-MCP Caddy route is ambiguous')
    if markers:
        block_without_route = block[:markers[0].start()] + block[markers[0].end():]
    else:
        block_without_route = block
        # Older repository installations shipped these two routes without markers.
        spans = []
        for matcher, path in [('publicMcp', '/mcp'), ('publicMcpOther', '/mcp/*')]:
            matches = list(re.finditer(
                r'^[ \t]*@' + matcher + r'[ \t]+path[ \t]+' + re.escape(path) +
                r'[ \t]*\n[ \t]*handle[ \t]+@' + matcher + r'[ \t]*\{[ \t]*\n', block, re.M))
            if len(matches) > 1:
                raise ValueError('legacy public-MCP Caddy route is ambiguous')
            if not matches:
                continue
            match = matches[0]
            depth, end = 1, match.end()
            for line in block[end:].splitlines(keepends=True):
                syntax = re.sub(r'\{\$[^}]+\}', '', line)
                depth += syntax.count('{') - syntax.count('}')
                end += len(line)
                if depth == 0:
                    break
            if depth != 0:
                raise ValueError('legacy public-MCP Caddy route has unbalanced braces')
            spans.append((match.start(), end))
        if spans and len(spans) != 2:
            raise ValueError('legacy public-MCP Caddy route is incomplete')
        for start, end in sorted(spans, reverse=True):
            block_without_route = block_without_route[:start] + block_without_route[end:]
    proxies = []
    depth = 0
    position = 0
    for line in block_without_route.splitlines(keepends=True):
        syntax = re.sub(r'\{\$[A-Za-z_][A-Za-z0-9_]*\}', '', line)
        if depth == 1 and re.fullmatch(r'[ \t]*reverse_proxy[ \t]*\{[ \t]*(?:#.*)?(?:\r?\n)?', line):
            proxies.append(position)
        depth += syntax.count('{') - syntax.count('}')
        position += len(line)
    if len(proxies) != 1:
        raise ValueError('WEB_DOMAIN Caddy catch-all reverse_proxy is missing or ambiguous')
    route = public_mcp_route(alias)
    insertion = proxies[0]
    # Keep the route at the same indentation as the surrounding site directives.
    block_with_route = block_without_route[:insertion] + route + block_without_route[insertion:]
    return source[:block_start] + block_with_route + source[block_end:]


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


# Explicit forward-switch phases, recorded in deploy/github-state.json under "switch" before and
# after each step of apply(). rollback() decides every abort from this record, never from file
# contents.
SWITCH_NONE = 'not-switched'              # live Caddyfile, env, rolling and state are all previous
SWITCH_CADDY_WRITING = 'caddy-writing'    # candidate validated; in-place write of live started
SWITCH_CADDY_WRITTEN = 'caddy-written'    # live file is the candidate; USR1 not yet confirmed
SWITCH_CADDY_RELOADED = 'caddy-reloaded'  # USR1 sent to Caddy; routing not verified
SWITCH_CONFIG_PROMOTED = 'config-promoted'  # env and rolling overlay promoted to the candidate
SWITCH_ROLLING_BACK = 'rolling-back'      # rollback started; cleared only after previous is reloaded and verified
SWITCH_ROUTED_TO_CANDIDATE = (SWITCH_CADDY_WRITTEN, SWITCH_CADDY_RELOADED, SWITCH_CONFIG_PROMOTED)
SWITCH_PROCESS = {
    SWITCH_NONE: 'the Caddy process runs the previous release',
    SWITCH_CADDY_WRITING: ('the live Caddyfile write was interrupted (contents unknown; install recovery may '
                           'have restored the backup) and Caddy was never reloaded, so the process runs the '
                           'previous release'),
    SWITCH_CADDY_WRITTEN: ('the live Caddyfile file is the candidate but Caddy reload (USR1) was not confirmed, '
                           'so the Caddy process may still run the previous release'),
    SWITCH_CADDY_RELOADED: 'USR1 sent to Caddy; routing not verified',
    SWITCH_CONFIG_PROMOTED: 'USR1 sent to Caddy; routing not verified',
}


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
        self.services = {kind: kind + '_' + self.release.replace('-', '_') for kind in APPLICATION_SERVICES}
        self.aliases = {}
        self.current_web_assets = []
        self.web_replicas = configured_web_replicas(config)
        self.warm_paths = warm_paths(payload)

    @property
    def degraded_path(self):
        return self.root / 'deploy/github-degraded.json'

    def recovery_instructions(self):
        return ('Manual intervention required: verify Caddy routing, running services and release '
                f'configuration, then remove {self.degraded_path} to allow deployments.')

    def mark_degraded(self, reason):
        # Keep this outside release snapshots and the last verified state so retries cannot
        # bypass unresolved routing. Only a verified rollback in this run may clear it.
        write_private(self.degraded_path, {
            'release': self.release, 'sha': self.payload['sha'], 'reason': reason,
        })
        self.degraded_marker_written = True
        print(f'DEGRADED: {reason}. {self.recovery_instructions()}', flush=True)

    def compose(self, *args, candidate=True):
        directory = self.directory if candidate else self.root
        result = command(['docker', 'compose', '--project-directory', str(self.root), '-p', self.config['project'],
                          '--env-file', str(self.root / '.env'), '--env-file', str(directory / ('release.env' if candidate else 'current-release.env')),
                          '-f', str(self.root / self.config['compose_file']),
                          '-f', str(self.directory / 'rolling.compose.json' if candidate else self.rolling),
                          '--profile', 'ops', '--profile', 'automation', *args])
        if args and args[0] == 'up':
            services = self.services if candidate else self.state['services']
            public_mcp = services.get('public-mcp')
            if public_mcp and public_mcp in args[1:]:
                self.restore_public_mcp_proxy(public_mcp, candidate=candidate)
        return result

    def restore_public_mcp_proxy(self, service, *, candidate):
        """Restore an endpoint reclaimed by preflight before the caller can switch routes."""
        ids = self.compose('ps', '-q', service, candidate=candidate).splitlines()
        if not ids:
            raise RuntimeError('public-MCP proxy restore found no running service container')
        # Use Compose's resolved configuration so inherited addresses and DNS aliases survive
        # a reconnect, including hosts whose base Compose file supplies the fixed address.
        resolved = json.loads(self.compose('config', '--format', 'json', candidate=candidate))
        options = resolved['services'][service]['networks']['proxy'] or {}
        address = None if candidate else self.public_mcp_overlay_ip()
        address = address or options.get('ipv4_address')
        if address:
            address = str(ipaddress.ip_address(address))
        network = self.config.get('proxy_network') or self.config['project'] + '_proxy'
        containers = json.loads(command(['docker', 'inspect', *ids]))
        for container in containers:
            endpoint = (container.get('NetworkSettings') or {}).get('Networks', {}).get(network)
            if endpoint is not None:
                if address and endpoint.get('IPAddress') != address:
                    raise RuntimeError('public-MCP proxy restore found an unexpected fixed address')
                continue
            if not address:
                raise RuntimeError('public-MCP proxy restore requires a recorded fixed address')
            # Compose up can restart a retained container without restoring disconnected
            # networks. Reconnect only the missing endpoint; healthy peers stay untouched.
            connect = ['docker', 'network', 'connect', '--ip', address]
            for alias in dict.fromkeys([service, *(options.get('aliases') or [])]):
                connect.extend(['--alias', alias])
            command([*connect, network, container['Id']])

    def write_manifest(self, directory=None):
        target = self.directory if directory is None else Path(directory)
        write_private(target / 'manifest.json', release_manifest(
            self.payload, getattr(self, 'current_web_assets', [])))

    def public_mcp_proxy_ips(self):
        """Resolve the two reserved proxy addresses without asking Compose to recreate a network."""
        configured = getattr(self, 'config', {})
        values = configured.get('public_mcp_proxy_ips')
        if values is not None:
            if isinstance(values, str):
                values = [item.strip() for item in values.split(',')]
            if not isinstance(values, (list, tuple)) or len(values) != 2:
                raise RuntimeError('public-MCP proxy preflight requires exactly two proxy addresses')
            raw = list(values)
        else:
            env = {}
            try:
                for line in (self.root / '.env').read_text().splitlines():
                    if '=' in line and not line.lstrip().startswith('#'):
                        key, value = line.split('=', 1)
                        env[key.strip()] = value.strip()
            except OSError:
                pass
            raw = [configured.get('public_mcp_proxy_ip') or env.get('PUBLIC_MCP_PROXY_IP') or
                   os.environ.get('PUBLIC_MCP_PROXY_IP') or PUBLIC_MCP_PROXY_IP,
                   configured.get('public_mcp_proxy_ip_alt') or env.get('PUBLIC_MCP_PROXY_IP_ALT') or
                   os.environ.get('PUBLIC_MCP_PROXY_IP_ALT') or PUBLIC_MCP_PROXY_IP_ALT]
        try:
            addresses = tuple(str(ipaddress.ip_address(value)) for value in raw)
        except ValueError as error:
            raise RuntimeError('public-MCP proxy preflight found an invalid reserved address') from error
        if len(set(addresses)) != 2:
            raise RuntimeError('public-MCP proxy preflight requires two distinct reserved addresses')
        return addresses

    def public_mcp_overlay_ip(self, overlay=None):
        """Read the live public-MCP address from the previous overlay when available."""
        try:
            for key in ('public_mcp_proxy_ip', 'public_mcp_ip'):
                value = self.state.get(key)
                if value:
                    return str(ipaddress.ip_address(value))
            state_service = self.state.get('public-mcp')
            if isinstance(state_service, dict) and state_service.get('ipv4_address'):
                return str(ipaddress.ip_address(state_service['ipv4_address']))
            overlay = json.loads(self.rolling.read_text()) if overlay is None else overlay
            old = self.state.get('services', {}).get('public-mcp')
            options = overlay.get('services', {}).get(old, {}).get('networks', {}).get('proxy', {})
            value = options.get('ipv4_address') if isinstance(options, dict) else None
            return str(ipaddress.ip_address(value)) if value else None
        except (OSError, TypeError, ValueError, json.JSONDecodeError):
            return None

    @staticmethod
    def _container_is_public_mcp(container, network_entry, project):
        labels = (container.get('Config') or {}).get('Labels') or {}
        if labels:
            return (labels.get('com.docker.compose.project') == project and
                    ((labels.get('com.docker.compose.service') or '') == 'public-mcp' or
                     (labels.get('com.docker.compose.service') or '').startswith('public-mcp_')))
        return False

    def public_mcp_preflight(self):
        """Check reserved proxy addresses before any pull, migration, or candidate start."""
        if not hasattr(self, 'config'):
            return
        project = self.config.get('project')
        if not project:
            raise RuntimeError('public-MCP proxy preflight requires the Compose project name')
        addresses = self.public_mcp_proxy_ips()
        network = self.config.get('proxy_network') or project + '_proxy'
        try:
            inspected = json.loads(command(['docker', 'network', 'inspect', network]))
            network_info = inspected[0]
            entries = network_info.get('Containers') or {}
        except (RuntimeError, OSError, TypeError, ValueError, IndexError, KeyError) as error:
            raise RuntimeError(f'public-MCP proxy preflight could not inspect network {network}') from error

        occupied = {address: [] for address in addresses}
        for container_id, entry in entries.items():
            raw_ip = (entry or {}).get('IPv4Address', '').split('/', 1)[0]
            if raw_ip in occupied:
                occupied[raw_ip].append((container_id, entry or {}))
        details = {}
        ids = [container_id for holders in occupied.values() for container_id, _ in holders]
        if ids:
            try:
                inspected_containers = json.loads(command(['docker', 'inspect', *ids]))
                details = {item.get('Id'): item for item in inspected_containers if isinstance(item, dict)}
            except (RuntimeError, OSError, TypeError, ValueError):
                # Missing inspect labels cannot prove ownership; refuse the occupied address.
                details = {}
        public_holders = {}
        reclaim = []
        for address, holders in occupied.items():
            for container_id, entry in holders:
                container = details.get(container_id, {})
                if not self._container_is_public_mcp(container, entry, project):
                    name = (entry.get('Name') or container_id).lstrip('/')
                    raise RuntimeError(
                        f'public-MCP proxy preflight refused: reserved address {address} is occupied '
                        f'by non-public-mcp container {name}')
                state = container.get('State') or {}
                if state.get('Running') is False and not state.get('Restarting') and not state.get('Paused'):
                    reclaim.append((address, container_id, entry))
                else:
                    public_holders[address] = container_id

        # Check every holder before changing anything. Disconnect only stopped, label-proven
        # project services; retained containers remain available to Compose for rollback.
        for address, container_id, entry in reclaim:
            command(['docker', 'network', 'disconnect', network, container_id])
            occupied[address].remove((container_id, entry))

        free = [address for address in addresses if not occupied[address]]
        if not free:
            raise RuntimeError('public-MCP proxy preflight refused: both reserved addresses are occupied')
        old_ip = self.public_mcp_overlay_ip()
        if old_ip in addresses:
            choices = [address for address in addresses if address != old_ip and address in free]
        else:
            current = next((address for address in addresses if address in public_holders), None)
            if current:
                self.public_mcp_live_ip = current
            choices = [address for address in addresses if address != current and address in free]
        if not choices:
            raise RuntimeError('public-MCP proxy preflight refused: no alternate reserved address is free')
        self.public_mcp_proxy_ip = choices[0]
        print(f'Public MCP proxy preflight passed: candidate address {self.public_mcp_proxy_ip}.', flush=True)

    def ensure_live_manifest(self):
        release = self.state.get('release')
        if not release:
            return
        try:
            directory = self.root / 'releases' / release
            manifest = directory / 'manifest.json'
            try:
                data = json.loads(manifest.read_text())
                if isinstance(data, dict) and isinstance(data.get('web_assets'), list):
                    return
            except (OSError, TypeError, ValueError):
                pass
            assets = directory / 'web-assets'
            self.current_web_assets = web_asset_paths(assets) if assets.is_dir() else []
            self.write_manifest(directory)
        except OSError:
            print('Live release manifest repair skipped; continuing deployment.', flush=True)

    def prepare(self, archive):
        self.directory.mkdir(mode=0o700)
        for source, name in [(self.live_caddy, 'before-caddy'), (self.rolling, 'before-rolling.json'), (self.env, 'before-release.env'), (self.state_path, 'before-state.json')]:
            shutil.copyfile(source, self.directory / name)
        overlay = json.loads(self.rolling.read_text())
        # Keep only the live release as the rollback candidate in the next overlay.
        live_services = set(self.state['services'].values())
        # Capture a base public-MCP definition before filtering the previous overlay. Hosts
        # deployed before public-MCP was introduced have no state entry to clone.
        base_public_mcp = copy.deepcopy(overlay['services'].get('public-mcp'))
        proxy_ips = self.public_mcp_proxy_ips()
        old_public_mcp = self.state['services'].get('public-mcp')
        old_public_mcp_ip = self.public_mcp_overlay_ip(overlay) if old_public_mcp else None
        if old_public_mcp and not old_public_mcp_ip and getattr(self, 'public_mcp_live_ip', None):
            old_options = overlay['services'].setdefault(old_public_mcp, {}).setdefault('networks', {}).setdefault('proxy', {})
            old_options['ipv4_address'] = self.public_mcp_live_ip
            # Preserve the live address in the rollback snapshot as well, so a restart during
            # recovery cannot move the previous container to a dynamic address.
            before_overlay_path = self.directory / 'before-rolling.json'
            before_overlay = json.loads(before_overlay_path.read_text())
            before_options = before_overlay['services'].setdefault(old_public_mcp, {}).setdefault('networks', {}).setdefault('proxy', {})
            before_options['ipv4_address'] = self.public_mcp_live_ip
            write_private(before_overlay_path, before_overlay)
        candidate_public_mcp_ip = getattr(self, 'public_mcp_proxy_ip', None)
        if candidate_public_mcp_ip is None:
            candidate_public_mcp_ip = next((ip for ip in proxy_ips if ip != old_public_mcp_ip), proxy_ips[0])
        overlay['services'] = {name: service for name, service in overlay['services'].items()
                               if not re.match(r'^(?:api|web|mcp|public-mcp|admin)(?:_|$)', name) or name in live_services}
        routes = self.live_caddy.read_text()
        for kind, new in self.services.items():
            old = self.state['services'].get(kind)
            if old is None and kind == 'public-mcp':
                service = base_public_mcp or {
                    'image': '${MCP_IMAGE:?Build or pull MCP_IMAGE}',
                    'restart': 'unless-stopped',
                    'user': '10001:10001',
                    'read_only': True,
                    'tmpfs': ['/tmp:rw,noexec,nosuid,size=64m'],
                    'security_opt': ['no-new-privileges:true'],
                    'cap_drop': ['ALL'],
                    'entrypoint': ['opennavo-public-mcp'],
                    'environment': {
                        'OPENNAVO_PUBLIC_API_BASE': 'http://api-proxy:8080/api/v1',
                        'OPENNAVO_PUBLIC_MCP_HOST': '0.0.0.0',
                        'OPENNAVO_PUBLIC_MCP_PORT': '8790',
                        'OPENNAVO_PUBLIC_MCP_PATH': '/mcp',
                        'OPENNAVO_PUBLIC_MCP_TRUSTED_PROXIES': '["${CADDY_PROXY_IP:-172.30.80.2}/32"]',
                        'OPENNAVO_PUBLIC_MCP_RATE_LIMIT': '60',
                        'OPENNAVO_PUBLIC_MCP_RATE_WINDOW_SECONDS': '60',
                    },
                    'networks': {'proxy': {'aliases': ['public-mcp-proxy'],
                                           'ipv4_address': candidate_public_mcp_ip}},
                    'deploy': {'resources': {'limits': {'memory': '256M'}}},
                    'depends_on': {'api': {'condition': 'service_healthy'}},
                }
            else:
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
                image_kind = 'server' if kind == 'api' else 'mcp' if kind == 'public-mcp' else kind
                service['image'] = self.payload['images'][image_kind]
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
            elif kind == 'public-mcp':
                service.setdefault('entrypoint', ['opennavo-public-mcp'])
                proxy = service.setdefault('networks', {}).setdefault('proxy', {})
                proxy['ipv4_address'] = candidate_public_mcp_ip
                # Compose --wait uses this liveness endpoint to reject a bad candidate before
                # Caddy is switched. Keep it explicit for overlays from before public-MCP existed.
                service['healthcheck'] = {
                    'test': ['CMD', 'python', '-I', '-c',
                             "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8790/healthz', timeout=2).close()"],
                    'interval': '30s', 'timeout': '3s', 'retries': 3,
                    'start_period': '60s', 'start_interval': '2s'}
            overlay['services'][new] = service
        for kind, new in self.services.items():
            service = overlay['services'][new]
            for key in ['NUXT_API_BASE_INTERNAL', 'OPENNAVO_AGENT_API_BASE', 'OPENNAVO_PUBLIC_API_BASE']:
                if key in service.get('environment', {}):
                    for old, replacement in self.aliases.items():
                        service['environment'][key] = service['environment'][key].replace(old, replacement)
        # A public-MCP service created for the first time comes from the base Compose file, whose
        # API base names the unversioned api-proxy alias. Rolling releases only expose versioned
        # aliases, so point it at this release's API proxy alias like every other service.
        public_mcp = self.services.get('public-mcp')
        api_proxy_alias = next(iter(overlay['services'][self.services['api']].get('networks', {})
                                    .get('proxy', {}).get('aliases', []) or []), None)
        if public_mcp and api_proxy_alias:
            environment = overlay['services'][public_mcp].setdefault('environment', {})
            base = environment.get('OPENNAVO_PUBLIC_API_BASE')
            if isinstance(base, str):
                environment['OPENNAVO_PUBLIC_API_BASE'] = re.sub(
                    r'^(https?://)api-proxy(?=[:/]|$)', lambda match: match.group(1) + api_proxy_alias, base)
        for old, replacement in self.aliases.items():
            routes = routes.replace(old, replacement)
        routes = install_public_mcp_route(
            routes, self.aliases.get('public-mcp-proxy', 'public-mcp-proxy-' + self.release))
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
        api_environment = overlay['services'][self.services['api']].setdefault('environment', {})
        api_environment.setdefault('USAGE_COUNTRY_TRUSTED_PROXIES', '${CADDY_PROXY_IP:-172.30.80.2}')
        api_environment['TRUSTED_PROXIES'] = merge_public_mcp_trusted_proxy(
            api_environment.get('TRUSTED_PROXIES'), proxy_ips)
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
            result = subprocess.run([
                'docker', 'exec', container, 'node', '--input-type=module', '-e', WARM_SCRIPT,
                json.dumps(self.warm_paths), WARM_ORIGIN, WARM_HOST, WARM_INTERNAL_ORIGIN,
                str(WARM_READY_TIMEOUT_MS)
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1200)
            if result.returncode:
                print(f'Warmup failed for candidate web replica {container[:12]} (exit {result.returncode}).',
                      flush=True)
                print('warmup stdout:\n' + warm_output(result.stdout), flush=True)
                print('warmup stderr:\n' + warm_output(result.stderr), flush=True)
                raise RuntimeError(f'candidate warmup failed (exit {result.returncode})')
            # Retries that later passed are otherwise invisible; log them (or a no-retry summary)
            # through the same prefix filter as failures.
            print(warm_output('\n'.join(warm_retry_summary(result.stdout, result.stderr))), flush=True)
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

    def _validate_staged(self, content):
        """Validate content as a staged copy inside the Caddy container; never touches the live file."""
        container = self.config['caddy_container']
        with tempfile.NamedTemporaryFile(dir=self.live_caddy.parent, prefix='.Caddyfile-', delete=False) as staged:
            path = Path(staged.name)
            remote = '/tmp/' + path.name
            try:
                staged.write(content)
                staged.flush()
                os.fsync(staged.fileno())
                command(['docker', 'cp', str(path), container + ':' + remote])
                return caddy_validate(container, remote)
            finally:
                path.unlink(missing_ok=True)
                with contextlib.suppress(RuntimeError, OSError):
                    command(['docker', 'exec', container, 'rm', '-f', remote])

    def install_caddy(self, content, before_write=None):
        """Validate the candidate first, then back up the live file and update its inode.

        The live file is read once: the backup and the staged validation both use that snapshot,
        so a backup is never taken from a candidate that has not been validated yet.
        """
        live = self.live_caddy.read_bytes()
        if not self._validate_staged(content):
            raise RuntimeError('invalid staged Caddyfile; live configuration left untouched')
        backup = self.live_caddy.with_name('Caddyfile.bak')
        # Keep the pre-write live version even when an earlier backup exists: the release's own
        # before-caddy snapshot is only a fallback, this file is what recovery restores in place.
        write_atomic(backup, live)
        if before_write:
            before_write()
        try:
            write_caddy_in_place(self.live_caddy, content)
        except Exception:
            print(f'WARNING: writing {self.live_caddy} failed; checking whether it needs recovery.', flush=True)
            # The failed write leaves its intent record, so this restores the backup only when the
            # live file is incomplete/invalid and the backup validates (same rule everywhere).
            self.validate_live_caddy()
            raise

    def validate_live_caddy(self):
        """Recover an interrupted or invalid in-place write before trusting the live configuration.

        The backup is restored only when caddy definitively rejects the live file (or an intent
        record shows the last write never completed) and the backup itself validates. If the
        validator cannot run, nothing is touched and the deployment aborts.
        """
        container = self.config['caddy_container']
        backup = self.live_caddy.with_name('Caddyfile.bak')
        interrupted = interrupted_caddy_write(self.live_caddy)
        if not interrupted and caddy_validate(container, '/etc/caddy/Caddyfile'):
            return
        reason = 'an interrupted write' if interrupted else 'live Caddyfile validation failed'
        self._restore_live_caddy(backup, reason)

    def _restore_live_caddy(self, backup, reason):
        """The single recovery path: restore backup in place only if it validates as a staged copy."""
        container = self.config['caddy_container']
        if not backup.is_file():
            raise RuntimeError(f'Live Caddyfile validation failed for {self.live_caddy} ({reason}); '
                               f'backup {backup} is missing. The live file is left as found (interrupted or failing validation) and needs manual '
                               f'recovery. Aborting deployment.')
        if not self._validate_staged(backup.read_bytes()):
            raise RuntimeError(f'Live Caddyfile validation failed for {self.live_caddy} ({reason}) and '
                               f'backup {backup} is invalid too. The backup was not restored; the live file is left as found '
                               f'(interrupted or failing validation) and needs manual recovery. Aborting deployment.')
        print(f'WARNING: {reason}; restoring {self.live_caddy} in place from {backup}.', flush=True)
        write_caddy_in_place(self.live_caddy, backup.read_bytes())
        if not caddy_validate(container, '/etc/caddy/Caddyfile'):
            raise RuntimeError(f'Restored {self.live_caddy} from {backup} but it still fails validation.')
        print(f'Restored live Caddyfile from {backup}; validation passed.', flush=True)

    def reload(self):
        try:
            self.validate_live_caddy()
        except Exception:
            # validate_live_caddy already restored a good backup when that was provably safe;
            # never overwrite a file whose validity is unknown.
            self.mark_degraded('Caddy reload failed; routing unconfirmed')
            raise
        try:
            command(['docker', 'kill', '--signal=USR1', self.config['caddy_container']])
        except Exception:
            # The live file was just validated, so a failed signal is not a reason to overwrite it
            # with a backup. Leave every file as is and require manual confirmation.
            print(f'WARNING: Caddy reload signal failed; {self.live_caddy} validated and was left unchanged.', flush=True)
            self.mark_degraded('Caddy reload failed; routing unconfirmed')
            raise
        self.switch_at = time.monotonic()

    @staticmethod
    def _mcp_payload(body):
        try:
            return json.loads(body)
        except (TypeError, ValueError):
            # Streamable HTTP may return one or more SSE data events.
            events = []
            for line in body.decode('utf-8', 'replace').splitlines():
                if line.startswith('data:'):
                    try:
                        events.append(json.loads(line[5:].strip()))
                    except (TypeError, ValueError):
                        continue
            if not events:
                raise RuntimeError('public MCP returned an invalid response')
            return events[-1]

    def web_domain(self):
        """The site host WEB_DOMAIN from config or the host .env, or None."""
        configured = getattr(self, 'config', {}) or {}
        value = str(configured.get('web_domain') or '').strip()
        if value:
            return value
        root = getattr(self, 'root', None)
        if root is None:
            return None
        try:
            lines = (Path(root) / '.env').read_text().splitlines()
        except OSError:
            return None
        for line in lines:
            if line.lstrip().startswith('#') or '=' not in line:
                continue
            key, value = line.split('=', 1)
            if key.strip() == 'WEB_DOMAIN':
                value = value.strip().strip('"\'')
                if value:
                    return value
        return None

    def public_mcp_host(self):
        """Host for public-MCP verification; never inferred from a check (admin is not public)."""
        configured = getattr(self, 'config', {}) or {}
        host = str(configured.get('public_mcp_host') or '').strip()
        if host:
            return host
        host = self.web_domain()
        if host:
            return host
        raise ConfigurationError('public MCP verification requires public_mcp_host or WEB_DOMAIN in the host '
                                 'configuration or .env; refusing to infer it from the route checks')

    def verify_public_mcp(self):
        host = self.public_mcp_host()
        endpoint = self.config['local_origin'].rstrip('/') + '/mcp'
        session = None

        def post(request_id, method, call_params=None):
            nonlocal session
            params = ({
                'protocolVersion': '2025-06-18',
                'capabilities': {},
                'clientInfo': {'name': 'opennavo-deploy', 'version': '1'},
            } if method == 'initialize' else (call_params or {}))
            request = urllib.request.Request(
                endpoint,
                data=json.dumps({'jsonrpc': '2.0', 'id': request_id, 'method': method,
                                 'params': params}).encode(),
                method='POST',
                headers={
                    'Host': host,
                    'Accept': 'application/json, text/event-stream',
                    'Content-Type': 'application/json',
                    'MCP-Protocol-Version': '2025-06-18',
                    'User-Agent': VERIFY_USER_AGENT,
                    **({'Mcp-Session-Id': session} if session else {}),
                })
            # A redirect would turn the POST into a GET elsewhere (possibly via Cloudflare, which
            # rejects urllib clients); report it instead of following it.
            try:
                with open_without_redirects(request, timeout=15) as response:
                    status = getattr(response, 'status', None)
                    if status is None:
                        status = response.getcode()
                    body = response.read(2 * 1024 * 1024 + 1)
                    headers = getattr(response, 'headers', {})
            except Exception as error:
                converted = route_check_error(error, f'POST {method}', endpoint, host, public_mcp=True)
                if converted is None:
                    raise
                raise converted from None
            if status != 200:
                raise RouteCheckError(f'POST {method}', endpoint, host, status, body, public_mcp=True)
            for name in ('Mcp-Session-Id', 'MCP-Session-Id', 'mcp-session-id'):
                value = headers.get(name)
                if value:
                    session = value
                    break
            try:
                payload = self._mcp_payload(body)
            except RuntimeError:
                raise RouteCheckError(f'POST {method}', endpoint, host, f'{status} invalid MCP response', body,
                                      public_mcp=True) from None
            if not isinstance(payload, dict) or 'result' not in payload or 'error' in payload:
                raise RouteCheckError(f'POST {method}', endpoint, host, f'{status} JSON-RPC error', body,
                                      public_mcp=True)
            return payload['result']

        post(1, 'initialize')
        result = post(2, 'tools/list')
        if not isinstance(result, dict) or not isinstance(result.get('tools'), list) or not result['tools']:
            raise RuntimeError('public MCP tools/list returned no tools')
        # initialize/tools/list never reach the API; a real tool call proves API base and trust work.
        called = post(3, 'tools/call', {'name': 'list_categories', 'arguments': {}})
        content = called.get('content') if isinstance(called, dict) else None
        if (not isinstance(content, list) or not content or called.get('isError')
                or not any(isinstance(item, dict) and str(item.get('text') or '').strip() for item in content)):
            text = next((str(item.get('text') or '') for item in content or [] if isinstance(item, dict)), '')
            raise RouteCheckError('POST tools/call list_categories', endpoint, host,
                                  '200 tool error or no content', text.encode(), public_mcp=True)
        # Only the exact /mcp path is public; sub-paths must not reach the MCP service.
        # A redirect must not count: a 404 at the redirect target says nothing about /mcp/x.
        # The body marker proves the public-MCP handle produced this 404, not the site catch-all.
        probe = urllib.request.Request(endpoint + '/x', method='GET',
                                       headers={'Host': host, 'User-Agent': VERIFY_USER_AGENT})
        try:
            with open_without_redirects(probe, timeout=15) as response:
                status = getattr(response, 'status', None) or response.getcode()
                marker = response.read(256).decode('utf-8', 'replace')
        except urllib.error.HTTPError as error:
            status = error.code
            try:
                marker = error.read(256).decode('utf-8', 'replace')
            except Exception:
                marker = ''
        except Exception as error:
            # Caddy not ready yet, or the connection refused: keep the endpoint in the log instead
            # of letting verify() collapse the failure to a bare exception type name.
            converted = route_check_error(error, 'GET', endpoint + '/x', host, public_mcp=True)
            if converted is None:
                raise
            raise converted from None
        if status != 404:
            # Same contract as the marker check below: the public log keeps the endpoint, the
            # status and a redacted preview instead of a bare exception type name.
            raise RouteCheckError('GET', endpoint + '/x', host, status,
                                  marker.encode('utf-8', 'replace'),
                                  expected='404 from the public-MCP sub-path handle',
                                  public_mcp=True)
        # Older releases answer this probe with a bare `respond 404`. The marker is required only
        # when the live Caddyfile this run installed carries it; a rollback to such an older
        # release still validates its own route instead of failing on the newer contract.
        if self._live_caddy_requires_subpath_marker() and PUBLIC_MCP_SUBPATH_MARKER not in marker:
            # RouteCheckError keeps the endpoint, status and a redacted preview in the public log;
            # a bare RuntimeError would only ever print its type there.
            raise RouteCheckError(
                'GET', endpoint + '/x', host, status,
                marker.encode('utf-8', 'replace'),
                expected=f'body containing {PUBLIC_MCP_SUBPATH_MARKER!r} from the public-MCP handle',
                public_mcp=True)
        print(f'Public MCP route verified ({len(result["tools"])} tools).', flush=True)

    def _live_caddy_requires_subpath_marker(self):
        """True when the live Caddyfile answers /mcp/* with the marker body."""
        try:
            return PUBLIC_MCP_SUBPATH_MARKER in self.live_caddy.read_text()
        except (OSError, AttributeError):
            # Unit-test doubles may not have a live file; keep the stricter contract.
            return True

    def verify(self, expected=None):
        # A legacy deployment may have no recorded SHA; still check its routes without a header match.
        checking_current = expected is None
        expected = self.payload['sha'][:12] if expected is None else expected
        check_public_mcp = checking_current and 'public-mcp' in getattr(self, 'services', {})
        last_error = None
        logged = None
        public_mcp_attempts = 0
        for attempt in range(1, VERIFY_ATTEMPTS + 1):
            delay = 2
            attempted_public_mcp = False
            try:
                for check in self.config['checks']:
                    url = self.config['local_origin'] + check['path']
                    request = urllib.request.Request(
                        url, headers={'Host': check['host'], 'User-Agent': VERIFY_USER_AGENT})
                    try:
                        with urllib.request.urlopen(request, timeout=15) as response:
                            release = response.headers.get('X-OpenNavo-Release')
                            if response.status != 200 or (expected and release != expected):
                                body = response.read(VERIFY_BODY_PREVIEW_BYTES)
                                raise RouteCheckError(
                                    'GET', url, check['host'],
                                    f'{response.status} release={release or "(none)"} expected={expected or "(any)"}',
                                    body)
                            while response.read(65536):
                                pass
                    except Exception as error:
                        converted = route_check_error(error, 'GET', url, check['host'])
                        if converted is None:
                            raise
                        raise converted from None
                if check_public_mcp:
                    if public_mcp_attempts >= PUBLIC_MCP_VERIFY_ATTEMPTS:
                        break
                    public_mcp_attempts += 1
                    attempted_public_mcp = True
                    self.verify_public_mcp()
                return
            except Exception as error:
                if isinstance(error, ConfigurationError):
                    # A permanent host misconfiguration cannot be fixed by waiting, and its fixed
                    # message is safe for the public log: print it whole and stop instead of retrying.
                    print(f'Post-deployment check attempt {attempt} failed: {error}', flush=True)
                    raise RuntimeError(f'post-deployment route checks failed: {error}') from None
                last_error = str(error) if isinstance(error, RouteCheckError) else type(error).__name__
                if last_error != logged:
                    print(f'Post-deployment check attempt {attempt} failed: {last_error}', flush=True)
                    logged = last_error
                if isinstance(error, RouteCheckError) and error.retry_after is not None:
                    delay = max(delay, error.retry_after)
                elif attempted_public_mcp:
                    # Each public-MCP attempt sends three POSTs against a 60/min per-client quota;
                    # back off so a slow cold start cannot consume the whole budget in seconds.
                    backoff = min(PUBLIC_MCP_BACKOFF_BASE * 2 ** max(0, public_mcp_attempts - 1),
                                  PUBLIC_MCP_BACKOFF_MAX)
                    delay = max(delay, backoff)
                # Do not wait after the last attempt: nothing follows it but the failure.
                if attempt < VERIFY_ATTEMPTS:
                    time.sleep(delay)
        if check_public_mcp and public_mcp_attempts >= PUBLIC_MCP_VERIFY_ATTEMPTS:
            print(f'Public MCP verification stopped after {public_mcp_attempts} attempts '
                  f'to stay within the public rate limit.', flush=True)
        raise RuntimeError('post-deployment route checks failed: ' + str(last_error))

    def switch_phase(self):
        phase = getattr(self, '_switch_phase', None)
        if phase:
            return phase
        recorded = self.state.get('switch') if isinstance(getattr(self, 'state', None), dict) else None
        if isinstance(recorded, dict) and recorded.get('release') == self.release:
            if recorded.get('phase') == SWITCH_ROLLING_BACK:
                return recorded.get('forward_phase')
            return recorded.get('phase')
        return None

    def record_switch(self, phase):
        """Record the forward-switch phase in memory first, then durably in github-state.json."""
        self._switch_phase = phase
        try:
            write_private(self.state_path, {**self.state, 'switch': self._switch_record(phase)})
        except Exception:
            self.mark_degraded(f'could not record switch phase {phase} in {self.state_path.name}; '
                               f'{SWITCH_PROCESS.get(phase, "")}')
            raise

    def _switch_record(self, phase, **extra):
        return {'release': self.release, 'phase': phase, 'sha': self.payload['sha'],
                'services': self.services, **extra}

    def record_rollback(self):
        """Atomically record that rollback has begun, together with the previous state it restores.

        The record survives until the previous Caddyfile is reloaded and verified, so a run killed at
        any point of the switch back is recognised by reconcile_interrupted_switch().
        """
        previous = self._previous_state()
        self._switch_phase = self.switch_phase()  # keep the forward phase for abort decisions
        write_private(self.state_path, {**previous, 'switch': self._switch_record(
            SWITCH_ROLLING_BACK, forward_phase=self._switch_phase,
            previous_sha=previous.get('sha'), previous_services=previous.get('services'))})

    def reconcile_interrupted_switch(self):
        """Make live Caddy, env, rolling and state agree before a deployment trusts any of them.

        A recorded switch phase means an earlier run stopped mid-switch (aborted rollback, or the
        process was killed with no marker). caddy-writing: the live file may be the complete
        candidate, partial or previous, so restore the validated Caddyfile.bak and the previous
        env/rolling/state. Later phases: Caddy was given the candidate file, so adopt the candidate
        for env/rolling/state. Otherwise prepare() would rewrite candidate aliases using the
        previous state and route to retired containers.
        """
        record = self.state.get('switch')
        if not isinstance(record, dict):
            return
        phase, release = record.get('phase'), str(record.get('release'))
        previous_dir = self.root / 'releases' / release
        try:
            before_state = json.loads((previous_dir / 'before-state.json').read_text())
            base = {key: value for key, value in before_state.items() if key not in ('switch', 'unverified')}
            if phase in (SWITCH_NONE, SWITCH_CADDY_WRITING, SWITCH_ROLLING_BACK):
                if phase != SWITCH_NONE:
                    # rolling-back: the previous Caddyfile of that release; caddy-writing: the backup
                    # install_caddy took of the previous live file just before writing.
                    source = (previous_dir / 'before-caddy' if phase == SWITCH_ROLLING_BACK
                              else self.live_caddy.with_name('Caddyfile.bak'))
                    backup = source.read_bytes()
                    if not self._validate_staged(backup):
                        raise RuntimeError(f'{source.name} failed caddy validate')
                    if self.live_caddy.read_bytes() != backup:
                        write_caddy_in_place(self.live_caddy, backup)
                sources, route = ('before-release.env', 'before-rolling.json'), 'previous'
                # not-switched means the run stopped before the live file was touched, so the
                # previous release is still the verified one: a same-sha retry keeps its
                # already-deployed shortcut. The later phases replaced or restored the live file
                # after the last verification, so those stay unverified.
                state = {**base}
                if phase != SWITCH_NONE or before_state.get('unverified'):
                    state['unverified'] = True
            elif phase in SWITCH_ROUTED_TO_CANDIDATE:
                sources, route = ('release.env', 'rolling.compose.json'), 'candidate'
                services = record.get('services')
                if not services:
                    # Records written by intermediate revisions carry no services; the release
                    # name fully determines them, so a retry does not have to fail here.
                    services = {kind: kind + '_' + release.replace('-', '_') for kind in APPLICATION_SERVICES}
                    print(f'WARNING: switch record for {release} has no services; derived them from the '
                          f'release name.', flush=True)
                sha = record.get('sha') or release_snapshot_sha(previous_dir)
                if not sha:
                    print(f'WARNING: switch record for {release} has no sha and the release snapshot has no '
                          f'manifest; the next deployment will not take the already-deployed shortcut.', flush=True)
                state = {'release': release, 'services': services,
                         'previous': {key: value for key, value in base.items() if key != 'previous'},
                         'unverified': True}
                if sha:
                    state['sha'] = sha
            else:
                raise RuntimeError('unknown or incomplete switch record')
            write_atomic(self.env, (previous_dir / sources[0]).read_bytes())
            write_atomic(self.rolling, (previous_dir / sources[1]).read_bytes())
            write_private(self.state_path, state)
        except Exception as error:
            self.mark_degraded(f'interrupted switch of {release} (phase {phase}) could not be reconciled '
                               f'({type(error).__name__}: {error}); live Caddyfile, env, rolling and state were '
                               'not all aligned; verify them manually')
            raise
        self.state = state
        print(f'Reconciled interrupted switch of {release} (phase {phase}): live Caddyfile, env, rolling and '
              f'state now all name the {route} release (unverified).', flush=True)
        # Files alone do not prove what the Caddy process runs: a run killed between the live write
        # and the reload leaves the process on the other release. Send USR1 now so the process
        # matches the reconciled file; a failure marks the deployment degraded via reload().
        if phase != SWITCH_NONE:
            self.reload()
        self.stop_interrupted_candidates()

    def stop_interrupted_candidates(self):
        """Remove application containers left by an interrupted run that no release claims."""
        wanted = set(self.state.get('services', {}).values())
        wanted |= set(self.state.get('previous', {}).get('services', {}).values())
        # Nothing is claimed, so nothing can be proven unclaimed: an empty set would make every
        # application container an orphan. Without a recorded live release, leave Docker alone.
        if not wanted:
            print('WARNING: state records no live or previous services; skipping candidate container '
                  'cleanup so running containers are never treated as orphans.', flush=True)
            return
        try:
            ids = command(['docker', 'ps', '-aq', '--filter',
                           'label=com.docker.compose.project=' + self.config['project'],
                           '--filter', 'label=com.docker.compose.oneoff=False']).splitlines()
            if not ids:
                return
            containers = json.loads(command(['docker', 'inspect', *ids]))
        except Exception as error:
            print(f'WARNING: could not list candidate containers from an interrupted run '
                  f'({type(error).__name__}); the next cleanup retries it.', flush=True)
            return
        orphaned = []
        for container in containers:
            labels = container.get('Config', {}).get('Labels') or {}
            service = labels.get('com.docker.compose.service', '')
            if (labels.get('com.docker.compose.project') != self.config['project']
                    or labels.get('com.docker.compose.oneoff') == 'True'
                    or not re.fullmatch(APPLICATION_SERVICE_PATTERN, service)
                    or service in wanted):
                continue
            orphaned.append(container['Id'])
        if not orphaned:
            return
        try:
            command(['docker', 'stop', '--timeout', '30', *orphaned])
            command(['docker', 'rm', *orphaned])
        except Exception as error:
            print(f'WARNING: could not remove {len(orphaned)} candidate container(s) from an interrupted '
                  f'run ({type(error).__name__}); the next cleanup retries it.', flush=True)
            return
        print(f'Removed {len(orphaned)} candidate container(s) left by an interrupted run.', flush=True)

    def _previous_state(self):
        return json.loads((self.directory / 'before-state.json').read_text())

    def _candidate_state(self):
        previous = {key: value for key, value in self._previous_state().items()
                    if key not in ('previous', 'switch', 'unverified')}
        # Not verified: the "already deployed" shortcut must not trust it, but prepare() derives
        # the next release's aliases from these candidate services, matching the live Caddyfile.
        return {'sha': self.payload['sha'], 'release': self.release, 'services': self.services,
                'previous': previous, 'unverified': True}

    def _abort_rollback(self, cause):
        """Abort before Caddy is switched back, aligning env, rolling and state with the switch phase.

        The recorded phase (not file contents) says which release the live Caddyfile belongs to.
        Env, rolling and github-state.json are made to match it so that prepare() after a manual
        marker removal derives aliases from the same release the live Caddyfile names. The marker
        records what the Caddy process may actually be running and every file that failed to align.
        """
        phase = self.switch_phase()
        failed = []
        target = None
        try:
            if phase in SWITCH_ROUTED_TO_CANDIDATE:
                target = 'candidate'
                plan = [(self.env, lambda: (self.directory / 'release.env').read_bytes()),
                        (self.rolling, lambda: (self.directory / 'rolling.compose.json').read_bytes()),
                        (self.state_path, lambda: self._state_bytes(self._candidate_state(), phase))]
            elif phase in (SWITCH_NONE, SWITCH_CADDY_WRITING):
                target = 'previous'
                plan = [(self.env, lambda: (self.directory / 'before-release.env').read_bytes()),
                        (self.rolling, lambda: (self.directory / 'before-rolling.json').read_bytes()),
                        (self.state_path, lambda: self._state_bytes(
                            {**self._previous_state(), **({'unverified': True} if phase == SWITCH_CADDY_WRITING else {})},
                            phase))]
            else:
                plan = []
            for path, source in plan:
                try:
                    content = source()
                    if not path.exists() or path.read_bytes() != content:
                        if path == self.state_path:
                            write_atomic(path, content, mode=0o600)
                        else:
                            path.write_bytes(content)
                except Exception as error:
                    failed.append(f'{path.name} ({type(error).__name__})')
        except Exception as error:  # defensive: the marker must still be written truthfully
            failed.append(f'alignment ({type(error).__name__})')
        names = f'{self.env.name}, {self.rolling.name} and {self.state_path.name}'
        if target is None:
            files = (f'switch phase is unknown, so {self.live_caddy.name}, {names} were left as found '
                     'and may name different releases')
        elif failed:
            files = (f'{self.live_caddy.name} belongs to the {target} release; aligning {", ".join(failed)} '
                     f'failed, so those files may hold either release')
        else:
            files = f'{self.live_caddy.name} file, {names} all name the {target} release'
            if target == 'candidate':
                files += ('; if this marker is removed manually, the next deployment treats this unverified '
                          'candidate as its rollback target')
            if phase == SWITCH_CADDY_WRITING:
                # The in-place write may have completed, stopped part-way or been undone by recovery.
                files = (f'{self.live_caddy.name} content is unknown (may be the candidate, partially written '
                         f'or the previous release) and needs manual verification; {names} name the previous '
                         'release (state marked unverified)')
        process = SWITCH_PROCESS.get(phase, 'the Caddy process state is unknown')
        message = (f'rollback aborted: {cause}; switch phase {phase or "unknown"}: {process}; {files}; '
                   'Caddy was not switched back by rollback; candidate containers retained')
        self.mark_degraded(message)
        return 'degraded rollback: ' + message

    def _state_bytes(self, state, phase):
        state = {**state, 'switch': self._switch_record(phase, rollback='aborted')}
        return (json.dumps(state, indent=2) + '\n').encode()

    def rollback(self):
        # Read and validate the previous Caddyfile before touching anything. Every abort goes
        # through _abort_rollback, which follows the recorded switch phase.
        try:
            previous = (self.directory / 'before-caddy').read_bytes()
        except Exception as error:
            print('WARNING: ' + self._abort_rollback(
                f'previous Caddyfile {self.directory.name}/before-caddy could not be read '
                f'({type(error).__name__})'), flush=True)
            raise
        try:
            valid = self._validate_staged(previous)
        except Exception as error:
            print('WARNING: ' + self._abort_rollback(
                f'previous Caddyfile could not be validated ({type(error).__name__})'), flush=True)
            raise
        if not valid:
            raise RuntimeError(self._abort_rollback(f'{self.directory / "before-caddy"} failed caddy validate'))
        try:
            self.record_rollback()  # previous state + rolling-back record, in one atomic write
            self.env.write_bytes((self.directory / 'before-release.env').read_bytes())
            self.rolling.write_bytes((self.directory / 'before-rolling.json').read_bytes())
        except Exception as error:
            # Re-align env, rolling and state with the recorded forward phase before raising.
            print('WARNING: ' + self._abort_rollback(
                f'restoring previous env/rolling/state failed ({type(error).__name__})'), flush=True)
            raise
        # Restore and verify the old services before changing the public route. This keeps a failed
        # candidate from being the only service available during rollback. compose() also restores
        # any reclaimed public-MCP proxy endpoint before returning.
        try:
            self.compose('up', '-d', '--no-deps', '--wait', '--wait-timeout', '240',
                         *self.state['services'].values(), 'worker', candidate=False)
        except Exception as error:
            # Caddy has not been switched back; re-align env/rolling/state with the forward phase.
            raise RuntimeError(self._abort_rollback('previous services failed to start')) from error
        print('Previous release services are healthy; switching Caddy back.', flush=True)
        restored = f'{self.env.name}, {self.rolling.name} and {self.state_path.name} hold the previous release'
        try:
            write_caddy_in_place(self.live_caddy, previous)
        except Exception:
            self.mark_degraded(f'rollback Caddyfile write failed; {self.live_caddy.name} may be partially written '
                               f'({caddy_intent_path(self.live_caddy).name} records the expected content); '
                               f'{SWITCH_PROCESS.get(self.switch_phase(), "the Caddy process state is unknown")}; '
                               f'{restored}; candidate containers retained')
            raise
        try:
            self.reload()
        except Exception:
            self.mark_degraded(f'rollback reload failed; {self.live_caddy.name} is the previous Caddyfile but '
                               f'the Caddy process may still run the candidate; {restored}; '
                               'candidate containers retained')
            raise
        try:
            self.verify(expected=self.state.get('sha', '')[:12])
            if 'public-mcp' in self.state['services']:
                self.verify_public_mcp()
            self.update_monitoring(self.state['services'])
        except Exception:
            self.mark_degraded(f'rollback verification or monitoring failed; Caddy was reloaded with the previous '
                               f'Caddyfile; {restored}; candidate containers retained')
            print('WARNING: rollback verification or monitoring failed; candidate containers retained.', flush=True)
            raise
        try:
            write_private(self.state_path, self._previous_state())  # verified: clear the rolling-back record
        except Exception:
            self.mark_degraded(f'rollback verified but clearing the rolling-back record in {self.state_path.name} '
                               f'failed; {restored}; candidate containers retained')
            raise
        candidates = []
        try:
            for service in self.services.values():
                ids = command([
                    'docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + self.config['project'],
                    '--filter', 'label=com.docker.compose.service=' + service,
                    '--filter', 'label=com.docker.compose.oneoff=False'
                ]).splitlines()
                candidates.extend({'Id': container, 'State': {'Running': True},
                                   'Config': {'Labels': {'com.docker.compose.service': service}}}
                                  for container in ids)
        except Exception as error:
            self.mark_degraded(f'rollback restored and verified the previous release, but listing its candidate '
                               f'containers failed ({type(error).__name__}); candidates may still run and must be '
                               f'removed manually; {restored}')
            raise
        try:
            drained = self.drain_retired(candidates, preserve_stream_grace=False, timeout=ROLLBACK_DRAIN_TIMEOUT_SECONDS)
            if not drained and self.config.get('retain_undrained_containers') is True:
                print('WARNING: retain_undrained_containers=true; rollback candidates were kept.', flush=True)
            else:
                if not drained:
                    print('WARNING: rollback drain failed; stopping candidates with a 90s grace period.', flush=True)
                self.compose('stop', '--timeout', '90', *self.services.values())
                # The previous release remains on its original fixed address; remove only the
                # candidate service containers after the old route has been verified.
                self.compose('rm', '--force', '--stop', *self.services.values())
        except Exception as error:
            # The previous release is live and verified; only candidate cleanup failed. Record it so
            # leftover candidates are not mistaken for the running release.
            self.mark_degraded(f'rollback restore was verified but candidate cleanup failed '
                               f'({type(error).__name__}); candidate containers may still run and need manual '
                               f'removal; {restored}')
            raise
        print('Previous routes and worker restored; database was not rolled back.', flush=True)

    def cleanup(self, previous):
        """Stop retired application containers and retain only one rollback release."""
        current = set(self.services.values())
        retained = set(previous.get('services', {}).values())
        ids = command(['docker', 'ps', '-aq', '--filter',
                       'label=com.docker.compose.project=' + self.config['project'],
                       '--filter', 'label=com.docker.compose.oneoff=False']).splitlines()
        if not ids:
            return
        containers = json.loads(command(['docker', 'inspect', *ids]))
        retired = []
        for container in containers:
            labels = container['Config'].get('Labels') or {}
            service = labels.get('com.docker.compose.service', '')
            if (labels.get('com.docker.compose.project') != self.config['project']
                    or labels.get('com.docker.compose.oneoff') == 'True'
                    or not re.fullmatch(APPLICATION_SERVICE_PATTERN, service)
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
        if self.degraded_path.exists():
            raise RuntimeError('deployment blocked by persistent degraded marker. ' + self.recovery_instructions())
        self.degraded_marker_written = False
        self.reconcile_interrupted_switch()
        self.validate_live_caddy()
        if self.state.get('sha') == self.payload['sha'] and not self.state.get('unverified'):
            self.services = self.state['services']
            self.ensure_live_manifest()
            self.verify()
            self.update_monitoring(self.services)
            with phase('Cleanup'):
                self.clean_up_quietly(self.state.get('previous', {}))
            print('This source revision is already deployed.'); return
        # This read-only gate runs before image pulls, release directories, migrations or Caddy
        # changes. A second web replica therefore fails cleanly when the host cannot overlap it.
        self.capacity_gate()
        # The production receiver always has a host .env. Lightweight unit-test doubles may
        # omit the host installation files and exercise the rest of the rollout in isolation.
        if (self.root / '.env').is_file():
            self.public_mcp_preflight()
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
            # A verified release must already carry its asset manifest before
            # routes or deployment state can point at it.
            self.write_manifest()
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
                self.record_switch(SWITCH_NONE)
                # Preserve the inode because Caddy bind-mounts this file.
                self.install_caddy((self.directory / 'Caddyfile.next').read_bytes(),
                                   before_write=lambda: self.record_switch(SWITCH_CADDY_WRITING))
                self.record_switch(SWITCH_CADDY_WRITTEN)
                self.reload()
                self.record_switch(SWITCH_CADDY_RELOADED)
                self.verify()
                self.env.write_bytes((self.directory / 'release.env').read_bytes())
                self.rolling.write_bytes((self.directory / 'rolling.compose.json').read_bytes())
                self.record_switch(SWITCH_CONFIG_PROMOTED)
                self.compose('up', '-d', '--no-deps', '--wait', '--wait-timeout', '180', 'worker', candidate=False)
                self.verify()
                self.update_monitoring(self.services)
        except Exception:
            self.rollback()
            if self.degraded_marker_written:
                # rollback() confirmed old services, restored routing and verification before returning.
                self.degraded_path.unlink()
                self.degraded_marker_written = False
                print('Rollback verified; cleared this deployment\'s degraded marker.', flush=True)
            raise
        previous = {key: value for key, value in self.state.items() if key != 'previous'}
        write_private(self.state_path, {'sha': self.payload['sha'], 'release': self.release, 'services': self.services, 'previous': previous})
        print('Production deployed and verified: ' + self.payload['sha'], flush=True)
        # Cleanup failure must not roll back a verified release, and must not turn a
        # verified release into a failed exit status either: the deploy bootstrap
        # restores the previous receiver on any nonzero status, and this deployment
        # succeeded. A retry resumes cleanup.
        with phase('Cleanup'):
            self.clean_up_quietly(previous)

    def clean_up_quietly(self, previous):
        """Retire old containers without failing a release that is already serving."""
        try:
            self.cleanup(previous)
        except Exception as error:
            print('Cleanup incomplete; the next deployment retries it: ' + str(error), flush=True)
        self.prune(previous)


def self_check():
    """Load this receiver and validate the host configuration without touching the host.

    The deploy bootstrap runs this before it points `current` at a new receiver, so a
    version that cannot even start never replaces one that works. It reads no payload,
    takes no lock and changes nothing; it distinguishes a configuration problem from a
    broken receiver, because a new version may require keys the host does not have yet.
    """
    config = json.loads(Path('/etc/opennavo/github-deploy.json').read_text())
    if not isinstance(config, dict):
        raise ValueError('host configuration is not an object')
    required = ('root', 'project', 'registry', 'compose_file', 'caddy_file', 'caddy_container',
                'postgres_container', 'postgres_user', 'postgres_database', 'local_origin', 'checks')
    missing = [key for key in required if key not in config]
    if missing:
        raise ValueError('host configuration is missing: ' + ', '.join(missing))
    if not isinstance(config['checks'], list) or any(
            not isinstance(check, dict) or 'host' not in check or 'path' not in check for check in config['checks']):
        raise ValueError('host configuration checks must be a list of {host,path} entries')
    print('self-check: receiver loaded and host configuration is valid', flush=True)


def probe():
    """Report whether this receiver can start, without doing anything to the host.

    The deploy bootstrap runs this on a staged receiver before `current` moves, and
    reads three exit codes rather than one: 0 when the receiver loaded, read the host
    configuration and could take its lock; 2 when the lock is held by another
    deployment, which is busy and not broken; anything else for a real failure. A
    single success/failure code cannot separate those last two, and a deployment that
    is merely running would look like a broken receiver.

    It reaches everything main() does before it reads a payload and nothing after:
    main() sweeps stale temporary files before reading stdin, so running main() with
    an empty payload would edit the host just to answer "can you start".
    """
    config = json.loads(Path('/etc/opennavo/github-deploy.json').read_text())
    required = ('root', 'project', 'registry', 'compose_file', 'caddy_file')
    missing = [key for key in required if key not in config]
    if missing:
        raise ValueError('host configuration is missing: ' + ', '.join(missing))
    lock_path = Path(config['root']) / 'deploy/github-deploy.lock'
    # Same file and mode main() uses, so this proves the path main() will open works.
    with lock_path.open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            print('probe: another deployment holds the receiver lock', flush=True)
            sys.exit(PROBE_BUSY)
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)
    print('probe: receiver loaded, host configuration is valid and the lock is free', flush=True)


def main():
    os.umask(0o077)
    config = json.loads(Path('/etc/opennavo/github-deploy.json').read_text())
    with (Path(config['root']) / 'deploy/github-deploy.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        swept = sweep_stale_temp_files(config['root'], config.get('caddy_file'))
        if swept:
            print(f'Removed {swept} stale temporary file(s) left by an interrupted write.', flush=True)
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
        # The deploy bootstrap probes a staged receiver with these before switching to it.
        if '--self-check' in sys.argv[1:]:
            self_check()
        elif '--probe' in sys.argv[1:]:
            probe()
        else:
            main()
    except Exception as error:
        # Never echo input payloads, environment or captured subprocess diagnostics.
        print('Deployment failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
