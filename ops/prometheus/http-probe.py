#!/usr/bin/env python3
"""Bounded HTTP probes for the existing Prometheus instance (stdlib + curl)."""
import concurrent.futures
import json
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def probe(target):
    command = ['curl', '--silent', '--output', '/dev/null', '--noproxy', '*',
               '--connect-timeout', '2', '--max-time', '4',
               '--user-agent', 'OpenNavo-Probe/1.0', '--write-out', '%{http_code} %{time_total}']
    if target.get('resolve'):
        command += ['--resolve', target['resolve']]
    command += [target['url']]
    code, duration, success = 0, 0.0, 0
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=5)
        status, elapsed = result.stdout.split()
        code, duration = int(status), float(elapsed)
        success = int(result.returncode == 0 and code in target.get('statuses', [200]))
    except (OSError, ValueError, subprocess.TimeoutExpired):
        pass
    labels = 'service=' + json.dumps(target['service']) + ',layer=' + json.dumps(target['layer'])
    return ''.join(f'opennavo_http_probe_{name}{{{labels}}} {value}\n' for name, value in
                   [('success', success), ('status_code', code), ('duration_seconds', duration)])


def serve(config):
    targets = config['targets']
    if not 1 <= len(targets) <= 8:
        raise ValueError('configure between one and eight probes')

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != '/metrics':
                self.send_error(404)
                return
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                body = ''.join(pool.map(probe, targets)).encode()
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
