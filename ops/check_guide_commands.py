#!/usr/bin/env python3
"""Check that the shell blocks in ops/GUIDE.md are at least runnable.

`DOC-CMD-UNVALIDATED`: nothing in this repository executes the commands in
ops/GUIDE.md, so a command that cannot run costs someone a debugging session on
the production host. This script closes the mechanical part of that gap; it does
not prove a command produces the right answer, only that the shell can parse it.

Two checks, both of which caught real defects in this file:

1. Syntax. Every ```sh / ```bash block is extracted and passed to `sh -n`. A
   block that does not parse is a command the reader will hit an error on.

2. Shared temporary paths. A block that redirects to a fixed /tmp name lets two
   runs overwrite each other's stderr, so one reads another run's error as its
   own result. Reproduced with two concurrent runs of the block that used
   /tmp/lvs.err: 5/5 rounds both processes read the same file. This check
   rejects a literal /tmp/<name> and requires mktemp.

3. Redirects into a placeholder. `2><WINDOW_TIMESTAMP>` passes `sh -n` - `<`
   starts an input redirect - and fails only when run, with the script still
   exiting 0 because the failing command is a bare assignment. Checked
   separately because `sh -n` is not enough on its own; measured:
   `sh -n` exit 0, run exit 0, `syntax error near unexpected token '<'`.

What this deliberately does not check: whether a command produces the correct
answer, whether its flags exist on the host's version of the tool, or whether
the block runs at all against a real system. Those need a host.

Exit codes: 0 when every block parses and none uses a shared temp path,
1 otherwise. Wired into ops/lint.py so `deploy-lint` runs it in CI.
"""

from __future__ import annotations

import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GUIDE = ROOT / "ops" / "GUIDE.md"

# A fence may be indented (a block nested inside a list item), so the opening
# marker allows leading spaces and the body is de-indented by the same amount.
# Without this an indented block is skipped silently, which is worse than a
# failure: the block count drops and the summary still says everything is fine.
BLOCK = re.compile(r"^([ \t]*)```(?:sh|bash)\n(.*?)^\1```$", re.S | re.M)

# A literal path under /tmp that two runs would both pick. Any extension, not just
# .err, because the defect is the fixed name and not the suffix.
#
# A path containing a run of six or more `X` is a mktemp template and is not
# fixed: the unique part is exactly what the two runs differ in. That test runs in
# the checker loop, against the match, and not as a lookbehind on this pattern.
#
# A trailing `(?<!XXXXXX)` here does not express the rule. Measured on
# `${TMPDIR:-/tmp}/lvs.XXXXXX` with the trailing `\b` the old pattern had:
#
#   ...(?<!XXXXXX)      -> `${TMPDIR:-/tmp}/lvs.XXXXX`   one X short, still "fixed"
#   ...(?<!XXXXXX)\b    -> `${TMPDIR:-/tmp}/lvs.`        reports a fixed path on the
#                                                        mktemp idiom this check
#                                                        tells the reader to use
#
# The name class is greedy and stops where the lookbehind lets it, so the match
# is truncated rather than rejected. `\b` sits in a zero-width position where it
# can satisfy the lookbehind by landing before the X run. Testing the match for a
# six-X run after the fact has neither failure mode.
FIXED_TMP = re.compile(
    r"(?<!mktemp )(?<!mktemp -d )"
    r"(?:/tmp/|\$\{TMPDIR:-/tmp\}/)[A-Za-z0-9_.-]+"
)
# A `<name>` token anywhere a shell would read it as a redirect. This covers the
# command position too, which an earlier version of this check exempted as a
# "legitimate substitute your own value placeholder". The shell has no such
# concept: `<worker-container>` in a command position is an input redirect, and
# the command after it is lost. Measured with a `docker` stub:
#
#   docker exec -it <worker-container> /opennavo assets-backfill --dry-run --limit 50
#   sh -n  -> 0
#   run    -> exit 1, "worker-container: No such file or directory"
#             argv was `docker got 2 args: [exec] [-it]`, so the container name
#             and all three flags never reached the command.
#
# Requiring the value to come from a variable (`"$WORKER"`) keeps the commands
# copy-pasteable, which is the only thing that makes an angle bracket safe.
PLACEHOLDER = re.compile(r"(?<![<\w])<[A-Za-z_][A-Za-z0-9_.-]*>")

# A path whose variable part is an `XXXXXX` run is a mktemp template, which each
# run resolves to a different name, so it is not shared. Returns True for those.
TEMPLATE = re.compile(r"X{6,}")


def shell_blocks(text: str) -> list[tuple[int, str]]:
    """Every ```sh / ```bash block, as (offset of the fence line, body).

    The body is de-indented so a block nested in a list item is checked as the
    shell would see it, not with the markdown's leading spaces.
    """
    blocks: list[tuple[int, str]] = []
    for match in BLOCK.finditer(text):
        indent = match.group(1)
        body = match.group(2)
        if indent:
            body = "".join(
                line[len(indent):] if line.startswith(indent) else line.lstrip()
                for line in body.splitlines(keepends=True)
            )
        blocks.append((match.start(), body))
    return blocks


def line_of(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def check_syntax(body: str) -> str | None:
    with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
        handle.write(body)
        path = handle.name
    try:
        result = subprocess.run(
            ["sh", "-n", path], capture_output=True, text=True, timeout=30
        )
    finally:
        Path(path).unlink(missing_ok=True)
    if result.returncode != 0:
        return (result.stderr or result.stdout).strip().replace(path, "<block>")
    return None


def main() -> int:
    if not GUIDE.exists():
        print(f"missing {GUIDE}", file=sys.stderr)
        return 1
    text = GUIDE.read_text()
    blocks = shell_blocks(text)
    # A fence this script cannot parse is a block nobody checks, and reporting
    # "all fine" over it is worse than reporting nothing. Compare against the
    # fences the file actually has, not against the ones the pattern found.
    declared = len(re.findall(r"^[ \t]*```(?:sh|bash)\s*$", text, re.M))
    if not blocks:
        print("no shell blocks found in ops/GUIDE.md", file=sys.stderr)
        return 1
    if declared != len(blocks):
        print(
            f"ops/GUIDE.md: {declared} shell fence(s) but {len(blocks)} parsed - "
            f"a block is being skipped, so its commands are unchecked. "
            f"The opening and closing fence must be indented the same amount.",
            file=sys.stderr,
        )
        return 1

    failures: list[str] = []
    for offset, body in blocks:
        block_line = line_of(text, offset)
        problem = check_syntax(body)
        if problem:
            failures.append(
                f"ops/GUIDE.md:{block_line}: shell block does not parse:\n{problem}"
            )
        # Report each distinct fixed path once, at its real line in the file.
        body_lines = body.split("\n")
        seen: set[str] = set()
        for index, line in enumerate(body_lines):
            if line.lstrip().startswith("#"):
                continue
            for match in FIXED_TMP.finditer(line):
                path = match.group(0)
                # A template each run resolves differently is not a shared path.
                if TEMPLATE.search(path):
                    continue
                if path in seen:
                    continue
                seen.add(path)
                real_line = block_line + 1 + index
                failures.append(
                    f"ops/GUIDE.md:{real_line}: {path} is a fixed path two runs "
                    f"would share; use mktemp \"${{TMPDIR:-/tmp}}/name.XXXXXX\".\n"
                    f"  Two concurrent runs then read each other's stderr, which is "
                    f"the answer this section exists to get right."
                )
            for match in PLACEHOLDER.finditer(line):
                real_line = block_line + 1 + index
                failures.append(
                    f"ops/GUIDE.md:{real_line}: {match.group(0)} is an angle-bracket "
                    f"placeholder in a shell block, where the shell reads it as an "
                    f"input redirect, not as a value to substitute.\n"
                    f"  Measured: `sh -n` exits 0, then the run loses every argument "
                    f"after the placeholder and fails on a missing file. Put the "
                    f"value in a variable and quote it: \"$WORKER\"."
                )

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        print(f"\n{len(failures)} problem(s) in {len(blocks)} shell block(s)", file=sys.stderr)
        return 1

    print(
        f"ops/GUIDE.md: {len(blocks)} shell block(s) parse, none use a shared /tmp "
        f"path, no redirect into a placeholder"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
