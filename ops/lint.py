#!/usr/bin/env python3
""" Validate configuration and fixtures only; do not start services or read local secrets."""
import importlib.util
import json
import ipaddress
from pathlib import Path
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('production_verification', ROOT / 'ops/verify-production.py')
verification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verification)

verification.compose('config', '--quiet')
# SSR may forward identity only over the dedicated network; never trust the shared default subnet.
config = json.loads(verification.compose('config', '--format', 'json').stdout)
services = config['services']
# Page cache eviction must never remove queue/session/rate-limit keys.
assert services['web']['environment']['NUXT_REDIS_URL'] == 'redis://web-cache:6379/0'
assert services['api']['environment']['REDIS_URL'] == 'redis://redis:6379/0'
assert services['api']['environment']['CACHE_REDIS_URL'] == 'redis://web-cache:6379/1'
assert services['web']['depends_on']['web-cache']['condition'] == 'service_healthy'
cache_command = services['web-cache']['command']
assert cache_command[cache_command.index('--maxmemory-policy') + 1] == 'allkeys-lru'
assert cache_command[cache_command.index('--maxmemory') + 1] == '768mb'
assert not services['web-cache'].get('ports')
business_command = services['redis']['command']
assert business_command[business_command.index('--maxmemory-policy') + 1] == 'noeviction'
assert business_command[business_command.index('--maxmemory') + 1] == '1024mb'

mcp_spec = importlib.util.spec_from_file_location('mcp_verification', ROOT / 'ops/verify-mcp.py')
mcp_verification = importlib.util.module_from_spec(mcp_spec)
mcp_spec.loader.exec_module(mcp_verification)
mcp_verification.validate_config(config)
# Caddy's fixed address must remain available even when it starts after dynamic replicas.
proxy_ipam = config['networks']['proxy']['ipam']['config'][0]
proxy_subnet = ipaddress.ip_network(proxy_ipam['subnet'])
proxy_pool = ipaddress.ip_network(proxy_ipam['ip_range'])
caddy_ip = ipaddress.ip_address(services['caddy']['networks']['proxy']['ipv4_address'])
assert proxy_pool.subnet_of(proxy_subnet), 'proxy dynamic pool must stay inside the subnet'
assert caddy_ip in proxy_subnet and caddy_ip not in proxy_pool, 'Caddy IP must be outside the dynamic pool'
assert caddy_ip not in (proxy_subnet.network_address, proxy_subnet.broadcast_address,
    ipaddress.ip_address(proxy_ipam.get('gateway', str(proxy_subnet.network_address + 1)))), 'Caddy IP must be a usable non-gateway address'
public_mcp_ip = ipaddress.ip_address(services['public-mcp']['networks']['proxy']['ipv4_address'])
assert public_mcp_ip in proxy_subnet and public_mcp_ip not in proxy_pool and public_mcp_ip != caddy_ip, 'public-mcp IP must be reserved outside the dynamic pool and Caddy address'
public_mcp_alt_ip = ipaddress.ip_address(verification.ENV.get('PUBLIC_MCP_PROXY_IP_ALT', '172.30.80.4'))
assert public_mcp_alt_ip in proxy_subnet and public_mcp_alt_ip not in proxy_pool and public_mcp_alt_ip != caddy_ip, 'alternate public-mcp IP must be reserved outside the dynamic pool and Caddy address'
trusted_proxies = services['api']['environment']['TRUSTED_PROXIES'].split(',')
assert str(public_mcp_ip) + '/32' in trusted_proxies, 'API must trust the reserved public-mcp IP as /32'
assert str(public_mcp_alt_ip) + '/32' in trusted_proxies, 'API must trust the alternate public-mcp IP as /32'
assert not any(str(public_mcp_ip).rsplit('.', 1)[0] + '/24' == entry for entry in trusted_proxies), 'API must not trust the public-mcp subnet'

assert {name for name, service in services.items() if 'ssr' in service.get('networks', {})} == {'api', 'web'}
assert config['networks']['ssr']['internal']
assert services['web']['environment']['NUXT_API_BASE_INTERNAL'] == 'http://api-ssr:8080/api/v1'
assert 'api-ssr' in services['api']['networks']['ssr']['aliases']
assert 'web-proxy' in services['web']['networks']['proxy']['aliases']
assert services['web']['environment']['NUXT_TRUSTED_PROXIES'] == services['caddy']['networks']['proxy']['ipv4_address']
assert config['networks']['ssr']['ipam']['config'][0]['subnet'] in services['api']['environment']['TRUSTED_PROXIES'].split(',')

# Check configuration wiring and required values without printing or saving randomly generated verification signing keys.
assert verification.ENV.get('NUXT_OG_IMAGE_SECRET')
template = (ROOT / 'ops/.env.example').read_text()
assert 'NUXT_OG_IMAGE_SECRET=\n' in template
production = (ROOT / 'ops/docker-compose.prod.yml').read_text()
assert 'NUXT_OG_IMAGE_SECRET: ${NUXT_OG_IMAGE_SECRET:?' in production
for key in ['NUXT_PUBLIC_SITE_URL', 'NUXT_PUBLIC_I18N_BASE_URL']:
    assert f'{key}: ${{WEB_BASE_URL}}' in production
    assert f'{key}=${{WEB_BASE_URL}}' in template
for script in ['backup.sh', 'restore.sh', 'migrate.sh']:
    verification.run(['sh', '-n', 'ops/' + script])
verification.run(['python3', '-I', '-m', 'unittest', 'discover', '-s', 'ops', '-p', 'test_*.py'], capture=False)
# `discover -s ops` only recurses into packages; ops/prometheus is plain files mounted into the
# container, so run its probe tests explicitly or they never reach CI.
verification.run(['python3', '-I', '-m', 'unittest', 'discover', '-s', 'ops/prometheus', '-p', 'test_*.py'], capture=False)
verification.run(['docker', 'run', '--rm', '-v', str(ROOT / 'ops/prometheus') + ':/etc/prometheus:ro',
    '--entrypoint', '/bin/promtool', 'registry.hub.docker.com/prom/prometheus:v3.15.0',
    'check', 'config', '/etc/prometheus/prometheus.yml'], capture=False)
verification.run(['node', 'tooling/scripts/tasks/server.mjs', 'alerts-test'], capture=False)
# ops/GUIDE.md is read by a human on the production host, so a block that does not
# parse costs a debugging session there. Nothing else in CI executes those blocks.
verification.run(['python3', '-I', str(ROOT / 'ops/check_guide_commands.py')], capture=False)
verification.run(['docker', 'run', '--rm', '-v', str(ROOT / 'ops/caddy/Caddyfile') + ':/etc/caddy/Caddyfile:ro',
    '-e', 'WEB_DOMAIN', '-e', 'API_DOMAIN', '-e', 'ADMIN_DOMAIN', '-e', 'PUBLIC_BASE_URL', '-e', 'CADDY_GLOBAL_OPTIONS',
    'registry.hub.docker.com/library/caddy:2.11.6-alpine', 'caddy', 'validate', '--config', '/etc/caddy/Caddyfile'], capture=False)
# Exercise the installer against the actual repository file, its unmarked predecessor,
# and the current production layout (no MCP route, existing trusted-proxy options).
deploy_spec = importlib.util.spec_from_file_location('github_deploy', ROOT / 'ops/github-deploy.py')
deploy = importlib.util.module_from_spec(deploy_spec)
deploy_spec.loader.exec_module(deploy)
source = (ROOT / 'ops/caddy/Caddyfile').read_text()
legacy = source.replace('\t' + deploy.PUBLIC_MCP_BEGIN + '\n', '').replace('\t' + deploy.PUBLIC_MCP_END + '\n', '')
start = source.index('\t' + deploy.PUBLIC_MCP_BEGIN)
end = source.index(deploy.PUBLIC_MCP_END) + len(deploy.PUBLIC_MCP_END) + 1
production = (source[:start] + source[end:]).replace('{$CADDY_GLOBAL_OPTIONS}',
    'auto_https off\nservers {\n trusted_proxies static {$NGINX_PROXY_IP:172.30.82.1}\n'
    ' trusted_proxies_strict\n client_ip_headers X-Forwarded-For\n}')
with tempfile.TemporaryDirectory() as directory:
    for fixture in [source, legacy, production]:
        installed = deploy.install_public_mcp_route(fixture, 'public-mcp-proxy-lint')
        assert installed.count('@publicMcp path /mcp') == 1
        assert deploy.install_public_mcp_route(installed, 'public-mcp-proxy-lint') == installed
        (Path(directory) / 'Caddyfile').write_text(installed)
        verification.run(['docker', 'run', '--rm', '-v', directory + ':/etc/caddy:ro',
            '-e', 'WEB_DOMAIN', '-e', 'API_DOMAIN', '-e', 'ADMIN_DOMAIN', '-e', 'PUBLIC_BASE_URL', '-e', 'CADDY_GLOBAL_OPTIONS',
            'registry.hub.docker.com/library/caddy:2.11.6-alpine', 'caddy', 'validate',
            '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'], capture=False)
print('deployment configuration, shell scripts, collection safety and alert rules: passed')
