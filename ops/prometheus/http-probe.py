#!/usr/bin/env python3
"""Bounded HTTP probes for the existing Prometheus instance (stdlib + curl)."""
import concurrent.futures
import json
import math
import random
import subprocess
import sys
import time
from email.utils import parsedate_to_datetime
from http.server import BaseHTTPRequestHandler, HTTPServer


class ProbePolicy:
    def __init__(self, config=None):
        config = config or {}
        self.concurrency = config.get('concurrency', 1)
        self.attempts = config.get('maxAttempts', 3)
        self.budget = config.get('budgetSeconds', 12)
        if (type(self.concurrency) is not int or not 1 <= self.concurrency <= 4
                or type(self.attempts) is not int or not 1 <= self.attempts <= 3
                or type(self.budget) not in (int, float) or not math.isfinite(self.budget)
                or not 1 <= self.budget <= 12):
            raise ValueError('invalid bounded probe policy')


def retry_delay(header, failures):
    if header and len(header.strip()) <= 10 and header.strip().isascii() and header.strip().isdigit():
        seconds = int(header)
        if seconds <= 2147483647:
            return max(0.1, seconds)
    try:
        due = parsedate_to_datetime(header).timestamp() - time.time()
        if due > 0:
            return due
    except (TypeError, ValueError, OverflowError):
        pass
    base = min(60, 2 ** min(failures, 6))
    return base + random.uniform(0, base / 2)


def probe(target, policy=None, state=None, deadline=None):
    policy = policy or ProbePolicy()
    state = state if state is not None else {}
    deadline = deadline if deadline is not None else time.monotonic() + policy.budget
    command = ['curl', '--silent', '--output', '/dev/null', '--noproxy', '*',
               '--dump-header', '-', '--connect-timeout', '2',
               '--user-agent', 'OpenNavo-Probe/1.0', '--write-out', '%{http_code} %{time_total}']
    if target.get('resolve'):
        command += ['--resolve', target['resolve']]
    code, duration, success, temporary = state.get('code', 0), 0.0, 0, 1
    for attempt in range(policy.attempts):
        remaining = deadline - time.monotonic()
        if state.get('due', 0) > time.monotonic() or remaining <= 0:
            break
        retry_after = None
        transport_ok = False
        started = time.monotonic()
        try:
            result = subprocess.run(command + ['--max-time', str(min(4, remaining)), target['url']],
                                    capture_output=True, text=True, timeout=remaining)
            lines = result.stdout.strip().splitlines()
            status, _elapsed = lines[-1].split()
            code = int(status)
            transport_ok = result.returncode == 0
            # The last header block wins (curl may see proxy/100-continue headers first).
            for line in lines[:-1]:
                if line.upper().startswith('HTTP/'):
                    retry_after = None
                elif line.lower().startswith('retry-after:'):
                    retry_after = line.split(':', 1)[1].strip()
        except (OSError, ValueError, IndexError, subprocess.TimeoutExpired):
            code = 0
        duration += time.monotonic() - started
        temporary = int(not transport_ok or code in (429, 503))
        success = int(transport_ok and code in target.get('statuses', [200]) and not temporary)
        state['code'] = code
        if not temporary:
            state.update(failures=0, due=0)
            break
        failures = min(state.get('failures', 0) + 1, 7)
        delay = retry_delay(retry_after, failures)
        state.update(failures=failures, due=time.monotonic() + delay)
        if attempt + 1 == policy.attempts or state['due'] >= deadline:
            break
        time.sleep(delay)
    retry_in = max(0, state.get('due', 0) - time.monotonic())
    labels = 'service=' + json.dumps(target['service']) + ',layer=' + json.dumps(target['layer'])
    return ''.join(f'opennavo_http_probe_{name}{{{labels}}} {value}\n' for name, value in
                   [('success', success), ('status_code', code), ('duration_seconds', duration),
                    ('temporary_failure', temporary), ('retry_after_seconds', retry_in)])


def probe_targets(targets, states, policy):
    deadline = time.monotonic() + policy.budget
    with concurrent.futures.ThreadPoolExecutor(max_workers=policy.concurrency) as pool:
        return ''.join(pool.map(lambda pair: probe(pair[0], policy, pair[1], deadline),
                                zip(targets, states))).encode()


def serve(config):
    targets = config['targets']
    if not 1 <= len(targets) <= 8:
        raise ValueError('configure between one and eight probes')
    policy = ProbePolicy(config.get('probe'))
    # One slot per configured target: cooldown survives scrapes without a growing URL cache.
    states = [{} for _ in targets]

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != '/metrics':
                self.send_error(404)
                return
            body = probe_targets(targets, states, policy)
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain; version=0.0.4')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    HTTPServer((config['listen'], config.get('port', 19192)), Handler).serve_forever()


if __name__ == '__main__':
    with open(sys.argv[1]) as stream:
        serve(json.load(stream))
