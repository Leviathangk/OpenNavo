""" Verify MCP boundaries with production configuration, invoked only by the isolated prod-verify project."""
import json
import struct
import urllib.parse
import uuid
import zlib


def icon_upload(size=256):
    """ Build a multipart body holding one solid PNG icon, without third-party image libraries."""
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    rows = b''.join(b'\x00' + b'\x24\x68\xac' * size for _ in range(size))
    image = (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', size, size, 8, 2, 0, 0, 0))
             + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))
    boundary = uuid.uuid4().hex
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="icon.png"\r\n'
            f'Content-Type: image/png\r\n\r\n').encode() + image + f'\r\n--{boundary}--\r\n'.encode()
    return body, f'multipart/form-data; boundary={boundary}'


def validate_config(config):
    services = config['services']
    holders = {name for name, service in services.items()
               if 'AGENT_GATEWAY_SECRET' in service.get('environment', {})}
    assert holders == {'api', 'mcp'}, 'gateway secret must only reach api/mcp'
    assert services['api']['environment']['AGENT_GATEWAY_SECRET'] == services['mcp']['environment']['AGENT_GATEWAY_SECRET']
    assert len(services['mcp']['environment']['AGENT_GATEWAY_SECRET']) >= 32
    assert services['mcp']['user'] == '10001:10001'
    assert services['mcp']['read_only'] and 'ALL' in services['mcp']['cap_drop']
    assert not services['mcp'].get('ports') and not services['api'].get('ports')
    assert set(services['mcp']['networks']) == {'agent', 'proxy'}
    assert {name for name, service in services.items() if 'agent' in service.get('networks', {})} == {'api', 'mcp'}
    assert config['networks']['agent']['internal']
    env = services['mcp']['environment']
    assert env['OPENNAVO_AGENT_API_BASE'] == 'http://api-agent:8080/agent-api'
    assert json.loads(env['OPENNAVO_MCP_TRUSTED_PROXIES']) == [services['caddy']['networks']['proxy']['ipv4_address'] + '/32']
    assert not set(env) & {'DATABASE_URL', 'REDIS_URL', 'S3_ACCESS_KEY', 'S3_SECRET_KEY', 'LLM_API_KEY', 'OPENNAVO_TOKEN'}


def rpc(verification, token, method, params=None, extra_headers=None):
    status, _, body = verification.http('/mcp', 'admin.opennavo.localhost', 'POST',
        {'jsonrpc': '2.0', 'id': uuid.uuid4().hex, 'method': method, 'params': params or {}},
        authorization='Bearer ' + token if token else None,
        extra_headers={'Accept': 'application/json, text/event-stream', **(extra_headers or {})})
    if status != 200:
        return status, None
    text = body.decode()
    if text.startswith('event:') or text.startswith('data:'):
        packets = [json.loads(line[5:].strip()) for line in text.splitlines() if line.startswith('data:')]
        assert packets, 'MCP must return a JSON-RPC message'
        packet = packets[-1]
    else:
        packet = json.loads(text)
    assert 'error' not in packet, 'MCP JSON-RPC operation failed'
    return status, packet['result']


def verify(verification):
    config = json.loads(verification.compose('config', '--format', 'json').stdout)
    validate_config(config)
    for host in ['api.opennavo.localhost', 'admin.opennavo.localhost', 'web.opennavo.localhost']:
        for path in ['/agent-api', '/agent-api/introspect', '/agent-api/packages', '/agent-api/system/users']:
            for method in ['GET', 'POST']:
                assert verification.http(path, host, method)[0] == 404, 'internal Agent route exposed'
    for path in ['/mcp/healthz', '/mcp/other', '/healthz']:
        assert verification.http(path, 'admin.opennavo.localhost')[0] == 404
    assert rpc(verification, None, 'tools/list')[0] == 401
    assert rpc(verification, 'invalid-verification-token', 'tools/list')[0] == 401

    def admin(path, data=None, method='GET', jwt=None):
        status, _, raw = verification.http('/admin-api' + path, 'admin.opennavo.localhost', method, data,
                                           authorization='Bearer ' + jwt if jwt else None)
        envelope = json.loads(raw)
        assert status == 200 and envelope['code'] == '0000', 'deployment admin operation failed: ' + path
        return envelope['data']

    jwt = admin('/auth/login', {'userName': verification.ENV['ADMIN_BOOTSTRAP_USERNAME'],
                'password': verification.ENV['ADMIN_BOOTSTRAP_PASSWORD']}, 'POST')['token']
    settings = admin('/agent/settings', jwt=jwt)
    assert len(settings['grantablePermissions']) == 21
    client = admin('/agent/clients', {'name': 'B5 disposable deployment check'}, 'POST', jwt)
    issued = admin(f'/agent/clients/{client["id"]}/tokens',
                   {'name': 'B5 deployment check', 'permissions': settings['grantablePermissions'],
                    'allowDelete': True, 'ipAllowlist': []}, 'POST', jwt)
    token = issued['plaintext']
    try:
        status, initialized = rpc(verification, token, 'initialize', {
            'protocolVersion': '2025-11-25', 'capabilities': {},
            'clientInfo': {'name': 'opennavo-deployment-check', 'version': '1'}})
        assert status == 200 and initialized['serverInfo']['name'] == 'OpenNavo'
        status, listed = rpc(verification, token, 'tools/list')
        assert status == 200 and len(listed['tools']) == 61
        status, resources = rpc(verification, token, 'resources/list')
        assert status == 200 and len(resources['resources']) == 7
        status, guide = rpc(verification, token, 'resources/read', {'uri': 'opennavo://guide/daily-routine'})
        assert status == 200 and guide['contents']
        forged = '198.51.100.77'
        status, identity = rpc(verification, token, 'tools/call', {'name': 'whoami', 'arguments': {}},
                              {'X-Forwarded-For': forged, 'X-Agent-Client-IP': forged,
                               'X-Agent-Gateway-Key': 'untrusted-client-header'})
        assert status == 200 and not identity.get('isError', False)
        assert identity['structuredContent']['untrusted']['active']
        actual_ip = verification.sql(f'SELECT last_used_ip::text FROM agent_tokens WHERE id={int(issued["token"]["id"])}').stdout.decode().strip()
        assert actual_ip and actual_ip.split('/')[0] != forged, 'caller must not spoof IP allowlist identity'
        status, packages = rpc(verification, token, 'tools/call', {'name': 'packages_search', 'arguments': {'size': 1}})
        assert status == 200 and not packages.get('isError', False)
        assert len(packages['structuredContent']['untrusted']['records']) == 1
        # Local files bypass MCP: the single-use URL must route through Caddy's admin origin to the API.
        status, issued = rpc(verification, token, 'tools/call', {'name': 'asset_upload_ticket', 'arguments': {'kind': 'icon'}})
        assert status == 200 and not issued.get('isError', False)
        upload_url = issued['structuredContent']['untrusted']['uploadUrl']
        assert upload_url.startswith(verification.ENV['ADMIN_ORIGIN'] + '/admin-api/assets/upload-tickets/onvu_')
        ticket = upload_url.rsplit('/', 1)[1]
        target = urllib.parse.urlsplit(upload_url)
        body, content_type = icon_upload()
        uploads = []
        for _ in range(2):
            status, _, raw = verification.http(target.path, target.hostname, 'POST', body,
                                               extra_headers={'Content-Type': content_type})
            assert status == 200, 'ticket upload must reach the API through the admin origin'
            uploads.append(json.loads(raw)['code'])
        assert uploads == ['0000', '8888'], 'ticket uploads must succeed once and then be rejected'
        # Pass credentials through stdin to the owned container's verifier, never command arguments or raw response output.
        code = '''import json, os, sys, urllib.request, uuid
payload = json.load(sys.stdin)
results = []
for gateway, bearer, path in [(False, True, '/introspect'), (True, False, '/introspect'), (True, True, '/introspect'), (True, True, '/app-config')]:
 headers = {'X-Agent-Client-IP': '127.0.0.1', 'X-Request-ID': str(uuid.uuid4()), 'X-Agent-Tool': 'whoami'}
 if gateway: headers['X-Agent-Gateway-Key'] = os.environ['AGENT_GATEWAY_SECRET']
 if bearer: headers['Authorization'] = 'Bearer ' + payload['token']
 request = urllib.request.Request(os.environ['OPENNAVO_AGENT_API_BASE'] + path, headers=headers, method='POST' if path == '/introspect' else 'GET')
 with urllib.request.urlopen(request, timeout=10) as response: results.append(json.load(response)['code'])
print(json.dumps({'codes': results, 'uid': os.getuid()}))
'''
        checked = verification.compose('exec', '-T', 'mcp', 'python', '-I', '-c', code,
                                       input=json.dumps({'token': token}).encode())
        internal = json.loads(checked.stdout)
        assert internal['codes'] == ['1004', '8888', '0000', '1004']
        assert internal['uid'] == 10001
        oversized = verification.http('/mcp', 'admin.opennavo.localhost', 'POST',
            {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/list', 'params': {'padding': 'x' * (2 * 1024 * 1024 + 1)}},
            authorization='Bearer ' + token, extra_headers={'Accept': 'application/json, text/event-stream'})
        assert oversized[0] == 413, 'Caddy must enforce the MCP request body limit'
    finally:
        admin(f'/agent/tokens/{issued["token"]["id"]}/revoke', {}, 'POST', jwt)
    status, revoked = rpc(verification, token, 'tools/call', {'name': 'whoami', 'arguments': {}})
    assert status == 401 or (status == 200 and revoked.get('isError') is True)
    assert rpc(verification, token, 'tools/list')[0] == 401, 'revocation must invalidate discovery cache'
    logs = verification.compose('logs', '--no-color', 'api', 'mcp', 'caddy').stdout
    assert all(secret.encode() not in logs for secret in [token, jwt, ticket, verification.ENV['AGENT_GATEWAY_SECRET']]), 'credential appeared in container logs'
    print('MCP production route/auth/tools/resources/IP/body-limit/ticket-upload/non-root/log checks: passed', flush=True)
    return {'tools': 61, 'resources': 7, 'grantablePermissions': 21, 'publicMissingToken': 401,
            'publicAgentRoute': 404, 'internalMissingGateway': '1004', 'internalMissingToken': '8888',
            'containerUID': internal['uid'], 'spoofedClientIPRejected': True, 'oversizeBody': 413,
            'ticketUploads': uploads, 'revokedTokenRejected': True, 'credentialLogMatches': 0}
