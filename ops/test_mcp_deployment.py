""" Deployment boundary regressions: parse only isolated verification configuration; never start or modify services."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


verification = load('production_verification', 'verify-production.py')
mcp = load('mcp_deployment', 'verify-mcp.py')


class MCPDeploymentTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = json.loads(verification.compose('config', '--format', 'json').stdout)

    def test_production_network_credentials_and_user(self):
        mcp.validate_config(self.config)

    def test_forbidden_secret_recipient_fails(self):
        for name in ['worker', 'web', 'caddy', 'seed', 'migrate', 'backup']:
            changed = copy.deepcopy(self.config)
            changed['services'][name].setdefault('environment', {})['AGENT_GATEWAY_SECRET'] = 'forbidden'
            with self.subTest(service=name), self.assertRaises(AssertionError):
                mcp.validate_config(changed)

    def test_public_port_or_root_or_infrastructure_credentials_fail(self):
        for field, value in [('ports', [{'target': 8790, 'published': '8790'}]), ('user', '0'),
                             ('networks', {'default': {}, 'proxy': {}, 'agent': {}})]:
            changed = copy.deepcopy(self.config)
            changed['services']['mcp'][field] = value
            with self.subTest(field=field), self.assertRaises(AssertionError):
                mcp.validate_config(changed)
        changed = copy.deepcopy(self.config)
        changed['services']['mcp']['environment']['DATABASE_URL'] = 'forbidden'
        with self.assertRaises(AssertionError):
            mcp.validate_config(changed)

    def test_gateway_template_empty_and_verification_random(self):
        self.assertIn('AGENT_GATEWAY_SECRET=\n', (ROOT / '.env.example').read_text())
        with patch.dict(verification.os.environ, {'AGENT_GATEWAY_SECRET': 'do-not-inherit', 'LLM_BASE_URL': 'https://do-not-inherit.example', 'LLM_SETTINGS_ENCRYPTION_KEY': 'do-not-inherit'}):
            first, second = verification.environment(), verification.environment()
        self.assertGreaterEqual(len(first['AGENT_GATEWAY_SECRET']), 32)
        self.assertNotEqual(first['AGENT_GATEWAY_SECRET'], second['AGENT_GATEWAY_SECRET'])
        self.assertNotEqual(first['AGENT_GATEWAY_SECRET'], 'do-not-inherit')
        self.assertEqual(first['LLM_BASE_URL'], 'http://127.0.0.1:1/v1')
        self.assertNotIn('LLM_SETTINGS_ENCRYPTION_KEY', first)

    def test_caddy_exact_mcp_route_and_internal_deny(self):
        text = (ROOT / 'caddy/Caddyfile').read_text()
        self.assertEqual(text.count('@internalAgent path /agent-api /agent-api/*'), 3)
        self.assertEqual(text.count('handle @internalAgent {'), 3)
        self.assertIn('handle /mcp {', text)
        self.assertNotIn('handle /mcp/* {', text)
        self.assertIn('max_size 2MB', text)
        self.assertIn('header_up -X-Agent-Gateway-Key', text)
        self.assertIn('header_up -X-Agent-Client-IP', text)

    def test_caddy_hides_internal_sitemap_feed(self):
        text = (ROOT / 'caddy/Caddyfile').read_text()
        api = text[text.index('{$API_DOMAIN} {'):text.index('{$ADMIN_DOMAIN} {')]
        # Only the web server reads the package sitemap feed, over api-ssr; the public API host answers 404 before proxying.
        self.assertIn('@internalSitemap path /api/v1/sitemap/packages\n\thandle @internalSitemap {\n\t\trespond 404', api)
        self.assertLess(api.index('handle @internalSitemap {'), api.index('handle @public {'))

    def test_monitoring_requires_exactly_six_current_schedules(self):
        def responses(count):
            values = [
                {'data': {'activeTargets': [{'health': 'up', 'labels': {'job': job}}
                                            for job in ['api', 'api', 'asynq']]}},
                {'database': 'ok'}, {'dashboard': {'panels': [{}] * 12}},
                {'entries': [{}] * count},
            ]
            return [(200, {}, json.dumps(value).encode()) for value in values]
        with patch.object(verification, 'http', side_effect=responses(6)):
            verification.check_monitoring()
        for count in [5, 7, 8]:
            with self.subTest(count=count), patch.object(verification, 'http', side_effect=responses(count)):
                with self.assertRaises(AssertionError):
                    verification.check_monitoring()


if __name__ == '__main__':
    unittest.main()
