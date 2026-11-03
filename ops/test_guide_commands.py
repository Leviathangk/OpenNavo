"""The GUIDE shell checker must fail on the defects it exists to catch.

`ops/check_guide_commands.py` is the only thing standing between a broken command
and an operator running it on the production host, and until this file nothing
tested the checker itself. It shipped a hole: `<worker-container>` in a command
position was treated as a legitimate placeholder and exempted, so the four
`docker exec -it <worker-container> ...` commands in GUIDE.md were reported as
fine while the shell truncates each one to `docker exec -it`.

Each test writes a GUIDE body to a temporary directory, runs the checker against
it as a subprocess, and reads the real exit code. Exit codes are read from the
subprocess directly and never through a pipe's tail: this repository has already
had three cases where `cmd | grep ...; echo $?` reported grep's status instead of
the command's.
"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CHECKER = Path(__file__).with_name('check_guide_commands.py')


def run_checker(guide_body: str) -> subprocess.CompletedProcess[str]:
    """Run the checker over `guide_body` as if it were ops/GUIDE.md.

    The checker resolves GUIDE from its own path, so the script is copied into a
    temporary tree next to the GUIDE it should read. That keeps the real
    ops/GUIDE.md out of the test: a test that only reads the shipped file cannot
    tell a checker that works from one that never looks.
    """
    with tempfile.TemporaryDirectory(prefix='guide-checker-test-') as directory:
        root = Path(directory)
        (root / 'ops').mkdir()
        (root / 'ops' / 'GUIDE.md').write_text(guide_body)
        script = root / 'ops' / 'check_guide_commands.py'
        script.write_text(CHECKER.read_text())
        return subprocess.run(
            [sys.executable, '-I', str(script)],
            capture_output=True,
            text=True,
            timeout=120,
        )


class AngleBracketPlaceholderTests(unittest.TestCase):
    """A `<name>` in a shell block is an input redirect, never a placeholder."""

    def test_a_placeholder_in_command_position_is_rejected(self) -> None:
        """T1: `<worker-container>` in a command position must not pass."""
        result = run_checker(
            'Replace the name below.\n\n'
            '```sh\n'
            'docker exec -it <worker-container> /opennavo assets-backfill --dry-run\n'
            '```\n'
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('<worker-container>', result.stderr)
        self.assertIn('input redirect', result.stderr)

    def test_a_placeholder_in_argument_position_is_rejected(self) -> None:
        """T2: `cp a-<TS> b` truncates the command the same way, so it fails too."""
        result = run_checker(
            '```sh\n'
            'cp -a ~/.ssh/authorized_keys.bak-<WINDOW_TIMESTAMP> ~/.ssh/authorized_keys\n'
            '```\n'
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('<WINDOW_TIMESTAMP>', result.stderr)

    def test_a_quoted_variable_is_accepted(self) -> None:
        """The fix this check recommends must not itself be rejected."""
        result = run_checker(
            '```sh\n'
            'WORKER=$(docker ps --filter label=com.docker.compose.service=worker --format "{{.Names}}")\n'
            '[ -n "$WORKER" ] || { echo "no worker container found"; exit 1; }\n'
            'docker exec -it "$WORKER" /opennavo assets-backfill --dry-run\n'
            '```\n'
        )
        self.assertEqual(result.returncode, 0, result.stderr)


class SharedTempPathTests(unittest.TestCase):
    """A fixed /tmp name is shared by two concurrent runs."""

    def test_a_fixed_tmp_path_is_rejected(self) -> None:
        result = run_checker(
            '```sh\n'
            'OUT=$(lvs 2>/tmp/lvs.err)\n'
            '```\n'
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('/tmp/lvs.err', result.stderr)

    def test_mktemp_on_the_same_line_does_not_exempt_a_fixed_path(self) -> None:
        """T3: `mktemp` used elsewhere on the line must not excuse a fixed name.

        The checker originally skipped any line containing the word `mktemp`, so
        this line passed while still sharing /tmp/shared.err between runs.
        """
        result = run_checker(
            '```sh\n'
            'tmpf=$(mktemp); cat /tmp/shared.err > "$tmpf"\n'
            '```\n'
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('/tmp/shared.err', result.stderr)

    def test_a_mktemp_template_is_accepted(self) -> None:
        """A template is not a fixed path: each run resolves it differently.

        This is the idiom the checker tells the reader to use, so a checker that
        rejects it would send the reader to a path with no correct answer.
        """
        result = run_checker(
            '```sh\n'
            'ERR=$(mktemp "${TMPDIR:-/tmp}/lvs.XXXXXX")\n'
            'OUT=$(lvs --noheadings 2>"$ERR")\n'
            '```\n'
        )
        self.assertEqual(result.returncode, 0, result.stderr)


class ShippedGuideTests(unittest.TestCase):
    """T4: the GUIDE this repository actually ships must pass."""

    def test_the_shipped_guide_passes(self) -> None:
        shipped = CHECKER.parent / 'GUIDE.md'
        result = subprocess.run(
            [sys.executable, '-I', str(CHECKER)],
            capture_output=True,
            text=True,
            timeout=120,
        )
        self.assertEqual(
            result.returncode,
            0,
            f'{shipped} must pass its own check:\n{result.stderr}',
        )


class SelfTestTests(unittest.TestCase):
    """The checker's own reporting must not claim a check it did not run."""

    def test_a_file_with_no_shell_blocks_fails_instead_of_passing(self) -> None:
        """Reporting 'all fine' over zero checked blocks is the worst answer."""
        result = run_checker('# Guidance\n\nNo commands here.\n')
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('no shell blocks', result.stderr)

    def test_the_summary_line_matches_the_failure_count(self) -> None:
        """The count in the last line equals the problems actually reported.

        A summary that disagrees with its own body is read as the authoritative
        number by whoever skims the output, and this check has no other place to
        state a total.
        """
        result = run_checker(
            '```sh\n'
            'a 2>/tmp/one.err\n'
            'b <placeholder-here>\n'
            '```\n'
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        reported = sum(
            1 for line in result.stderr.splitlines() if line.startswith('ops/GUIDE.md:')
        )
        self.assertIn(f'{reported} problem(s)', result.stderr)
        self.assertGreaterEqual(reported, 2, 'both defects should be reported')


if __name__ == '__main__':
    unittest.main()
