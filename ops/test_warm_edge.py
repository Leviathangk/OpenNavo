import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
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


if __name__ == '__main__':
    unittest.main()
