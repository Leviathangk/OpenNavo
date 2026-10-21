#!/usr/bin/env python3
""" Start a dedicated E2E API; reuse local infrastructure locally and an isolated Compose project in CI."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import signal
import secrets
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
STATE = ROOT / 'apps/server/tmp/e2e'
BINARY = STATE / 'api'
PID_FILE = STATE / 'api.pid'
DB_NAME = 'opennavo_e2e'
REDIS_DB = 9
WEB_REDIS_DB = 10
BUCKET = 'opennavo-e2e'
API_BASE = 'http://127.0.0.1:18082'


def command(args, *, env=None, capture=True, cwd=ROOT):
    result = subprocess.run(args, cwd=cwd, env=env, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else ''


def compose_files(ci):
    name = 'e2e' if ci else 'dev'
    files = ['-f', f'ops/docker-compose.{name}.yml']
    # CI builds E2E images from layers in the GitHub Actions cache; main also writes them.
    mode = os.environ.get('OPENNAVO_E2E_BUILD_CACHE') if ci else None
    if mode in ('gha', 'gha-save'):
        files += ['-f', 'ops/docker-compose.e2e.ci.yml']
    if mode == 'gha-save':
        files += ['-f', 'ops/docker-compose.e2e.ci-save.yml']
    return files


def compose(ci, *args, capture=True):
    return command(['docker', 'compose', *compose_files(ci), *args], capture=capture)


def environment(ci):
    # Read public defaults only; connections are fixed and isolated, never inherited development connections, gateway credentials, or production tokens.
    env = {k: v for k, v in os.environ.items() if k in
           ('PATH', 'HOME', 'GOCACHE', 'GOMODCACHE', 'GOPATH', 'GOPROXY', 'GOSUMDB', 'GOTOOLCHAIN', 'TMPDIR', 'SYSTEMROOT')}
    for line in (ROOT / 'apps/server/.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            env[key] = value
    pg_port, redis_port, s3_port = (55433, 56380, 19000) if ci else (55432, 56379, 9000)
    env.update(APP_ENV='dev', HTTP_ADDR='127.0.0.1:18082', METRICS_ADDR='127.0.0.1:19090',
               PUBLIC_BASE_URL=API_BASE, WEB_BASE_URL='http://127.0.0.1:3000',
               ADMIN_ORIGIN=os.environ.get('E2E_ADMIN_ORIGIN', 'http://127.0.0.1:9527'),
               PUBLIC_CORS_ORIGINS=','.join(f'http://{host}:{port}' for host in ('127.0.0.1', 'localhost')
                                          for port in (3000, 9527, 1420)),
               DATABASE_URL=f'postgres://opennavo:opennavo-dev-only@127.0.0.1:{pg_port}/{DB_NAME}?sslmode=disable',
               REDIS_URL=f'redis://127.0.0.1:{redis_port}/{REDIS_DB}',
               S3_ENDPOINT=f'127.0.0.1:{s3_port}', S3_BUCKET=BUCKET,
               CDN_BASE_URL=f'http://127.0.0.1:{s3_port}/{BUCKET}',
               E2E_ADMIN_PASSWORD=os.environ.get('E2E_ADMIN_PASSWORD', env['E2E_ADMIN_PASSWORD']),
               JWT_ACCESS_TTL=os.environ.get('E2E_JWT_ACCESS_TTL', env['JWT_ACCESS_TTL']),
               LLM_ENABLED='false', LLM_API_KEY='', LLM_BASE_URL='http://127.0.0.1:1/v1',
               HOMEBREW_API_BASE='http://127.0.0.1:1/api', CI_RELEASE_TOKEN='',
               ADMIN_BOOTSTRAP_USERNAME='', ADMIN_BOOTSTRAP_PASSWORD='', OTEL_EXPORTER_OTLP_ENDPOINT='')
    STATE.mkdir(parents=True, exist_ok=True)
    gateway_file = STATE / 'agent-gateway.key'
    if not gateway_file.exists():
        descriptor = os.open(gateway_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, 'w') as stream:
            stream.write(secrets.token_urlsafe(48))
    env['AGENT_GATEWAY_SECRET'] = gateway_file.read_text().strip()
    return env


def frontend_environment(ci):
    redis_port, s3_port = (56380, 19000) if ci else (56379, 9000)
    return {'NUXT_API_BASE_INTERNAL': API_BASE + '/api/v1', 'NUXT_PUBLIC_API_BASE': API_BASE + '/api/v1',
            'NUXT_PUBLIC_SITE_URL': 'http://127.0.0.1:3000',
            'NUXT_PUBLIC_I18N_BASE_URL': 'http://127.0.0.1:3000',
            'NUXT_PUBLIC_CDN_BASE': f'http://127.0.0.1:{s3_port}/{BUCKET}',
            'NUXT_REDIS_URL': f'redis://127.0.0.1:{redis_port}/{WEB_REDIS_DB}',
            'VITE_SERVICE_BASE_URL': API_BASE + '/admin-api',
            'VITE_API_BASE': API_BASE + '/api/v1'}


def fingerprint(pid):
    try:
        return command(['ps', '-p', str(pid), '-o', 'lstart=', '-o', 'args='])
    except subprocess.CalledProcessError:
        return ''


def owned_process():
    if not PID_FILE.exists():
        return None
    record = json.loads(PID_FILE.read_text())
    current = fingerprint(record['pid'])
    if not current:
        PID_FILE.unlink()
        return None
    if record['binary'] != str(BINARY) or record['identity'] != current or str(BINARY) not in current:
        raise RuntimeError('PID identity differs; refusing to signal another process')
    return record


def down():
    record = owned_process()
    if record:
        os.kill(record['pid'], signal.SIGTERM)
        deadline = time.monotonic() + 15
        while fingerprint(record['pid']) == record['identity']:
            if time.monotonic() >= deadline:
                raise RuntimeError('owned API did not stop within 15 seconds')
            time.sleep(0.1)
        PID_FILE.unlink(missing_ok=True)


def ports_free():
    for port in (18082, 19090):
        with socket.socket() as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind(('127.0.0.1', port))
            except OSError as error:
                raise RuntimeError(f'port {port} is occupied; refusing to stop its owner') from error


def ready():
    try:
        with urllib.request.urlopen(API_BASE + '/readyz', timeout=2) as reply:
            return reply.status == 200 and json.load(reply).get('code') == '0000'
    except (OSError, ValueError, urllib.error.URLError):
        return False


def wait_ready(process):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('E2E API exited; see apps/server/tmp/e2e/api.log')
        if ready():
            return
        time.sleep(0.2)
    raise RuntimeError('E2E API readiness timed out; see apps/server/tmp/e2e/api.log')


def database(ci, *, reset=False):
    exists = compose(ci, 'exec', '-T', 'postgres', 'psql', '-U', 'opennavo', '-d', 'postgres',
                     '-Atc', "SELECT 1 FROM pg_database WHERE datname='opennavo_e2e'")
    if exists != '1':
        compose(ci, 'exec', '-T', 'postgres', 'createdb', '-U', 'opennavo', DB_NAME)
    if reset:
        # Database name and Redis database index are constants; never accept user-supplied development connections.
        compose(ci, 'exec', '-T', 'postgres', 'psql', '-U', 'opennavo', '-d', DB_NAME, '-v', 'ON_ERROR_STOP=1',
                '-c', 'DROP SCHEMA public CASCADE; CREATE SCHEMA public AUTHORIZATION opennavo;')
        for number in (REDIS_DB, WEB_REDIS_DB):
            compose(ci, 'exec', '-T', 'redis', 'redis-cli', '-n', str(number), 'FLUSHDB')


def prepare(ci, *, reset=False):
    database(ci, reset=reset)
    env = environment(ci)
    for executable in ('migrate', 'storage-init', 'seed-e2e'):
        # Run tools from the isolated directory too, preventing configuration loaders from reading development .env.local files.
        binary = STATE / executable
        command(['go', 'build', '-o', str(binary), './cmd/' + executable],
                cwd=ROOT / 'apps/server', env=env, capture=False)
        command([str(binary)], cwd=STATE, env=env, capture=False)


def start(ci):
    ports_free()
    env = environment(ci)
    command(['go', 'build', '-trimpath', '-o', str(BINARY), './cmd/api'], cwd=ROOT / 'apps/server',
            env=env, capture=False)
    with (STATE / 'api.log').open('a') as log:
        process = subprocess.Popen([str(BINARY)], cwd=STATE, env=env,
                                   stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
    try:
        identity = fingerprint(process.pid)
        if not identity or str(BINARY) not in identity:
            raise RuntimeError('unable to record owned API identity')
        PID_FILE.write_text(json.dumps({'pid': process.pid, 'identity': identity, 'binary': str(BINARY),
                                       'ci': ci, 'access_ttl': env['JWT_ACCESS_TTL']}))
        wait_ready(process)
    except BaseException:
        process.terminate()
        process.wait(timeout=15)
        PID_FILE.unlink(missing_ok=True)
        raise


def check_running_access_ttl(record, ci):
    # Do not restart an existing service automatically: the frontend could assume short-lived tokens are active while using the old API.
    actual = record.get('access_ttl')
    requested = environment(ci)['JWT_ACCESS_TTL']
    if actual != requested and (actual is not None or 'E2E_JWT_ACCESS_TTL' in os.environ):
        raise RuntimeError('E2E access TTL differs or is unknown; run e2e-down before changing E2E_JWT_ACCESS_TTL')


def output(ci):
    variables = frontend_environment(ci)
    (STATE / 'frontend.env').write_text(''.join(f'{key}={value}\n' for key, value in variables.items()))
    print('\nE2E API ready; frontend environment:')
    for key, value in variables.items():
        print(f'{key}={value}')
    if os.environ.get('GITHUB_ENV'):
        with Path(os.environ['GITHUB_ENV']).open('a') as stream:
            stream.write(''.join(f'{key}={value}\n' for key, value in variables.items()))
    print('E2E account: e2e-super (password from E2E_ADMIN_PASSWORD; see apps/server/.env.example)')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('task', choices=('up', 'down', 'reset'))
    parser.add_argument('--ci', action='store_true', help='Use an isolated CI Compose project')
    args = parser.parse_args()
    STATE.mkdir(parents=True, exist_ok=True)
    with (STATE / 'operation.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        record = owned_process()
        ci = record['ci'] if record else args.ci
        if args.task == 'down':
            down()
            if ci:
                compose(True, 'down', '--volumes', '--remove-orphans')
            print('E2E API stopped; development infrastructure preserved')
            return
        if os.environ.get('APP_ENV') == 'prod':
            raise RuntimeError('E2E setup is forbidden in prod')
        if record and record['ci'] != args.ci:
            raise RuntimeError('E2E API mode differs; stop it before changing infrastructure')
        if args.task == 'up' and record:
            check_running_access_ttl(record, ci)
            if not ready():
                raise RuntimeError('owned E2E API is unhealthy; stop it before restarting')
            output(ci)
            return
        if record:
            down()
        ports_free()
        try:
            if ci:
                # CI infrastructure uses only public, fixed development defaults. Stream its
                # build/start diagnostics; keep database commands and application output captured.
                compose(True, 'up', '-d', '--wait', '--wait-timeout', '240', capture=False)
            prepare(ci, reset=args.task == 'reset')
            if args.task == 'up' or record:
                start(ci)
                output(ci)
            else:
                print('E2E data reset; API remains stopped')
        except BaseException:
            if ci:
                compose(True, 'down', '--volumes', '--remove-orphans')
            raise


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, OSError) as error:
        # Never echo subprocess arguments/environment or responses; secrets enter only the subprocess environment.
        print('E2E setup failed: ' + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
