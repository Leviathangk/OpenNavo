""" Verify isolated connections and process protection without connecting to infrastructure."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('e2e', Path(__file__).with_name('e2e.py'))
e2e = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(e2e)


class ComposeFileTests(unittest.TestCase):
    def test_ci_build_cache_override_is_opt_in(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop('OPENNAVO_E2E_BUILD_CACHE', None)
            self.assertEqual(e2e.compose_files(True), ['-f', 'ops/docker-compose.e2e.yml'])
        with patch.dict(os.environ, {'OPENNAVO_E2E_BUILD_CACHE': 'gha'}):
            self.assertEqual(e2e.compose_files(True), ['-f', 'ops/docker-compose.e2e.yml', '-f', 'ops/docker-compose.e2e.ci.yml'])
            self.assertEqual(e2e.compose_files(False), ['-f', 'ops/docker-compose.dev.yml'], 'local development never uses it')


class IsolationTests(unittest.TestCase):
    def test_environment_does_not_inherit_development_connections_or_credentials(self):
        with patch.dict(os.environ, {'DATABASE_URL': 'forbidden', 'REDIS_URL': 'forbidden',
                                     'S3_BUCKET': 'forbidden', 'LLM_ENABLED': 'true', 'LLM_API_KEY': 'forbidden',
                                     'JWT_SECRET': 'forbidden', 'E2E_ADMIN_PASSWORD': 'e2e-custom-development'}, clear=False):
            for ci in (False, True):
                env = e2e.environment(ci)
                self.assertIn('/opennavo_e2e?', env['DATABASE_URL'])
                self.assertTrue(env['REDIS_URL'].endswith('/9'))
                self.assertEqual(env['S3_BUCKET'], 'opennavo-e2e')
                self.assertEqual(env['LLM_ENABLED'], 'false')
                self.assertEqual(env['LLM_API_KEY'], '')
                self.assertNotEqual(env['JWT_SECRET'], 'forbidden')
                self.assertEqual(env['E2E_ADMIN_PASSWORD'], 'e2e-custom-development')
                self.assertEqual(env['HTTP_ADDR'], '127.0.0.1:18082')
                self.assertEqual(env['METRICS_ADDR'], '127.0.0.1:19090')
                self.assertTrue(e2e.frontend_environment(ci)['NUXT_REDIS_URL'].endswith('/10'))
                for host in ('127.0.0.1', 'localhost'):
                    for port in (3000, 9527, 1420):
                        self.assertIn(f'http://{host}:{port}', env['PUBLIC_CORS_ORIGINS'])

    def test_admin_origin_default_and_explicit_override_in_both_modes(self):
        for ci in (False, True):
            with patch.dict(os.environ, {'ADMIN_ORIGIN': 'http://unrelated.example'}, clear=True):
                self.assertEqual(e2e.environment(ci)['ADMIN_ORIGIN'], 'http://127.0.0.1:9527')
            with patch.dict(os.environ, {'E2E_ADMIN_ORIGIN': 'http://localhost:9528',
                                         'ADMIN_ORIGIN': 'http://unrelated.example'}, clear=True):
                self.assertEqual(e2e.environment(ci)['ADMIN_ORIGIN'], 'http://localhost:9528')

    def test_access_ttl_default_and_explicit_override_in_both_modes(self):
        for ci in (False, True):
            with patch.dict(os.environ, {'JWT_ACCESS_TTL': '1s'}, clear=True):
                self.assertEqual(e2e.environment(ci)['JWT_ACCESS_TTL'], '2h')
            with patch.dict(os.environ, {'E2E_JWT_ACCESS_TTL': '5s', 'JWT_ACCESS_TTL': '1s'}, clear=True):
                self.assertEqual(e2e.environment(ci)['JWT_ACCESS_TTL'], '5s')

    def test_running_access_ttl_change_requires_explicit_restart(self):
        with patch.dict(os.environ, {'E2E_JWT_ACCESS_TTL': '5s'}, clear=True):
            e2e.check_running_access_ttl({'access_ttl': '5s'}, False)
            for record in ({'access_ttl': '2h'}, {}):
                with self.assertRaisesRegex(RuntimeError, 'e2e-down'):
                    e2e.check_running_access_ttl(record, False)
        with patch.dict(os.environ, {}, clear=True):
            e2e.check_running_access_ttl({}, False)
            e2e.check_running_access_ttl({'access_ttl': '2h'}, True)
            with self.assertRaisesRegex(RuntimeError, 'e2e-down'):
                e2e.check_running_access_ttl({'access_ttl': '5s'}, True)

    def test_reset_can_only_target_fixed_e2e_database_and_redis_numbers(self):
        with patch.object(e2e, 'compose', return_value='1') as compose:
            e2e.database(False, reset=True)
        calls = [c.args for c in compose.call_args_list]
        ddl = next(c for c in calls if 'DROP SCHEMA' in str(c))
        self.assertEqual(ddl[ddl.index('-d') + 1], 'opennavo_e2e')
        flushes = [c for c in calls if 'FLUSHDB' in c]
        self.assertEqual([c[c.index('-n') + 1] for c in flushes], ['9', '10'])

    def test_reused_pid_is_never_signalled(self):
        with tempfile.TemporaryDirectory() as directory:
            pid_file = Path(directory) / 'api.pid'
            pid_file.write_text(json.dumps({'pid': 123, 'identity': 'old', 'binary': str(e2e.BINARY), 'ci': False}))
            with patch.object(e2e, 'PID_FILE', pid_file), patch.object(e2e, 'fingerprint', return_value='unrelated process'), patch.object(e2e.os, 'kill') as kill:
                with self.assertRaisesRegex(RuntimeError, 'identity differs'):
                    e2e.down()
                kill.assert_not_called()
                self.assertTrue(pid_file.exists())

    def test_dead_pid_record_is_removed_without_signalling(self):
        with tempfile.TemporaryDirectory() as directory:
            pid_file = Path(directory) / 'api.pid'
            pid_file.write_text(json.dumps({'pid': 123, 'identity': 'old', 'binary': str(e2e.BINARY), 'ci': False}))
            with patch.object(e2e, 'PID_FILE', pid_file), patch.object(e2e, 'fingerprint', return_value=''), patch.object(e2e.os, 'kill') as kill:
                e2e.down()
                e2e.down()
                kill.assert_not_called()
                self.assertFalse(pid_file.exists())


if __name__ == '__main__':
    unittest.main()
