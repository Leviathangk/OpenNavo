"""Route contract regressions without Docker, network, or browser operations."""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('verification', Path(__file__).with_name('verify-production.py'))
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)


class ProductionRouteTests(unittest.TestCase):
    def setUp(self):
        self.logins = 0
        self.queue_status = 'machine'
        self.login_code = '0000'
        self.queue_code = '0000'

    def response(self, path, host='api.opennavo.localhost', method='GET', data=None, **kwargs):
        def envelope(value, code='0000'):
            return 200, {}, json.dumps({'code': code, 'data': value}).encode()
        if host.startswith('admin.') and path in ['/api/health', '/readyz']:
            return 200, {'Content-Type': 'application/json'}, b'{"status":"ok"}'
        if path == '/readyz':
            return 200, {}, b'OK'
        if host == 'web.opennavo.localhost':
            prefix, name = ('/zh', 'VS Code 编辑器') if path == '/zh' else ('', 'Visual Studio Code')
            headers = {key: 'test' for key in ['Content-Security-Policy', 'Strict-Transport-Security', 'X-Content-Type-Options', 'Referrer-Policy']}
            headers['Content-Type'] = 'text/html'
            return 200, headers, f'<a href="{prefix}/apps/visual-studio-code">{name}</a>'.encode()
        if path == '/admin-api/auth/login':
            self.logins += 1
            return envelope({'token': 'test-token'}, self.login_code)
        if path == '/admin-api/auth/getUserInfo':
            return envelope({}, '8888') if host.startswith('admin.') else (404, {}, b'')
        if path.startswith('/admin-api/translations/queue'):
            if path != '/admin-api/translations/queue?type=release&status=machine&locale=zh-CN':
                return envelope({}, '1000')
            return envelope({'records': [{'type': 'release', 'status': self.queue_status, 'locale': 'zh-CN', 'translated': 'fixture'}]}, self.queue_code)
        if path.startswith('/admin-api'):
            return envelope({'records': [{'id': 1}]})
        if path.startswith('/api/v1/config'):
            return envelope({'links': {'download': v.ENV['WEB_BASE_URL'] + '/download'}})
        if path == '/api/v1/packages':
            return envelope([])
        if path == '/app.js':
            return 200, {'Content-Type': 'application/javascript'}, b'code'
        return 200, {}, b'<div id="app"></div><script src="/app.js"></script>'

    def test_current_translation_contract(self):
        with patch.object(v, 'http', side_effect=self.response):
            v.check_routes()
        self.assertEqual(self.logins, 1)

    def test_wrong_translation_status_is_rejected(self):
        self.queue_status = 'manual'
        with patch.object(v, 'http', side_effect=self.response):
            with self.assertRaisesRegex(AssertionError, 'machine.*zh-CN'):
                v.check_routes()

    def test_contract_failure_is_not_retried_after_login(self):
        self.queue_code = '1000'
        with patch.object(v, 'http', side_effect=self.response), patch.object(v.time, 'sleep') as sleep:
            with self.assertRaisesRegex(AssertionError, 'translations/queue.*1000'):
                v.verify_routes()
        self.assertEqual(self.logins, 1)
        sleep.assert_not_called()

    def test_auth_failure_is_observable_and_not_retried(self):
        self.login_code = '8888'
        with patch.object(v, 'http', side_effect=self.response), patch.object(v.time, 'sleep') as sleep:
            with self.assertRaisesRegex(AssertionError, 'auth/login.*8888'):
                v.verify_routes()
        self.assertEqual(self.logins, 1)
        sleep.assert_not_called()

    def test_startup_waits_without_logging_in(self):
        responses = [OSError('starting'), (503, {}, b''), (200, {}, b'')]
        def ready(*args, **kwargs):
            if responses:
                response = responses.pop(0)
                if isinstance(response, Exception):
                    raise response
                return response
            return 200, {}, b''
        with patch.object(v, 'http', side_effect=ready), patch.object(v.time, 'sleep') as sleep, patch.object(v, 'check_routes') as routes:
            v.verify_routes()
        self.assertEqual(sleep.call_count, 2)
        routes.assert_called_once()
        self.assertEqual(self.logins, 0)


if __name__ == '__main__':
    unittest.main()
