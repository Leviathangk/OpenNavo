"""Render isolated fixture configuration only; never start the production stack."""
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('bot_verification', ROOT / 'verify-production.py')
verification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verification)


class VerifiedBotConfigTests(unittest.TestCase):
    def test_default_and_explicit_flag_are_shared_by_caddy_and_nitro(self):
        for flag in [None, 'false', 'true', 'TRUE', '1']:
            env = verification.environment()
            if flag is not None:
                env['OPENNAVO_CF_VERIFIED_BOT_ENABLED'] = flag
            with self.subTest(flag=flag):
                services = json.loads(verification.compose('config', '--format', 'json', env=env).stdout)['services']
                self.assertEqual(services['caddy']['environment']['OPENNAVO_CF_VERIFIED_BOT_ENABLED'], flag or 'false')
                self.assertEqual(services['web']['environment']['NUXT_CF_VERIFIED_BOT_ENABLED'], flag or 'false')
                self.assertEqual(services['web']['environment']['NUXT_VERIFIED_BOT_HEADER'], 'X-OpenNavo-Verified-Bot')
                self.assertEqual(services['web']['environment']['NUXT_TRUSTED_PROXIES'], env['CADDY_PROXY_IP'])
                self.assertFalse(services['web'].get('ports'))

    def test_usage_country_trusts_only_caddy(self):
        env = verification.environment()
        services = json.loads(verification.compose('config', '--format', 'json', env=env).stdout)['services']
        # The API takes CF-IPCountry only from Caddy, which drops it from peers outside Cloudflare.
        self.assertEqual(services['api']['environment']['USAGE_COUNTRY_TRUSTED_PROXIES'], env['CADDY_PROXY_IP'])


if __name__ == '__main__':
    unittest.main()
