import contextlib
import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import Mock, call, patch
import urllib.error


spec = importlib.util.spec_from_file_location('warm_edge', Path(__file__).with_name('warm-edge.py'))
warm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(warm)


class Response:
    def __init__(self, status, content_type='text/html'):
        self.status = status
        self.headers = {'Content-Type': content_type}

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def read(self, size=None):
        return b'<html>ok</html>'


class Opener:
    def __init__(self, response):
        self.response = response

    def open(self, request, timeout):
        if isinstance(self.response, BaseException):
            raise self.response
        return self.response


class EdgeWarmupTests(unittest.TestCase):
    def test_404_410_and_redirects_are_recorded_but_do_not_fail(self):
        for status in [301, 404, 410]:
            with self.subTest(status=status):
                self.assertTrue(warm.warm_one('/apps/retired', Opener(Response(status))))

    def test_5xx_and_connection_errors_fail(self):
        self.assertFalse(warm.warm_one('/apps/broken', Opener(Response(503))))
        self.assertFalse(warm.warm_one('/apps/broken', Opener(TimeoutError())))

    def test_http_error_classification_does_not_read_or_print_body(self):
        error = urllib.error.HTTPError('https://opennavo.com/apps/retired', 410, 'gone', {}, None)
        with patch('builtins.print') as output:
            self.assertTrue(warm.warm_one('/apps/retired', Opener(error)))
        self.assertNotIn('gone', ' '.join(str(call) for call in output.call_args_list))

    def test_missing_or_corrupt_path_file_falls_back_to_fixed_list(self):
        with patch.object(warm.Path, 'read_text', side_effect=ValueError('bad json')):
            paths = warm.load_paths('/does/not/matter')
        self.assertEqual(paths[:len(warm.MANDATORY_PATHS)], warm.MANDATORY_PATHS)

    def test_successful_warmup_uses_default_budget_and_cancels_timer(self):
        previous_handler = warm.signal.getsignal(warm.signal.SIGALRM)
        with patch.object(warm, 'load_paths', return_value=['/', '/apps']), \
                patch.object(warm, 'warm_one', return_value=True) as request, \
                patch.object(warm.time, 'sleep'), \
                patch.object(warm.signal, 'setitimer') as timer, \
                contextlib.redirect_stdout(io.StringIO()) as output:
            warm.main(['paths.json'])
        self.assertEqual(request.call_count, 2)
        self.assertEqual(timer.call_args_list, [call(warm.signal.ITIMER_REAL, 300), call(warm.signal.ITIMER_REAL, 0)])
        self.assertEqual(warm.signal.getsignal(warm.signal.SIGALRM), previous_handler)
        self.assertIn('Public edge warmup passed: 2 paths', output.getvalue())

    def test_configured_budget_interrupts_blocked_body_and_reports_skipped_paths(self):
        sleep = warm.time.sleep
        class BlockedResponse(Response):
            def read(self, size=None):
                sleep(30)
                raise AssertionError('the total budget must interrupt a blocked response')

        paths = ['/'] + [f'/apps/fixture-{index}' for index in range(65)]
        opener = Mock()
        opener.open.side_effect = [Response(200), BlockedResponse(200)]
        previous_handler = warm.signal.getsignal(warm.signal.SIGALRM)
        with patch.object(warm, 'load_paths', return_value=paths), \
                patch.object(warm.urllib.request, 'build_opener', return_value=opener), \
                patch.object(warm.time, 'sleep'), \
                self.assertRaisesRegex(SystemExit, r'1s time budget; completed=1, failed=0, skipped=65'):
            warm.main(['paths.json', '--max-seconds', '1'])
        self.assertEqual(opener.open.call_count, 2)
        self.assertEqual(warm.signal.getitimer(warm.signal.ITIMER_REAL)[0], 0)
        self.assertEqual(warm.signal.getsignal(warm.signal.SIGALRM), previous_handler)

    def test_expiration_during_retry_counts_current_path_as_skipped(self):
        with patch.object(warm, 'load_paths', return_value=['/', '/apps']), \
                patch.object(warm, 'warm_one', side_effect=[False, warm.WarmupBudgetExceeded()]) as request, \
                patch.object(warm.time, 'sleep'), \
                patch.object(warm.signal, 'setitimer') as timer, \
                self.assertRaisesRegex(SystemExit, r'7s time budget; completed=0, failed=0, skipped=2'):
            warm.main(['paths.json', '--max-seconds', '7'])
        self.assertEqual(request.call_count, 2)
        self.assertEqual(timer.call_args_list, [call(warm.signal.ITIMER_REAL, 7), call(warm.signal.ITIMER_REAL, 0)])

    def test_completed_connection_failures_still_fail(self):
        with patch.object(warm, 'load_paths', return_value=['/']), \
                patch.object(warm, 'warm_one', return_value=False) as request, \
                patch.object(warm.time, 'sleep'), \
                self.assertRaisesRegex(SystemExit, 'edge warmup failed'):
            warm.main(['paths.json'])
        self.assertEqual(request.call_count, 2)

    def test_invalid_budget_is_rejected_before_warmup(self):
        for value in ['0', '-1', 'invalid']:
            with self.subTest(value=value), patch.object(warm, 'load_paths') as load, \
                    contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
                warm.main(['paths.json', '--max-seconds', value])
            self.assertEqual(error.exception.code, 2)
            load.assert_not_called()


if __name__ == '__main__':
    unittest.main()
