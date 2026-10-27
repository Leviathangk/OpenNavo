"""Loopback-only TLS fixture with a fresh temporary key, never trusted by the OS."""
import http.server
import json
from pathlib import Path
import ssl
import sys
import subprocess
import tempfile

payload = Path(sys.argv[1]).read_bytes()
mode = sys.argv[2] if len(sys.argv) > 2 else "normal"
requests = []
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        requests.append({"range": self.headers.get("Range"), "ifRange": self.headers.get("If-Range")})
        Path(str(sys.argv[1]) + ".requests").write_text(json.dumps(requests))
        if mode == "rate_limit" and len(requests) == 1:
            self.send_response(429)
            self.send_header('Retry-After', '120')
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        body = Path(str(sys.argv[1]) + ".invalid").read_bytes() if mode == "invalid_first" and len(requests) == 1 else payload
        start = int(self.headers.get("Range", "bytes=0-")[6:-1])
        resumed = start > 0 and mode != "changed"
        self.send_response(206 if resumed else 200)
        if not resumed:
            start = 0
        if mode != "no_validator":
            self.send_header('ETag', '"changed"' if mode == "changed" and len(requests) > 1 else '"fixture"')
        self.send_header('Content-Length', str(len(body) - start))
        if resumed:
            actual_start = start + (1 if mode == "bad_range" else 0)
            self.send_header('Content-Range', f'bytes {actual_start}-{len(body) - 1}/{len(body)}')
        self.end_headers()
        if mode in ("interrupt", "changed", "bad_range") and len(requests) == 1:
            self.wfile.write(body[:len(body) // 2])
            self.wfile.flush()
            self.close_connection = True
        else:
            self.wfile.write(body[start:])
    def log_message(self, *args):
        pass

server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
# Only the public certificate is exposed to the Rust client. The private key
# stays in an automatically cleaned temporary directory for this process.
with tempfile.TemporaryDirectory(prefix="opennavo-test-tls-") as temporary:
    root = Path(temporary)
    config = root / "openssl.cnf"
    config.write_text("""[req]
distinguished_name = dn
x509_extensions = extensions
prompt = no
[dn]
CN = localhost
[extensions]
subjectAltName = DNS:localhost,IP:127.0.0.1
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
""")
    certificate = Path(str(sys.argv[1]) + ".cert.pem")
    key = root / "key.pem"
    subprocess.run([
        "/usr/bin/openssl", "req", "-x509", "-newkey", "rsa:2048",
        "-nodes", "-days", "1", "-config", str(config),
        "-keyout", str(key), "-out", str(certificate),
    ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    context.load_cert_chain(certificate, key)
# SSLContext retains the key in memory; no private material remains on disk.
server.socket = context.wrap_socket(server.socket, server_side=True)
print(server.server_port, flush=True)
server.serve_forever()
