#!/usr/bin/env python3
"""Verify, install and start the deployment receiver.

This file is the trust root of the deployment channel: it is the one program on
the host that a deployment may not replace, because it decides which revision of
ops/github-deploy.py runs next. It therefore stays small, and it never holds the
receiver's lock across the handover -- the receiver keeps that logic unchanged.

The receiver reads its payload from standard input. This program must read that
payload first to learn which revision to install, so it hands the bytes on
through a pipe and never through a temporary file: the payload carries the
registry token, and writing it out would leave the token on the host's disk.
"""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.request

CONFIG_PATH = Path('/etc/opennavo/github-deploy.json')
REMOTE = 'https://raw.githubusercontent.com/opennavo/OpenNavo/{revision}/ops/github-deploy.py'
REVISION = re.compile(r'[0-9a-f]{40}\Z')
CHECKSUM = re.compile(r'[0-9a-f]{64}\Z')
# Mirrors the receiver's own stdin bound so a payload cannot grow here unnoticed.
MAX_PAYLOAD = 80 * 1024 * 1024
RETENTION = 5
# How long a receiver may take to report that it started, and how long it may run.
# The whole budget must fit inside the SSH client's timeout in
# .github/scripts/send-deployment.py: the SSH timeout runs from process start, while
# run()'s starts when run() is called, so the pre-run steps count against it too.
# PRE_RUN_LIMIT sums the worst case of every step before run(): the fetch, the two
# check steps and the probe. Keep PRE_RUN_LIMIT + RUN_TIMEOUT below the SSH timeout,
# or the SSH client gives up first: the rollback still happens, but the operator sees
# a bare SSH timeout instead of the message naming the receiver that was restored.
# REPORT_MARGIN is the room left for that last part -- killing the receiver, restoring
# the pointer and printing the message -- which no timeout above covers. Leave it out
# and the session can die in the middle of the sentence that explains what happened.
# Recent successful deploys took 2-8 minutes, so 900s is still about twice the
# observed worst case. test_the_run_timeout_leaves_room_under_the_ssh_timeout
# recomputes this from the source of both files.
FETCH_TIMEOUT = 30
CHECK_TIMEOUT = 120
START_TIMEOUT = 120
RUN_TIMEOUT = 900
REPORT_MARGIN = 60
PRE_RUN_LIMIT = FETCH_TIMEOUT + 2 * CHECK_TIMEOUT + START_TIMEOUT
BUDGET_LIMIT = PRE_RUN_LIMIT + RUN_TIMEOUT + REPORT_MARGIN
# The only exit statuses the receiver produces: 0 when the deployment succeeded and
# 1 when it failed. Anything else means the interpreter or the import itself failed,
# which is the one case where the receiver cannot repair itself.
STARTED, DEPLOY_FAILED = 0, 1
# The receiver answers --probe itself with this when another deployment holds the lock.
# Kept in step with PROBE_BUSY in ops/github-deploy.py.
PROBE_BUSY = 2
# Never produced by the receiver: the staged file was missing (the child never ran)
# or the receiver did not finish in time.
EXEC_FAILED, TIMED_OUT = -1, -2


def fail(message):
    print('Receiver bootstrap failed: ' + message, file=sys.stderr)
    sys.exit(1)


def revision_of(payload):
    revision = payload.get('receiver', '')
    if not isinstance(revision, str) or not REVISION.fullmatch(revision):
        # The revision becomes a directory name, so anything but 40 lowercase hex
        # characters could step outside the receiver directory.
        fail('the payload does not name a full 40-character receiver revision')
    return revision


def fetch(revision):
    url = REMOTE.format(revision=revision)
    try:
        with urllib.request.urlopen(url, timeout=FETCH_TIMEOUT) as response:
            data = response.read(MAX_PAYLOAD + 1)
    except urllib.error.URLError as error:
        # Named explicitly: an unreachable host looks like a broken receiver otherwise.
        fail(f'cannot fetch the receiver for revision {revision} from GitHub ({error.reason}); '
             'the host may be unable to reach raw.githubusercontent.com')
    if len(data) > MAX_PAYLOAD:
        fail(f'the receiver for revision {revision} is larger than the limit')
    return data


def stage(directory, revision, data):
    """Write the receiver for one revision; content-addressed, so a re-run rewrites identical bytes."""
    target = directory / revision
    target.mkdir(parents=True, exist_ok=True)
    temporary = target / ('.github-deploy.py.%d' % os.getpid())
    temporary.write_bytes(data)
    os.replace(temporary, target / 'github-deploy.py')
    installed = (target / 'github-deploy.py').read_bytes()
    if hashlib.sha256(installed).hexdigest() != hashlib.sha256(data).hexdigest():
        fail(f'the receiver written for revision {revision} does not match the verified bytes')
    return target / 'github-deploy.py'


def check(receiver):
    """Compile and self-check the staged receiver before it can become current.

    Both run before the current pointer moves, so a receiver that cannot even
    load never replaces one that works.

    Standard input is closed: neither step takes a payload, and a receiver that
    tried to read one here would block until CHECK_TIMEOUT instead of failing.
    """
    steps = ((['-I', '-m', 'py_compile', str(receiver)], 'compile'),
             ([str(receiver), '--self-check'], 'self-check'))
    for arguments, name in steps:
        try:
            subprocess.run([sys.executable, *arguments], check=True, capture_output=True, text=True,
                           stdin=subprocess.DEVNULL, timeout=CHECK_TIMEOUT)
        except subprocess.CalledProcessError as error:
            detail = (error.stderr or error.stdout or '').strip().splitlines()
            reason = detail[-1] if detail else 'exit %d' % error.returncode
            fail('the receiver failed its %s step, so the current receiver was left in place: %s' % (name, reason))
        except subprocess.TimeoutExpired:
            fail('the receiver %s step timed out, so the current receiver was left in place' % name)


def point(path, target):
    """Replace one symlink atomically, so no reader ever sees it missing."""
    temporary = path.with_name('.%s.%d' % (path.name, os.getpid()))
    try:
        temporary.unlink()
    except FileNotFoundError:
        pass
    os.symlink(target, temporary)
    os.replace(temporary, path)


def switch(directory, revision):
    current, previous = directory / 'current', directory / 'previous'
    if current.is_symlink():
        replaced = os.readlink(current)
        if replaced != revision:
            point(previous, replaced)
    point(current, revision)


def rollback(directory):
    """Point current back at previous; used when the receiver just installed cannot start."""
    current, previous = directory / 'current', directory / 'previous'
    if not previous.is_symlink():
        return False
    point(current, os.readlink(previous))
    return True


def prune(directory):
    """Keep the newest versions, never the two current and previous point at.

    Path.is_dir() follows symlinks, so the protected versions are resolved first:
    filtering by name alone would delete the very directory current points at.
    """
    protected = {Path(os.readlink(directory / name)).name
                 for name in ('current', 'previous') if (directory / name).is_symlink()}
    versions = [entry for entry in directory.iterdir()
                if entry.is_dir() and not entry.is_symlink() and REVISION.fullmatch(entry.name)]
    versions.sort(key=lambda entry: entry.stat().st_mtime, reverse=True)
    for entry in versions[RETENTION:]:
        if entry.name in protected:
            continue
        for child in sorted(entry.iterdir()):
            child.unlink()
        entry.rmdir()


def run(receiver, payload, timeout=None, arguments=()):
    """Start the receiver with the original payload on its standard input.

    A pipe, not a temporary file: the payload carries the registry token. The
    receiver may exit before reading it (a busy lock does that), which closes the
    pipe early; that is its exit status to report, not a bootstrap failure.

    Returns the exit status, or TIMED_OUT when the receiver did not finish. The
    caller decides what a timeout means; a deployment that is merely slow is not
    a receiver that cannot start.
    """
    if not receiver.is_file():
        return EXEC_FAILED
    process = subprocess.Popen([sys.executable, str(receiver), *arguments], stdin=subprocess.PIPE)
    try:
        process.communicate(payload, timeout=timeout)
    except BrokenPipeError:
        process.wait()
    except subprocess.TimeoutExpired:
        # Leaving it running would hold the receiver's own lock forever, and the SSH
        # session above ends at its own timeout anyway, leaving nobody able to see what
        # it does. RUN_TIMEOUT is kept below that SSH timeout so this message is read.
        process.kill()
        process.wait()
        return TIMED_OUT
    return process.returncode


def probe(receiver, timeout):
    """Decide whether the receiver can start, before it becomes current.

    The receiver answers this on its own, with --probe: it imports itself, reads the
    host configuration and takes its lock, and does nothing else. Running main() with
    an empty payload instead would be a worse question and a real edit — main() sweeps
    stale temporary files before it reads standard input.

    Returns STARTED, PROBE_BUSY, or -1 for anything else. `busy` has to be its own
    answer: the receiver's main() exits 1 both for a lock it cannot take and for every
    other failure, so a success/failure pair cannot tell a running deployment from a
    broken receiver.
    """
    status = run(receiver, b'', timeout, ['--probe'])
    return status if status in (STARTED, PROBE_BUSY) else EXEC_FAILED


def install(config, payload):
    """Fetch, verify, check and switch; returns the receiver that should run."""
    revision = revision_of(payload)
    expected = payload.get('receiver_sha256', '')
    if not isinstance(expected, str) or not CHECKSUM.fullmatch(expected):
        fail('the payload does not carry a full 64-character receiver checksum')
    data = fetch(revision)
    if hashlib.sha256(data).hexdigest() != expected:
        fail('the receiver fetched for revision %s does not match the checksum in the payload' % revision)
    directory = Path(config['root']) / 'receiver'
    directory.mkdir(parents=True, exist_ok=True)
    staged = stage(directory, revision, data)
    check(staged)
    status = probe(staged, START_TIMEOUT)
    if status == PROBE_BUSY:
        # Another deployment is running. It is not this revision's problem, and the
        # switch below would wait for its lock anyway, so say so and stop.
        fail('another deployment holds the receiver lock, so revision %s was not installed' % revision)
    if status != STARTED:
        # Switching first and hoping would install a receiver that cannot start, and
        # the next deployment would run it again.
        fail('the receiver for revision %s cannot start, so the current receiver was left in place' % revision)
    # Only the switch and the prune run under the lock: a concurrent deployment
    # must not see current between two revisions, and the receiver takes the
    # lock itself once it starts.
    with (Path(config['root']) / 'deploy/github-deploy.lock').open('w') as guard:
        fcntl.flock(guard, fcntl.LOCK_EX)
        switch(directory, revision)
        prune(directory)
    print('receiver: installed ' + revision, flush=True)
    return directory / 'current/github-deploy.py'


def main():
    os.umask(0o077)
    try:
        config = json.loads(CONFIG_PATH.read_text())
    except (OSError, ValueError) as error:
        fail('cannot read the host configuration at %s: %s' % (CONFIG_PATH, type(error).__name__))
    payload = sys.stdin.buffer.read(MAX_PAYLOAD + 1)
    if len(payload) > MAX_PAYLOAD:
        fail('the deployment payload exceeds the limit')
    message = json.loads(payload)
    if 'receiver' not in message:
        # Nothing to install: a desktop publish, or a sender older than this file.
        receiver = Path(config['root']) / 'receiver/current/github-deploy.py'
        if not receiver.exists():
            fail('no receiver is installed and this payload installs none')
        sys.exit(run(receiver, payload))
    receiver = install(config, message)
    status = run(receiver, payload, RUN_TIMEOUT)
    if status != STARTED:
        # The new receiver ran and failed, or never finished. Point current back at the
        # one that was working. The next deployment installs its own revision anyway,
        # but a desktop publish carries no receiver and would otherwise keep running a
        # receiver that cannot deploy. Either way say which one was restored: a silent
        # rollback would look like a deployment that merely failed.
        previous = Path(config['root']) / 'receiver' / 'previous'
        restored = os.readlink(previous) if previous.is_symlink() else None
        rollback(Path(config['root']) / 'receiver')
        if status == TIMED_OUT:
            reason = 'did not finish within %ds' % RUN_TIMEOUT
        elif status == EXEC_FAILED:
            reason = 'could not start'
        else:
            # The receiver exited on its own with a non-zero status, which is its own
            # "this deployment failed". An operator paged at night must read that first:
            # otherwise they go looking for a broken receiver when the deployment is
            # what failed, and the log above is where the actual reason is.
            reason = ('exited with status %d: the deployment failed on its own terms '
                      '(see the log above), not the receiver' % status)
        outcome = ('the previous receiver %s was restored, so a desktop publish does not '
                   'keep running this one' % restored if restored else
                   'there was no previous receiver to restore')
        fail('the receiver for this deployment %s; %s; the deployment did not take effect'
             % (reason, outcome))
    sys.exit(status)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # Never echo the payload: it carries the registry token.
        print('Receiver bootstrap failed: ' + type(error).__name__, file=sys.stderr)
        sys.exit(1)
