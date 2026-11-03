"""Pin the split-class check: what it catches, and what it must not blame.

The damage this guards against is invisible to `grep '^class'` because the two
halves of a split class have different names. Every case below is written as a
source string and fed to the checker directly, so the test does not depend on
the state of the working tree.
"""
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest

SCRIPT = Path(__file__).with_name('check-split-classes.py')
SPEC = importlib.util.spec_from_file_location('check_split_classes', SCRIPT)
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


def check(source):
    return CHECKER.check(source, 'sample.py')


SPLIT = '''
import unittest


class RetryMarkerNearMissTests(unittest.TestCase):
    def messages(self):
        return ["ERROR: syntax error"]

    def test_first(self):
        self.assertEqual(self.messages(), ["ERROR: syntax error"])


class RetryWhitelistCoverageTests(unittest.TestCase):
    def test_second(self):
        self.assertEqual(self.messages(), ["ERROR: syntax error"])
'''


class SplitClassTests(unittest.TestCase):
    def test_a_helper_left_in_the_other_class_is_reported(self):
        problems = check(SPLIT)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn('RetryWhitelistCoverageTests.test_second', problems[0])
        self.assertIn('self.messages', problems[0])

    def test_the_same_class_with_its_helper_back_is_clean(self):
        # The contrast case: if this also failed, the checker would be blaming
        # every class rather than the split one.
        repaired = SPLIT.replace(
            'class RetryWhitelistCoverageTests(unittest.TestCase):\n',
            'class RetryWhitelistCoverageTests(unittest.TestCase):\n'
            '    def messages(self):\n'
            '        return ["ERROR: syntax error"]\n\n',
        )
        self.assertEqual(check(repaired), [])

    def test_a_skipped_test_is_still_checked(self):
        # `unittest` never runs the body of a skipped test, so a split that only
        # affects skipped tests passes a real test run. This is the reason the
        # check is static, so pin it.
        source = SPLIT.replace(
            '    def test_second(self):',
            '    @unittest.skip("flaky")\n    def test_second(self):',
        )
        self.assertEqual(len(check(source)), 1, check(source))

    def test_an_async_test_is_checked(self):
        source = '''
import unittest


class AsyncSplit(unittest.IsolatedAsyncioTestCase):
    async def test_uses_missing(self):
        self.assertEqual(self.nonexistent_helper(), 1)
'''
        self.assertEqual(len(check(source)), 1, check(source))

    def test_a_helper_inherited_in_the_same_file_is_not_blamed(self):
        source = '''
import unittest


class Base(unittest.TestCase):
    def helper(self):
        return 1


class Sub(Base):
    def test_uses_inherited(self):
        self.assertEqual(self.helper(), 1)
'''
        self.assertEqual(check(source), [])

    def test_a_nested_class_does_not_borrow_the_test_self(self):
        # `self.send_response` inside a nested handler belongs to the handler.
        # Blaming the test case for it would make the checker cry wolf on every
        # test that defines one -- `ops/test_github_deploy.py:1310` is exactly
        # this shape, with a real `http.server.BaseHTTPRequestHandler` subclass.
        source = '''
import unittest


class Nested(unittest.TestCase):
    def test_nested_handler(self):
        import http.server

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
'''
        self.assertEqual(check(source), [])

    def test_a_self_call_in_a_nested_function_still_counts(self):
        # A nested function keeps the outer `self`, unlike a nested class.
        source = '''
import unittest


class NestedFunction(unittest.TestCase):
    def test_calls_through_a_local_function(self):
        def call():
            return self.missing_helper()
        self.assertEqual(call(), 1)
'''
        self.assertEqual(len(check(source)), 1, check(source))

    def test_a_file_that_does_not_parse_is_an_error_not_a_pass(self):
        problems = check('class Broken(unittest.TestCase:\n')
        self.assertEqual(len(problems), 1, problems)
        self.assertIn('does not parse', problems[0])


class CommandLineTests(unittest.TestCase):
    def run_checker(self, *args):
        return subprocess.run(
            [sys.executable, '-I', str(SCRIPT), *args],
            capture_output=True, text=True,
        )

    def test_the_ops_tree_is_clean(self):
        root = SCRIPT.parent.parent.parent
        completed = self.run_checker(str(root / 'ops'))
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)

    def test_a_missing_path_fails_instead_of_passing_quietly(self):
        # A typo in the CI argument must not turn the gate into a no-op.
        completed = self.run_checker('/nonexistent/path/to/ops')
        self.assertEqual(completed.returncode, 1, completed.stdout)

    def test_no_arguments_fails(self):
        self.assertEqual(self.run_checker().returncode, 1)


if __name__ == '__main__':
    unittest.main()
