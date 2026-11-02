""" Verify isolated connections and process protection without connecting to infrastructure."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
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
        with patch.dict(os.environ, {'OPENNAVO_E2E_BUILD_CACHE': 'gha-save'}):
            self.assertEqual(e2e.compose_files(True), ['-f', 'ops/docker-compose.e2e.yml', '-f', 'ops/docker-compose.e2e.ci.yml', '-f', 'ops/docker-compose.e2e.ci-save.yml'])


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
        # A rerun of migration 00110 creates seo_archive again, so the reset removes it too.
        self.assertIn('DROP SCHEMA IF EXISTS seo_archive CASCADE', ddl[ddl.index('-c') + 1])
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


class FailingStepTests(unittest.TestCase):
    """A failed setup step must say which one, and must not say anything else.

    A CI run once failed in `prepare()` and printed only `E2E setup failed: CalledProcessError`, so
    the log could not distinguish the database call from a `go build` or one of the seeded tools.
    """

    # A realistic command line and environment: the message must never echo either.
    ARGS = ['go', 'build', '-o', '/tmp/e2e/migrate', './cmd/migrate']
    ENV = {'DATABASE_URL': 'postgres://opennavo:opennavo-dev-only@127.0.0.1:55433/opennavo_e2e', 'PATH': '/usr/bin'}

    def failing(self, code=1):
        return subprocess.CalledProcessError(code, self.ARGS, output='', stderr='compile: something broke')

    def test_a_failed_step_names_the_stage_and_exit_status(self):
        for stage in ('go build ./cmd/migrate', 'migrate', 'go build ./cmd/storage-init',
                      'storage-init', 'go build ./cmd/seed-e2e', 'seed-e2e', 'go build ./cmd/api'):
            with patch.object(e2e, 'command', side_effect=self.failing(7)):
                with self.assertRaises(e2e.StepError) as caught:
                    e2e.step(stage, self.ARGS, env=self.ENV)
            self.assertEqual(caught.exception.stage, stage)
            self.assertEqual(caught.exception.returncode, 7)
            self.assertEqual(str(caught.exception), f'{stage} exited 7')

    def test_the_message_never_echoes_the_command_line_or_environment(self):
        with patch.object(e2e, 'command', side_effect=self.failing()):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.step('seed-e2e', self.ARGS, env=self.ENV)
        message = str(caught.exception)
        for secret in ('/tmp/e2e/migrate', './cmd/migrate', 'postgres://', 'opennavo-dev-only', '/usr/bin'):
            self.assertNotIn(secret, message)

    def test_prepare_names_the_exact_step_that_failed(self):
        # The loop must attribute a failure to the right tool, not merely to "prepare".
        for failing_at in ('go build ./cmd/migrate', 'migrate', 'go build ./cmd/storage-init',
                           'storage-init', 'go build ./cmd/seed-e2e', 'seed-e2e'):
            def fake_step(stage, args, **kwargs):
                if stage == failing_at:
                    raise e2e.StepError(stage, 1)
                return ''
            with patch.object(e2e, 'step', side_effect=fake_step), \
                 patch.object(e2e, 'database'), patch.object(e2e, 'environment', return_value={}):
                with self.assertRaises(e2e.StepError) as caught:
                    e2e.prepare(True)
            self.assertEqual(caught.exception.stage, failing_at)

    def test_database_names_the_query_and_the_create_separately(self):
        # These two calls capture their output, so before this the log showed neither which one failed
        # nor what the server said.
        with patch.object(e2e, 'compose', side_effect=self.failing(1)):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.database(True)
        self.assertEqual(caught.exception.stage, 'database: query opennavo_e2e')

        with patch.object(e2e, 'compose', side_effect=['', self.failing(2)]):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.database(True)
        self.assertEqual(caught.exception.stage, 'database: create opennavo_e2e')
        self.assertEqual(caught.exception.returncode, 2)

    def test_compose_up_failure_is_attributed_to_the_stack_not_the_step_before_it(self):
        with patch.object(e2e, 'compose', side_effect=self.failing(3)):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.labelled('compose up --wait', lambda: e2e.compose(
                    True, 'up', '-d', '--wait', '--wait-timeout', '240', capture=False))
        self.assertEqual(caught.exception.stage, 'compose up --wait')
        self.assertEqual(caught.exception.returncode, 3)

    def test_a_non_zero_exit_is_the_only_thing_treated_as_a_step_failure(self):
        # A successful step returns its captured stdout unchanged.
        with patch.object(e2e, 'command', return_value='1') as command:
            self.assertEqual(e2e.step('go build ./cmd/migrate', self.ARGS), '1')
        self.assertFalse(command.call_args.kwargs['capture'], 'step output streams; it is not captured')

    def test_a_step_that_never_started_names_the_stage_without_its_path(self):
        # A missing binary raises OSError, whose message carries the path it could not open — that is
        # the command line. The stage alone is what the log gets.
        with patch.object(e2e, 'command', side_effect=FileNotFoundError(2, 'No such file or directory', '/tmp/e2e/migrate')):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.step('migrate', self.ARGS, env=self.ENV)
        self.assertEqual(caught.exception.stage, 'migrate')
        self.assertIsNone(caught.exception.returncode)
        self.assertEqual(str(caught.exception), 'migrate could not start')
        self.assertNotIn('/tmp/e2e/migrate', str(caught.exception))

    def test_the_cleanup_after_a_failure_does_not_replace_the_original_error(self):
        # The `ci` path tears the stack down on any exception. If that teardown fails too (docker is
        # gone, a container is wedged), its error must not become the one the log reports.
        def fake_compose(ci, *args, **kwargs):
            if 'down' in args:
                raise FileNotFoundError(2, 'No such file or directory', 'docker')
            return ''

        with patch.object(e2e, 'ports_free'), patch.object(e2e, 'owned_process', return_value=None), \
             patch.object(e2e, 'prepare', side_effect=e2e.StepError('migrate', 1)), \
             patch.object(e2e, 'compose', side_effect=fake_compose), \
             patch.object(e2e, 'STATE', Path(tempfile.mkdtemp())), \
             patch.object(e2e.sys, 'argv', ['e2e.py', 'up', '--ci']):
            with self.assertRaises(e2e.StepError) as caught:
                e2e.main()
        self.assertEqual(caught.exception.stage, 'migrate')

    def test_runtime_failures_still_print_their_own_message(self):
        # main()'s handler prints str(error) for RuntimeError and StepError, and the type name for
        # everything else; a step failure must not regress to a bare type name.
        self.assertTrue(issubclass(e2e.StepError, RuntimeError))
        self.assertEqual(str(e2e.StepError('seed-e2e', 1)), 'seed-e2e exited 1')


class DatabaseRetryTests(unittest.TestCase):
    """database() retries only a not-ready failure, and retries the whole sequence rather than one command."""

    def failure(self, stderr, code=1):
        return subprocess.CalledProcessError(code, ['docker'], output='', stderr=stderr)

    def run_database(self, effects, reset=False):
        # `effects` is consumed one per compose() call across every attempt, so a retry that re-runs
        # the existence check is visible: the second attempt gets the next effect. The tail is padded
        # so a test only has to spell out the calls it cares about.
        effects = list(effects) + [''] * 8
        calls = []

        def fake_compose(ci, *args, **kwargs):
            calls.append(args)
            effect = effects.pop(0)
            if isinstance(effect, Exception):
                raise effect
            return effect

        with patch.object(e2e, 'compose', side_effect=fake_compose), \
             patch.object(e2e, 'time') as clock:
            clock.sleep = lambda seconds: None
            e2e.database(True, reset=reset)
        return calls

    def test_a_transient_failure_is_retried_and_then_succeeds(self):
        for message in ('the database system is shutting down',
                        'the database system is starting up',
                        'terminating connection due to administrator command',
                        'could not connect to server: Connection refused',
                        # libpq writes "Connection refused" with a capital C; the match must not
                        # depend on which layer produced the message.
                        'psql: error: connection to server at "127.0.0.1", port 55433 failed: '
                        'Connection refused',
                        'psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" '
                        'failed: No such file or directory'):
            with self.subTest(message=message):
                # query fails once, then the retry re-runs the query and finds the database absent.
                calls = self.run_database([self.failure(message), '', ''])
                self.assertEqual(len(calls), 3, 'the query was re-run, not resumed')
                self.assertEqual(calls[0][3], 'psql')
                self.assertEqual(calls[1][3], 'psql')
                self.assertEqual(calls[2][3], 'createdb')

    def test_a_permanent_failure_is_not_retried(self):
        # A syntax error is a real bug; retrying it only delays the report. The FATAL cases are the
        # ones that decide whether the reach markers are narrow enough: each arrives through libpq
        # with the very socket path and "connection to server" lead-in a retryable failure carries,
        # and only the words after them say the server answered and refused us. The last of those
        # carries the socket path on its second hint line rather than in the failure itself, so it
        # also pins that a marker must not key on "could not connect to server" or on the path.
        for message in ('ERROR: syntax error at or near "SELEC"',
                        'permission denied for database postgres',
                        'psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" '
                        'failed: FATAL:  permission denied for database "postgres"',
                        'psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" '
                        'failed: FATAL:  password authentication failed for user "opennavo"',
                        'psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" '
                        'failed: FATAL:  role "nosuchuser" does not exist',
                        'psql: error: could not connect to server: Permission denied\n'
                        '        Is the server running locally and accepting connections on Unix domain socket '
                        '"/var/run/postgresql/.s.pgsql.5432"?',
                        'could not connect to server: perm denied',
                        'dial unix /var/run/docker.sock: connect: connection refused',
                        'ERROR: relation "x" does not exist: No such file or directory candidate',
                        'ERROR:  duplicate key value violates unique constraint "users_email_key"',
                        # These four are the NEAR counter-examples for markers that key on a
                        # ready-state phrase. `test_every_near_miss_is_itself_a_permanent_case`
                        # requires them here, but they earn the place on their own: each is the
                        # same opening words as a retryable failure followed by a permanent cause,
                        # which is exactly the shape a marker widened by one word would swallow.
                        'psql: error: FATAL:  database system was interrupted while in recovery; '
                        'manual recovery required',
                        'psql: error: FATAL:  database system startup failed: invalid checkpoint record',
                        'psql: error: FATAL:  terminating connection failed: role "opennavo" does not exist',
                        'ERROR:  schema "public" already exists'):
            with self.subTest(message=message):
                with self.assertRaises(e2e.StepError) as caught:
                    self.run_database([self.failure(message)])
                self.assertEqual(caught.exception.stage, 'database: query opennavo_e2e')
                self.assertEqual(caught.exception.returncode, 1)

    def test_five_transient_failures_stop_at_five_attempts(self):
        with patch.object(e2e, 'compose') as compose, patch.object(e2e.time, 'sleep') as sleep:
            compose.side_effect = self.failure('the database system is shutting down')
            with self.assertRaises(e2e.StepError) as caught:
                e2e.database(True)
        self.assertEqual(caught.exception.stage, 'database: query opennavo_e2e')
        self.assertEqual(compose.call_count, 5, 'exactly five attempts, then raise')
        self.assertEqual(sleep.call_count, 4, 'no sleep after the final attempt')

    def test_a_raced_create_converges_because_the_whole_sequence_re_runs(self):
        # The window this exists for: the query says the database is absent, but createdb loses a race
        # with the init server and fails with `duplicate key`. Retrying createdb alone cannot recover
        # (it is not idempotent); re-running the sequence must see it now exists and skip the create.
        calls = self.run_database([
            '',                                                          # attempt 1: query -> absent
            self.failure('duplicate key value violates unique constraint "pg_database_datname_index"'),
            '1',                                                         # attempt 2: query -> now exists
        ])
        self.assertEqual([call[3] for call in calls], ['psql', 'createdb', 'psql'],
                         'the retry re-queried and did not retry createdb')
        self.assertNotIn('createdb', [call[3] for call in calls[2:]], 'no second createdb')

    def test_the_other_spelling_of_the_same_race_converges(self):
        # The same loss reported the other way. The init server creates the database itself, so a
        # create that reaches the server after the existence check finds it there; Postgres says
        # `already exists` rather than raising a unique violation. It is retryable only because
        # step_database() runs createdb solely when the query said absent, so here this message
        # cannot mean anything but the race. Retrying the sequence resolves it exactly as above.
        calls = self.run_database([
            '',                                                          # attempt 1: query -> absent
            self.failure('createdb: error: database creation failed: ERROR:  database '
                         '"opennavo_e2e" already exists'),
            '1',                                                         # attempt 2: query -> now exists
        ])
        self.assertEqual([call[3] for call in calls], ['psql', 'createdb', 'psql'],
                         'the retry re-queried and did not retry createdb')
        self.assertNotIn('createdb', [call[3] for call in calls[2:]], 'no second createdb')

    def test_a_reset_retries_from_the_query_not_from_the_failed_statement(self):
        # The reset DDL is not idempotent either; a transient failure there must restart the sequence.
        calls = self.run_database([
            '1',                                                        # attempt 1: query -> exists, no create
            self.failure('terminating connection due to administrator command'),   # reset DDL fails here
            '1',                                                        # attempt 2 starts at the query
        ], reset=True)
        # Attempt 1 stops at the DDL (2 calls). Attempt 2 runs it all: query, DDL, two FLUSHDBs.
        self.assertEqual([call[3] for call in calls],
                         ['psql', 'psql', 'psql', 'psql', 'redis-cli', 'redis-cli'])
        self.assertIn("SELECT 1 FROM pg_database", calls[2][calls[2].index('-Atc') + 1],
                      'the retry started again at the existence query, not at the failed DDL')

    def test_the_retry_stage_name_keeps_the_step_that_failed(self):
        # Retrying must not flatten the stage back to "database": #141's whole point was naming the step.
        with patch.object(e2e, 'compose') as compose, patch.object(e2e.time, 'sleep'):
            compose.side_effect = self.failure('the database system is shutting down')
            with self.assertRaises(e2e.StepError) as caught:
                e2e.database(True)
        self.assertEqual(str(caught.exception), 'database: query opennavo_e2e exited 1')

    def test_only_stderr_can_make_a_failure_retryable(self):
        # psql and createdb report on stderr; stdout is data. Scanning both would let an echoed
        # result that happens to contain a marker turn a permanent failure into five attempts.
        with patch.object(e2e, 'compose') as compose, patch.object(e2e.time, 'sleep'):
            compose.side_effect = subprocess.CalledProcessError(
                1, ['docker'], output='the database system is starting up', stderr='')
            with self.assertRaises(e2e.StepError):
                e2e.database(True)
        self.assertEqual(compose.call_count, 1, 'stdout must not be scanned for the whitelist')

    def test_the_retry_classification_never_reaches_the_log(self):
        # detail is used to classify and must never be printed: it can carry the command line.
        error = e2e.StepError('database: create opennavo_e2e', 1,
                              'psql: error: connection to server failed')
        self.assertNotIn('psql: error', str(error))
        self.assertIn('psql: error', error.detail)


class RetryWhitelistCoverageTests(unittest.TestCase):
    """Every retry marker must be exercised by a transient case and contradicted by no permanent one.

    The whitelist is an open set: a marker added without a case is invisible, because no existing
    assertion ever names it. This reads the case lists out of this file instead of trusting whoever
    adds the next marker, so an unexercised marker fails here.

    It cannot tell whether a marker is *correct* - a permanent error written as a case of its own
    would satisfy the coverage check. That is why the permanent cases stay: they are the only place
    that says "this wording must never be retried". This test only closes the "forgot the case" gap.
    """

    # Named, not discovered. Three transient methods, not one: the two race spellings have tests of
    # their own, and the markers that describe them appear only there.
    TRANSIENT_CASES = ('test_a_transient_failure_is_retried_and_then_succeeds',
                       'test_a_raced_create_converges_because_the_whole_sequence_re_runs',
                       'test_the_other_spelling_of_the_same_race_converges')
    PERMANENT_CASES = ('test_a_permanent_failure_is_not_retried',)

    # marker -> a message with the same wording but a permanent cause. Keyed by the marker itself
    # rather than by position, so reordering TRANSIENT cannot silently pair the wrong two.
    NEAR = {
        'the database system is shutting down':
            'psql: error: FATAL:  database system was interrupted while in recovery; manual recovery required',
        'the database system is starting up':
            'psql: error: FATAL:  database system startup failed: invalid checkpoint record',
        'terminating connection due to administrator command':
            'psql: error: FATAL:  terminating connection failed: role "opennavo" does not exist',
        ('to server', 'connection refused'):
            'dial unix /var/run/docker.sock: connect: connection refused',
        'failed: no such file or directory':
            'ERROR: relation "x" does not exist: No such file or directory candidate',
        'pg_database_datname_index':
            'ERROR:  duplicate key value violates unique constraint "users_email_key"',
        f'database "{e2e.DB_NAME}" already exists':
            'ERROR:  schema "public" already exists',
    }

    def messages(self, names):
        """The case messages of the named methods, read from the syntax that feeds them in.

        A message is either an element of a case list or the literal passed straight to
        self.failure(). Taking those two shapes rather than every string in the body keeps assert
        explanations out without a length heuristic, and a renamed variable or method-local
        refactor does not matter.
        """
        import ast
        tree = ast.parse(Path(__file__).read_text(encoding='utf-8'))
        found, seen = [], set()
        for node in ast.walk(tree):
            if not isinstance(node, ast.FunctionDef) or node.name not in names:
                continue
            seen.add(node.name)
            for sub in ast.walk(node):
                if isinstance(sub, (ast.Tuple, ast.List)):
                    found.extend(item.value for item in sub.elts
                                 if isinstance(item, ast.Constant) and isinstance(item.value, str))
                elif (isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute)
                      and sub.func.attr == 'failure'):
                    found.extend(arg.value for arg in sub.args
                                 if isinstance(arg, ast.Constant) and isinstance(arg.value, str))
        # A rename or a delete would empty a list and make both assertions below pass vacuously.
        self.assertEqual(seen, set(names), 'a named case is missing; the extraction would narrow silently')
        return found

    @staticmethod
    def matches(marker, text):
        parts = marker if isinstance(marker, tuple) else (marker,)
        return all(part in text.lower() for part in parts)

    def test_every_marker_has_a_case_that_exercises_it(self):
        transient = self.messages(self.TRANSIENT_CASES)
        self.assertGreaterEqual(len(transient), 5, 'the transient case list was not found')
        for marker in e2e.TRANSIENT:
            with self.subTest(marker=marker):
                self.assertTrue(any(self.matches(marker, message) for message in transient),
                                f'{marker!r} is in TRANSIENT but no transient case exercises it')

    def test_no_marker_matches_a_permanent_failure(self):
        permanent = self.messages(self.PERMANENT_CASES)
        self.assertGreaterEqual(len(permanent), 8, 'the permanent case list was not found')
        for marker in e2e.TRANSIENT:
            with self.subTest(marker=marker):
                hits = [message[:40] for message in permanent if self.matches(marker, message)]
                self.assertEqual(hits, [], f'{marker!r} also matches a permanent failure: {hits}')
    def test_every_marker_rejects_its_own_near_miss(self):
        # Both loops below run once per marker, so an empty TRANSIENT would run them zero times
        # and report green: the suite would claim every marker was checked when none was. The
        # bound makes the vacuous case fail here instead of passing quietly. The number is not
        # the contract - the careful user-facing cases are - so it only needs to be a floor low
        # enough not to argue with a genuine removal, which the older cases would catch anyway.
        self.assertGreaterEqual(len(e2e.TRANSIENT), 1, 'the marker list was emptied, so this proves nothing')
        missing = [marker for marker in e2e.TRANSIENT if marker not in self.NEAR]
        self.assertEqual(missing, [], f'these markers have no near-miss: {missing}')
        for marker in e2e.TRANSIENT:
            with self.subTest(marker=marker):
                self.assertFalse(self.matches(marker, self.NEAR[marker]),
                                 f'{marker!r} matches its own near-miss, so it is too wide')

    def test_no_near_miss_belongs_to_a_different_marker(self):
        # One near-miss per marker, and it must be that marker's alone. A pair of markers sharing
        # one near-miss would let a widening hide behind the other's entry.
        for marker, near in self.NEAR.items():
            with self.subTest(marker=marker):
                others = [str(other) for other in e2e.TRANSIENT
                          if other != marker and self.matches(other, near)]
                self.assertEqual(others, [], f'{near!r} also matches {others}, not only {marker!r}')

    def test_every_near_miss_is_itself_a_permanent_case(self):
        # NEAR says "this wording looks like the marker but is a permanent cause". If the same
        # message is not in the permanent corpus, nothing ever runs the classifier over it, so the
        # claim in NEAR is asserted by hand rather than measured. Four of the seven were in that
        # state: the list only ever grew where a marker needed a counter-example.
        permanent = self.messages(self.PERMANENT_CASES)
        absent = [marker for marker, near in self.NEAR.items()
                  if not any(near.strip() == message.strip() for message in permanent)]
        self.assertEqual(absent, [], f'these near-misses are not permanent cases: {absent}')

    def test_no_near_miss_outlives_its_marker(self):
        # The other half of the bound above: removing a marker leaves its entry here, and every
        # loop in this class iterates over TRANSIENT, so the leftover is never looked at. It reads
        # as coverage of a marker that no longer exists. Dropping a marker is a real edit the
        # older cases would catch on their own, so this only has to stop the table going stale.
        orphaned = [marker for marker in self.NEAR if marker not in e2e.TRANSIENT]
        self.assertEqual(orphaned, [], f'these near-misses outlive a removed marker: {orphaned}')


if __name__ == '__main__':
    unittest.main()
