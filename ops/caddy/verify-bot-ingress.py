#!/usr/bin/env python3
"""Run real Caddy ingress tests without external networking, production config or pulls."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent
IMAGE = 'registry.hub.docker.com/library/caddy:2.11.6-alpine'


def main():
    # Inspect only an already installed image; do not contact a registry.
    result = subprocess.run(['docker', 'image', 'inspect', IMAGE], check=True, capture_output=True)
    arch = json.loads(result.stdout)[0]['Architecture']
    with tempfile.TemporaryDirectory(prefix='opennavo-bot-ingress-') as directory:
        binary = Path(directory) / 'ingress.test'
        env = dict(os.environ, CGO_ENABLED='0', GOOS='linux', GOARCH=arch)
        subprocess.run(['go', 'test', '-c', '-o', str(binary), str(ROOT / 'verified_bot_test.go')],
                       env=env, check=True)
        subprocess.run([
            'docker', 'run', '--rm', '--pull', 'never', '--network', 'none', '--read-only',
            '--cap-drop', 'ALL', '--cap-add', 'NET_BIND_SERVICE', '--security-opt', 'no-new-privileges:true',
            '--tmpfs', '/tmp:rw,nosuid,size=64m',
            '-v', f'{binary}:/test/ingress.test:ro', '-v', f'{ROOT}/Caddyfile:/fixture/Caddyfile:ro',
            '--entrypoint', '/test/ingress.test', IMAGE, '-test.v', '-test.timeout=45s',
        ], check=True)


if __name__ == '__main__':
    main()
