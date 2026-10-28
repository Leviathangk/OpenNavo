import importlib.util
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


cf = load('cf_readonly', 'cf-readonly.py')


class Response:
    def __init__(self, value):
        self.value = value

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def read(self):
        return json.dumps(self.value).encode()


class CloudflareTests(unittest.TestCase):
    def test_public_path_rejects_queries_encoding_traversal_duplicates_and_private_routes(self):
        for path in ['/apps/foo?x=1', '/apps/%2Ffoo', '/apps/../admin', '/apps//foo', '/zh/api/private', '/_nuxt/app.js']:
            self.assertFalse(cf.public_path(path), path)
        for path in ['/', '/zh/apps/foo', '/apps/airbuild', '/background-items/com.example.agent']:
            self.assertTrue(cf.public_path(path), path)

    def test_top_filters_rows_and_keeps_mandatory_paths(self):
        rows = [
            {'dimensions': {'clientRequestPath': '/apps/ok'}},
            {'dimensions': {'clientRequestPath': '/api/private'}},
            {'dimensions': {'clientRequestPath': '/apps/ok?query=1'}},
        ]
        start = cf.dt.datetime(2026, 1, 1, tzinfo=cf.dt.timezone.utc)
        result = cf._top(rows, start, start)
        self.assertIn('/', result['paths'])
        self.assertIn('/apps/ok', result['paths'])
        self.assertNotIn('/api/private', result['paths'])
        self.assertNotIn('/apps/ok?query=1', result['paths'])

    def test_query_failure_does_not_expose_response_body_or_headers(self):
        with patch.object(cf.urllib.request, 'urlopen', side_effect=RuntimeError('secret response body')):
            with self.assertRaisesRegex(RuntimeError, '^Cloudflare analytics query failed$') as failure:
                cf._load_rows('top', 'a' * 32, cf.dt.datetime.now(cf.dt.timezone.utc),
                              cf.dt.datetime.now(cf.dt.timezone.utc), 'token')
        self.assertNotIn('secret', str(failure.exception))

    def test_main_requires_environment_credentials_without_network(self):
        with patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(SystemExit, 'credentials are unavailable'):
            cf.main(['top'])


if __name__ == '__main__':
    unittest.main()
