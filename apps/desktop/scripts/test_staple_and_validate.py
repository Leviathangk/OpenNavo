import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "staple", Path(__file__).with_name("staple-and-validate.py")
)
staple = importlib.util.module_from_spec(spec)
spec.loader.exec_module(staple)


def result(code=0, output=""):
    return subprocess.CompletedProcess(["xcrun", "stapler"], code, output, "")


class StapleTests(unittest.TestCase):
    @patch.object(staple.time, "sleep")
    @patch.object(staple.subprocess, "run")
    def test_recovers_cloudkit_disconnect(self, run, sleep):
        run.side_effect = [result(), result(68, "NSURLErrorDomain Code=-1005"), result(), result()]
        staple.staple_and_validate("Example.app")
        self.assertEqual([call.args[0][2] for call in run.call_args_list], ["staple", "validate", "staple", "validate"])
        sleep.assert_called_once_with(15)

    @patch.object(staple.time, "sleep")
    @patch.object(staple.subprocess, "run")
    def test_restaples_missing_ticket(self, run, sleep):
        run.side_effect = [result(), result(65, "Example.app does not have a ticket stapled to it."), result(), result()]
        staple.staple_and_validate("Example.app")
        self.assertEqual(run.call_count, 4)
        sleep.assert_called_once_with(15)

    @patch.object(staple.time, "sleep")
    @patch.object(staple.subprocess, "run")
    def test_network_failure_remains_fatal_after_limit(self, run, sleep):
        run.return_value = result(68, "CloudKit connection unavailable")
        with self.assertRaises(subprocess.CalledProcessError):
            staple.staple_and_validate("Example.dmg")
        self.assertEqual(run.call_count, 5)
        self.assertEqual(sleep.call_count, 4)

    @patch.object(staple.time, "sleep")
    @patch.object(staple.subprocess, "run")
    def test_invalid_ticket_is_not_retried(self, run, sleep):
        run.side_effect = [result(), result(65, "Ticket validation failed")]
        with self.assertRaises(subprocess.CalledProcessError):
            staple.staple_and_validate("Example.app")
        self.assertEqual(run.call_count, 2)
        sleep.assert_not_called()


if __name__ == "__main__":
    unittest.main()
