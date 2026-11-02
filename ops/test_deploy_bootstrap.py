""" Verify the receiver bootstrap without installing anything on a host.

Every test runs in a temporary directory with a temporary configuration, and
nothing here starts a deployment, reaches the network or touches /etc.
"""
import fcntl
import hashlib
import importlib.util
import io
import errno
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('deploy_bootstrap', Path(__file__).with_name('deploy-bootstrap.py'))
bootstrap = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(bootstrap)

REVISION = 'a' * 40
OTHER = 'b' * 40
THIRD = 'c' * 40
SENTINEL = 'registry-token-do-not-write-this-to-disk'

# Stands in for the receiver: reads the payload from stdin and reports what it
# got, so a test can prove the bytes crossed the handover unchanged. --probe and
# --self-check answer without reading stdin, like the real receiver. With an empty
# payload it then fails the same way main() does, so probe() still sees a start.
RECEIVER = '''\
import hashlib
import os
import sys
if '--self-check' in sys.argv or '--probe' in sys.argv:
    sys.exit(0)
raw = sys.stdin.buffer.read()
report = os.environ.get('HANDOVER_REPORT')
if report:
    with open(report, 'w') as stream:
        stream.write('%d %s' % (len(raw), hashlib.sha256(raw).hexdigest()))
if not raw:
    sys.exit(1)
sys.exit(int(os.environ.get('RECEIVER_STATUS', '0')))
'''
# Cannot answer --probe either: it dies the way the interpreter does when the file
# cannot be imported at all. This is the "cannot start" case, not a failed deployment,
# and it must be refused before current moves.
UNSTARTABLE = '''\
import sys
if '--self-check' in sys.argv:
    sys.exit(0)
sys.exit(3)
'''
# Answers --probe with 2: another deployment holds the lock. Busy, not broken.
BUSY = '''\
import sys
if '--self-check' in sys.argv:
    sys.exit(0)
if '--probe' in sys.argv:
    sys.exit(2)
sys.exit(0)
'''
BROKEN = '''\
import sys
if '--self-check' in sys.argv or '--probe' in sys.argv:
    print('configuration is missing key: example', file=sys.stderr)
    sys.exit(1)
sys.exit(0)
'''


def payload(revision=REVISION, data=RECEIVER.encode(), **extra):
    message = {'sha': revision, 'receiver': revision,
               'receiver_sha256': hashlib.sha256(data).hexdigest(),
               'images': {'server': 'x', 'web': 'x', 'mcp': 'x'},
               'registry_token': SENTINEL}
    message.update(extra)
    return json.dumps(message).encode()


class BootstrapTestCase(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name) / 'root'
        self.root.mkdir()
        (self.root / 'deploy').mkdir()
        self.config = Path(self.directory.name) / 'github-deploy.json'
        self.config.write_text(json.dumps({'root': str(self.root), 'project': 'opennavo'}))
        self.patch = patch.multiple(bootstrap, CONFIG_PATH=self.config)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.receiver = self.root / 'receiver'
        self.receiver.mkdir()
        (self.receiver / 'current').symlink_to('missing')

    def point_previous_at(self, revision):
        """Give the host a receiver to fall back to, without assuming a pointer file."""
        target = self.receiver / revision
        target.mkdir(parents=True, exist_ok=True)
        (target / 'github-deploy.py').write_bytes(RECEIVER.encode())
        previous = self.receiver / 'previous'
        if previous.is_symlink() or previous.exists():
            previous.unlink()
        previous.symlink_to(revision)

    def install_revision(self, revision, data=RECEIVER.encode()):
        """Put a version in place without going through the network."""
        target = self.receiver / revision
        target.mkdir(parents=True, exist_ok=True)
        (target / 'github-deploy.py').write_bytes(data)
        return target

    def run_main(self, message, receiver_status=0, stdout=None):
        """Run the bootstrap's entry point with the payload on its standard input.

        Returns the exit status it asks for. The entry point always leaves by
        SystemExit, so every caller here gets a number rather than an exception.
        """
        stream = io.BytesIO(message)
        environment = {'RECEIVER_STATUS': str(receiver_status)}
        if stdout is not None:
            environment['HANDOVER_REPORT'] = str(stdout)
        with patch.object(sys, 'stdin', type('Stdin', (), {'buffer': stream})()), \
                patch.object(sys, 'executable', sys.executable), \
                patch.dict(os.environ, environment), \
                patch.object(bootstrap, 'REMOTE', 'file://' + str(self.directory.name) + '/{revision}/receiver.py'):
            with self.assertRaises(SystemExit) as raised:
                bootstrap.main()
        return raised.exception.code

    def test_a_real_directory_called_current_is_named_not_overwritten(self):
        current = self.receiver / 'current'
        current.unlink()
        current.mkdir()
        with patch('sys.stderr', new_callable=io.StringIO) as stderr:
            with self.assertRaises(SystemExit) as raised:
                bootstrap.switch(self.receiver, REVISION)
        self.assertEqual(raised.exception.code, 1)
        self.assertIn('real directory', stderr.getvalue())
        self.assertTrue(current.is_dir(), 'the directory must be left exactly as it was')
        self.assertEqual(sorted(p.name for p in current.iterdir()), [])

    def test_a_publish_refuses_a_current_directory_with_an_old_receiver_inside(self):
        # A publish carries no receiver, so main() runs whatever current points at.
        # exists() answered yes for a real directory holding an old copy: the stale
        # file ran, exited 0, and the publish above read that as a deployment.
        current = self.receiver / 'current'
        current.unlink()
        current.mkdir()
        stale = current / 'github-deploy.py'
        stale.write_text(RECEIVER)
        stale.chmod(0o755)
        publish = json.dumps({'sha': REVISION, 'images': {'server': 'x'},
                              'registry_token': SENTINEL}).encode()
        with patch('sys.stderr', new_callable=io.StringIO) as stderr:
            self.assertEqual(self.run_main(publish), 1)
        self.assertIn('real directory', stderr.getvalue(),
                      'the publish must say what current is, not run it')
        self.assertEqual(stale.read_text(), RECEIVER, 'the stale copy must not run')

    def test_a_publish_still_runs_a_pointer_to_an_installed_receiver(self):
        # The positive side: a real pointer must not be caught by the refusal above.
        self.point_previous_at(REVISION)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(REVISION)
        publish = json.dumps({'sha': REVISION, 'images': {'server': 'x'},
                              'registry_token': SENTINEL}).encode()
        self.assertEqual(self.run_main(publish), 0)

    def test_a_pointer_to_a_revision_without_the_receiver_is_named(self):
        target = self.receiver / OTHER
        target.mkdir()
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        publish = json.dumps({'sha': REVISION, 'images': {'server': 'x'},
                              'registry_token': SENTINEL}).encode()
        with patch('sys.stderr', new_callable=io.StringIO) as stderr:
            self.assertEqual(self.run_main(publish), 1)
        self.assertIn('holds no github-deploy.py', stderr.getvalue())

    def serve(self, revision, data=RECEIVER.encode()):
        """Lay out what the download step would fetch for one revision."""
        directory = Path(self.directory.name) / revision
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'receiver.py').write_bytes(data)


class RevisionTests(unittest.TestCase):
    def test_only_a_full_lowercase_revision_is_accepted(self):
        self.assertEqual(bootstrap.revision_of({'receiver': REVISION}), REVISION)

    def test_anything_that_could_leave_the_receiver_directory_is_rejected(self):
        # The revision becomes a directory name, so a shorter or longer value, a
        # path, an uppercase digest or a missing key must all stop here.
        for value in ('', 'a' * 39, 'a' * 41, 'A' * 40, '../../etc', 'main', 'a' * 40 + '/..',
                      'a' * 40 + '\n', None, 123):
            with self.subTest(value=value):
                with self.assertRaises(SystemExit) as raised:
                    bootstrap.revision_of({'receiver': value})
                self.assertEqual(raised.exception.code, 1)

    def test_a_missing_receiver_key_is_rejected_only_when_installing(self):
        # main() treats an absent 'receiver' as "nothing to install", so the
        # stricter check belongs in revision_of() and must not leak into that path.
        self.assertEqual(bootstrap.revision_of({'receiver': OTHER}), OTHER)


class ChecksumTests(BootstrapTestCase):
    def test_the_fetched_receiver_must_match_the_payload_checksum(self):
        # The served bytes are a working receiver, so nothing downstream (compile or
        # self-check) can fail: only the checksum comparison can stop this, and a test
        # using broken bytes would pass for the wrong reason.
        self.serve(REVISION, RECEIVER.encode())
        bogus = hashlib.sha256(b'a receiver this deployment does not get').hexdigest()
        self.assertEqual(self.run_main(payload(receiver_sha256=bogus)), 1)
        self.assertFalse((self.receiver / REVISION / 'github-deploy.py').exists()
                         and (self.receiver / 'current').is_symlink(),
                         'a receiver with the wrong checksum must never become current')

    def test_a_checksum_that_is_not_a_full_digest_is_rejected_before_downloading(self):
        for value in ('', 'a' * 39, 'a' * 65, 'A' * 64, '../../etc'):
            with self.subTest(value=value):
                message = payload(receiver_sha256=value)
                with patch.object(bootstrap, 'fetch', side_effect=AssertionError('downloaded anyway')):
                    self.assertEqual(self.run_main(message), 1)

    def test_the_checksum_covers_the_bytes_that_will_actually_run(self):
        # Three-way comparison: the payload names a digest, the download must
        # produce it, and the file on disk must hash to the same value again.
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        installed = (self.receiver / REVISION / 'github-deploy.py').read_bytes()
        self.assertEqual(hashlib.sha256(installed).hexdigest(), hashlib.sha256(RECEIVER.encode()).hexdigest())

    def test_bytes_that_change_between_the_download_and_the_disk_are_caught(self):
        # Guards the third leg of the comparison: a write that silently stores
        # something else must be caught here. The substitution compiles and
        # self-checks cleanly, so compile and self-check cannot be what stops it:
        # only the re-hash of the written file can.
        self.serve(REVISION)
        innocent = b'import sys\nif "--self-check" in sys.argv:\n    sys.exit(0)\nsys.exit(0)\n'
        original = Path.write_bytes

        def replace(path, data):
            if path.name.startswith('.github-deploy.py'):
                return original(path, innocent)
            return original(path, data)

        with patch.object(Path, 'write_bytes', replace):
            self.assertEqual(self.run_main(payload()), 1)
        self.assertNotEqual(os.readlink(self.receiver / 'current'), REVISION)

    def test_an_unreachable_github_names_the_reason_instead_of_looking_broken(self):
        with patch.object(bootstrap.urllib.request, 'urlopen', side_effect=bootstrap.urllib.error.URLError('no route')):
            with self.assertRaises(SystemExit) as raised:
                bootstrap.fetch(REVISION)
        self.assertEqual(raised.exception.code, 1)


class VerificationOrderTests(BootstrapTestCase):
    def test_a_receiver_that_fails_its_self_check_never_becomes_current(self):
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION, BROKEN.encode())
        self.assertEqual(self.run_main(payload(data=BROKEN.encode())), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_a_receiver_that_does_not_compile_never_becomes_current(self):
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        broken = b'def f(:\n'
        self.serve(REVISION, broken)
        self.assertEqual(self.run_main(payload(data=broken)), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_the_compile_step_is_what_reports_unparsable_source(self):
        # Without py_compile the self-check step still exits 1, because the
        # interpreter raises SyntaxError on import. Both refuse the receiver, so a
        # test that only reads the exit status cannot tell which step ran. This one
        # reads the message: the compile step must be the one that fails first.
        self.serve(REVISION, b'def f(:\n')
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(payload(data=b'def f(:\n')), 1)
        written = ''.join(call[0][0] for call in stderr.write.call_args_list)
        self.assertIn('compile step', written)

    def test_a_regular_file_current_is_adopted_not_overwritten(self):
        # A host can have current as a regular file: that is how the receiver was
        # installed before receiver/ existed, and nothing migrates it. point() ends in
        # os.replace, which would delete it with no copy kept, and the next deployment
        # would then have no previous to roll back to. Adopt it into receiver/ first.
        old = b'#!/usr/bin/env python3\n# the receiver that was working\n'
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').write_bytes(old)
        bootstrap.switch(self.receiver, REVISION)
        self.assertTrue((self.receiver / 'current').is_symlink())
        self.assertEqual(os.readlink(self.receiver / 'current'), REVISION)
        self.assertTrue((self.receiver / 'previous').is_symlink())
        adopted = self.receiver / os.readlink(self.receiver / 'previous')
        self.assertEqual((adopted / 'github-deploy.py').read_bytes(), old,
                         'the pre-layout receiver was replaced without keeping a copy')
        self.assertEqual(bootstrap.rollback(self.receiver), True,
                         'a failed first deploy has nowhere to roll back to')

    def test_adopting_the_same_receiver_twice_keeps_one_copy(self):
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').write_bytes(RECEIVER.encode())
        bootstrap.switch(self.receiver, REVISION)
        first = os.readlink(self.receiver / 'previous')
        bootstrap.switch(self.receiver, REVISION)
        self.assertEqual(os.readlink(self.receiver / 'previous'), first)
        adopted = [entry for entry in self.receiver.iterdir() if entry.name.startswith('adopted-')]
        self.assertEqual(len(adopted), 1, 'a retried deployment left a second copy behind')

    def test_install_creates_and_tightens_deploy_before_probing(self):
        # The receiver's --probe takes the lock, so <root>/deploy must exist before the
        # probe runs or a first deploy dies on FileNotFoundError. It also has to be
        # tightened here: on a legacy host it exists at whatever mode the operator's
        # shell umask left, and the receiver's own main() never runs during an install.
        deploy = self.root / 'deploy'
        os.chmod(deploy, 0o755)
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        self.assertEqual(deploy.stat().st_mode & 0o777, 0o700,
                         'install() left the deployment directory at its inherited mode')

    def test_deploy_exists_before_the_probe_runs_not_just_after(self):
        # Ordering, not existence: the receiver's --probe opens the lock, so a deploy/
        # created after probe() is a deploy/ created too late. This records the state
        # at the moment probe() is entered, which is the only place the order shows.
        (self.root / 'deploy').rmdir()
        seen = []
        real = bootstrap.probe

        def recording(receiver, timeout):
            seen.append((self.root / 'deploy').is_dir())
            return real(receiver, timeout)

        self.serve(REVISION)
        with patch.object(bootstrap, 'probe', side_effect=recording):
            self.assertEqual(self.run_main(payload()), 0)
        self.assertEqual(seen, [True], 'the probe ran before <root>/deploy existed')

    def test_install_creates_deploy_when_the_host_has_none(self):
        (self.root / 'deploy').rmdir()
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        self.assertTrue((self.root / 'deploy').is_dir(),
                        'install() did not create the directory the probe needs')

    def test_the_previous_pointer_is_written_before_current_moves(self):
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        bootstrap.switch(self.receiver, REVISION)
        self.assertEqual(os.readlink(self.receiver / 'current'), REVISION)
        self.assertEqual(os.readlink(self.receiver / 'previous'), OTHER)


class DeployDirectoryTests(BootstrapTestCase):
    def test_a_pre_existing_directory_is_tightened(self):
        # Nothing in either program created deploy/; it was a precondition the operator
        # satisfied by hand, so its mode is whatever the creating shell's umask left.
        # mkdir(exist_ok=True) does not change the mode of a directory that already
        # exists (measured: 0755 stays 0755), so without the chmod a legacy host keeps
        # a group-traversable deploy/ forever and the fix silently does nothing.
        deploy = self.root / 'deploy'
        os.chmod(deploy, 0o775)
        self.assertEqual(deploy.stat().st_mode & 0o777, 0o775)
        returned = bootstrap.deploy_directory(self.root)
        self.assertEqual(returned, deploy)
        self.assertEqual(deploy.stat().st_mode & 0o777, 0o700,
                         'a deploy/ that already existed was left group-writable')

    def test_a_missing_directory_is_created_private(self):
        deploy = self.root / 'deploy'
        deploy.rmdir()
        old = os.umask(0o022)
        try:
            bootstrap.deploy_directory(self.root)
        finally:
            os.umask(old)
        self.assertTrue(deploy.is_dir())
        self.assertEqual(deploy.stat().st_mode & 0o777, 0o700,
                         'the umask decided the mode of a directory nothing created before')


class LockFileTests(BootstrapTestCase):
    def test_a_planted_symlink_is_refused_instead_of_truncated(self):
        victim = Path(self.directory.name) / 'victim.conf'
        victim.write_bytes(b'PRECIOUS CONFIG\n')
        lock = self.root / 'deploy/github-deploy.lock'
        lock.symlink_to(victim)
        with self.assertRaises(OSError) as raised:
            with bootstrap.open_lock(lock):
                pass
        self.assertEqual(raised.exception.errno, errno.ELOOP,
                         'the lock path was followed instead of refused')
        self.assertEqual(victim.read_bytes(), b'PRECIOUS CONFIG\n',
                         'following the lock symlink truncated the file it points at')
        self.assertTrue(lock.is_symlink())

    def test_a_lock_left_readable_by_an_earlier_version_is_repaired(self):
        # probe() does not run main(), so it never inherited os.umask(0o077) and could
        # create this file 0644. An existing inode keeps that mode through every later
        # open(), so only an explicit chmod closes it, and it has to run every time.
        lock = self.root / 'deploy/github-deploy.lock'
        lock.write_bytes(b'')
        os.chmod(lock, 0o644)
        with bootstrap.open_lock(lock):
            pass
        self.assertEqual(lock.stat().st_mode & 0o777, 0o600,
                         'a world-readable lock stayed readable')

    def test_the_lock_is_created_private_whatever_umask_is_inherited(self):
        lock = self.root / 'deploy/github-deploy.lock'
        lock.unlink(missing_ok=True)
        old = os.umask(0o000)
        try:
            with bootstrap.open_lock(lock):
                pass
        finally:
            os.umask(old)
        self.assertEqual(lock.stat().st_mode & 0o777, 0o600,
                         'the umask decided the mode of a freshly created lock')


class RetentionTests(BootstrapTestCase):
    def test_current_and_previous_are_never_pruned_even_when_oldest(self):
        # The natural implementation filters by name and calls is_dir(), which
        # follows the link: both spellings delete the directory current points
        # at. Only resolving the links first survives this test.
        for name in ('oldest', OTHER, THIRD):
            self.install_revision(name)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to('oldest')
        (self.receiver / 'previous').symlink_to(OTHER)
        for index, name in enumerate(('oldest', OTHER, THIRD)):
            os.utime(self.receiver / name, (index, index))
        for index in range(bootstrap.RETENTION + 2):
            self.install_revision('%040x' % (index + 100))
            os.utime(self.receiver / ('%040x' % (index + 100)), (1000 + index, 1000 + index))
        bootstrap.prune(self.receiver)
        self.assertTrue((self.receiver / 'current').exists())
        self.assertTrue((self.receiver / 'previous').exists())
        self.assertTrue((self.receiver / 'oldest').is_dir())
        self.assertTrue((self.receiver / OTHER).is_dir())

    def test_only_the_newest_versions_are_kept(self):
        for index in range(bootstrap.RETENTION + 3):
            name = '%040x' % (index + 100)
            self.install_revision(name)
            os.utime(self.receiver / name, (index, index))
        bootstrap.prune(self.receiver)
        remaining = {entry.name for entry in self.receiver.iterdir() if entry.is_dir() and not entry.is_symlink()}
        self.assertEqual(len(remaining), bootstrap.RETENTION)
        self.assertNotIn('%040x' % 100, remaining)
        self.assertIn('%040x' % (bootstrap.RETENTION + 2 + 100), remaining)

    def test_the_receiver_file_inside_a_version_is_removed_with_it(self):
        for index in range(bootstrap.RETENTION + 1):
            self.install_revision('%040x' % (index + 200))
            os.utime(self.receiver / ('%040x' % (index + 200)), (index, index))
        bootstrap.prune(self.receiver)
        self.assertFalse((self.receiver / ('%040x' % 200)).exists())


class HandoverTests(BootstrapTestCase):
    def test_the_payload_crosses_the_handover_unchanged(self):
        self.serve(REVISION)
        report = Path(self.directory.name) / 'handover.txt'
        message = payload()
        self.assertEqual(self.run_main(message, stdout=report), 0)
        size, digest = report.read_text().split()
        self.assertEqual(int(size), len(message))
        self.assertEqual(digest, hashlib.sha256(message).hexdigest())

    def test_the_payload_never_reaches_disk(self):
        # The payload carries the registry token. A temporary file would have
        # been removed by now, so the whole tree is scanned for the value too.
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        for path in Path(self.directory.name).rglob('*'):
            if path.is_file():
                self.assertNotIn(SENTINEL.encode(), path.read_bytes(), f'{path} contains the payload')

    def test_no_temporary_file_mechanism_is_used(self):
        # Guards the choice above: rewriting the handover through tempfile would
        # leave the token on the host, and must fail here rather than in review.
        self.serve(REVISION)
        for name in ('mkstemp', 'NamedTemporaryFile', 'TemporaryFile', 'mkdtemp', 'SpooledTemporaryFile'):
            patched = patch.object(tempfile, name, side_effect=AssertionError(f'tempfile.{name} was used'))
            patched.start()
            self.addCleanup(patched.stop)
        self.assertEqual(self.run_main(payload()), 0)

    def test_the_receiver_exit_status_is_reported(self):
        # A payload that installs nothing (a desktop publish) runs the installed receiver
        # and passes its exit status through: only an install can roll back.
        self.serve(REVISION)
        self.install_revision(REVISION)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(REVISION)
        message = json.dumps({'sha': REVISION, 'action': 'publish-desktop', 'registry_token': SENTINEL}).encode()
        self.assertEqual(self.run_main(message, receiver_status=3), 3)

    def test_a_payload_without_a_receiver_runs_the_installed_one(self):
        # Desktop publishes and older senders carry no receiver field; they must
        # not be forced to reinstall anything.
        self.install_revision(REVISION)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(REVISION)
        message = json.dumps({'sha': REVISION, 'action': 'publish-desktop', 'registry_token': SENTINEL}).encode()
        self.assertEqual(self.run_main(message), 0)

    def test_no_receiver_at_all_is_an_error_not_a_silent_success(self):
        message = json.dumps({'sha': REVISION, 'registry_token': SENTINEL}).encode()
        self.assertEqual(self.run_main(message), 1)

    def test_an_oversized_payload_is_refused_before_it_is_parsed(self):
        with patch.object(bootstrap, 'MAX_PAYLOAD', 64):
            self.assertEqual(self.run_main(b'x' * 128), 1)


class LockTests(BootstrapTestCase):
    """The switch must not be reachable by a second deployment while it runs.

    A second flock() from this process opens its own file description, and flock
    is per description, so it is blocked exactly as another deployment would be.
    That makes the lock observable without starting a second process.
    """

    def assert_locked(self):
        guard = (self.root / 'deploy/github-deploy.lock').open('w')
        self.addCleanup(guard.close)
        with self.assertRaises(BlockingIOError, msg='the deployment lock is not held here'):
            fcntl.flock(guard, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def assert_unlocked(self):
        guard = (self.root / 'deploy/github-deploy.lock').open('w')
        fcntl.flock(guard, fcntl.LOCK_EX | fcntl.LOCK_NB)
        fcntl.flock(guard, fcntl.LOCK_UN)
        guard.close()

    def hold(self, seen=None):
        """Run install with switch and prune replaced by probes of the lock."""
        original_switch, original_prune = bootstrap.switch, bootstrap.prune

        def switch(directory, revision):
            if seen is not None:
                seen.append('switch')
            self.assert_locked()
            return original_switch(directory, revision)

        def prune(directory):
            if seen is not None:
                seen.append('prune')
            self.assert_locked()
            return original_prune(directory)

        return patch.multiple(bootstrap, switch=switch, prune=prune)

    def test_the_switch_and_the_prune_run_inside_the_lock(self):
        self.serve(REVISION)
        with self.hold():
            self.assertEqual(self.run_main(payload()), 0)

    def test_the_receiver_starts_outside_the_lock(self):
        # The receiver takes the lock itself; holding it here would deadlock it.
        self.serve(REVISION)
        original = bootstrap.run
        observed = []

        def run(receiver, data, timeout=None, arguments=()):
            observed.append(Path(receiver).parent.name)
            self.assert_unlocked()
            return original(receiver, data, timeout, arguments)

        with self.hold(), patch.object(bootstrap, 'run', side_effect=run):
            self.assertEqual(self.run_main(payload()), 0)
        self.assertIn('current', observed, 'the receiver was never started')

    def test_the_lock_is_released_after_a_successful_install(self):
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        self.assert_unlocked()

    def test_the_lock_is_released_when_the_install_fails(self):
        self.serve(REVISION, BROKEN.encode())
        self.assertEqual(self.run_main(payload(data=BROKEN.encode())), 1)
        self.assert_unlocked()


class TimeoutTests(BootstrapTestCase):
    def test_a_receiver_that_hangs_is_killed_and_reported(self):
        self.serve(REVISION)
        hung = Path(self.directory.name) / 'hung.py'
        hung.write_text('import sys, time\nsys.stdin.buffer.read()\ntime.sleep(60)\n')
        started = time.monotonic()
        status = bootstrap.run(hung, payload(), 1)
        self.assertEqual(status, bootstrap.TIMED_OUT)
        self.assertLess(time.monotonic() - started, 30, 'the timeout did not stop the receiver')

    def test_a_receiver_that_finishes_before_the_deadline_reports_its_status(self):
        self.serve(REVISION)
        target = self.install_revision(REVISION)
        self.assertEqual(bootstrap.run(target / 'github-deploy.py', payload(), 30), 0)


class FailureTests(BootstrapTestCase):
    def test_a_receiver_that_cannot_exec_restores_the_previous_one(self):
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        (self.receiver / 'previous').symlink_to(OTHER)
        self.assertTrue(bootstrap.rollback(self.receiver))
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_rollback_reports_when_there_is_nothing_to_restore(self):
        (self.receiver / 'current').unlink()
        self.assertFalse(bootstrap.rollback(self.receiver))

    def test_a_first_deployment_that_fails_leaves_no_receiver_current(self):
        # The host has never deployed before: `previous` does not exist, so there is nothing
        # to roll back to. Returning quietly would leave `current` on the receiver that just
        # failed, and a desktop publish carries no receiver of its own: it runs
        # receiver/current/github-deploy.py, so every later publish would keep running the
        # broken one. The only safe state is "no receiver current", which the publish path
        # already refuses with a clear message.
        # setUp leaves a dangling `current`, which switch() would faithfully preserve as
        # `previous`. A host that has never deployed has no pointer at all.
        (self.receiver / 'current').unlink()
        self.assertFalse((self.receiver / 'previous').is_symlink())
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload(), receiver_status=1), 1)
        self.assertFalse((self.receiver / 'current').is_symlink(),
                         'a failed first deployment left its own receiver current')
        self.assertFalse((self.receiver / 'previous').is_symlink(),
                         'a rollback invented a previous receiver that never existed')

    def test_a_failed_first_deployment_says_no_receiver_is_installed(self):
        # The operator reads this line to know what the host is now running. "Restored the
        # previous receiver" would be a lie here, and "there was no previous receiver" alone
        # leaves the consequence unsaid.
        (self.receiver / 'current').unlink()
        self.serve(REVISION)
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(payload(), receiver_status=1), 1)
        text = ''.join(call.args[0] for call in stderr.write.call_args_list if call.args)
        self.assertIn('no receiver is current', text,
                      'the message does not say what the host is left running')

    def test_a_desktop_publish_after_a_failed_first_deployment_refuses_clearly(self):
        # The state rollback() now leaves behind: no `current` at all. A desktop publish
        # carries no receiver, so it has nothing to run and must say so, not crash on a
        # missing path or silently "succeed".
        (self.receiver / 'current').unlink()
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload(), receiver_status=1), 1)
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(json.dumps({'deployment': 'x'}).encode()), 1)
        text = ''.join(call.args[0] for call in stderr.write.call_args_list if call.args)
        self.assertIn('no receiver is installed', text,
                      'the publish path does not explain why it refused')

    def test_a_missing_configuration_stops_before_reading_the_payload(self):
        self.config.unlink()
        self.assertEqual(self.run_main(payload()), 1)

    def test_an_unstartable_receiver_is_refused_before_it_becomes_current(self):
        # Runs the real thing: no patch on run(), no synthetic EXEC_FAILED. The
        # receiver leaves with an exit status that only an interpreter failure
        # produces, so the probe has to catch it and current must never move.
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION, UNSTARTABLE.encode())
        self.assertEqual(self.run_main(payload(data=UNSTARTABLE.encode())), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_a_receiver_that_fails_the_deployment_is_rolled_back(self):
        # Exit 1 is the receiver's own "this deployment failed". The receiver repairs a
        # failure it can still reach (its own rollback), but a failure before that point
        # leaves the host on a receiver that cannot deploy: a desktop publish carries no
        # receiver, so it would keep running the broken one. Put the previous one back.
        # The next deployment installs its own revision, so this costs one install.
        self.install_revision(OTHER)
        self.point_previous_at(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload(), receiver_status=1), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_a_deployment_that_reports_success_leaves_the_new_receiver_current(self):
        # The other side of the rule above: a successful deployment must not roll back.
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION)
        self.assertEqual(self.run_main(payload()), 0)
        self.assertEqual(os.readlink(self.receiver / 'current'), REVISION)

    def test_a_real_receiver_that_fails_at_runtime_is_rolled_back_and_named(self):
        # The scenario end to end with a child process, not a patched run(): the receiver
        # passes --self-check and then exits 1, which is the most realistic way for a new
        # receiver to be broken. This test would pass on a stub that returned 127; it must
        # also pass on the real exit status, which is what it asserts here.
        self.install_revision(OTHER)
        self.point_previous_at(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        failing = (b"import sys\n"
                   b"if '--self-check' in sys.argv[1:] or '--probe' in sys.argv[1:]:\n"
                   b"    sys.exit(0)\nsys.exit(1)\n")
        self.serve(REVISION, failing)
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(payload(data=failing)), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)
        written = ''.join(call[0][0] for call in stderr.write.call_args_list)
        self.assertIn('exited with status 1', written)
        self.assertIn(OTHER, written)
        # The first thing an operator reads must be that the deployment failed, not
        # that the receiver is broken: the reason is in the log above, and pointing
        # them at the wrong component sends the investigation the wrong way.
        # The whole sentence, including "not the receiver": the half that says which
        # component is NOT at fault is the part an operator acts on. Testing only up to
        # "on its own terms" leaves it free to be deleted, which is how this survived.
        self.assertIn('exited with status 1: the deployment failed on its own terms '
                      '(see the log above), not the receiver', written,
                      'the message does not say the deployment, not the receiver, failed')
        # The reason the receiver is put back, not just that it was: a desktop publish
        # carries no receiver, so without this line a restored pointer looks optional.
        self.assertIn('a desktop publish does not', written,
                      'the message does not say why a failed deploy restores the receiver')

    def test_a_receiver_that_never_finishes_is_killed_and_rolled_back(self):
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION)

        def hang(receiver, data, timeout=None, arguments=()):
            if Path(receiver).parent.name == 'current':
                return bootstrap.TIMED_OUT
            return bootstrap.DEPLOY_FAILED

        with patch.object(bootstrap, 'run', side_effect=hang):
            self.assertEqual(self.run_main(payload()), 1)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_the_rollback_says_which_receiver_was_restored(self):
        # Nothing was restored yet: the probe refuses the receiver before the switch,
        # so current never moved and there is nothing to name. What the operator must
        # see is that the running receiver is unchanged and the deployment did nothing.
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION, UNSTARTABLE.encode())
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(payload(data=UNSTARTABLE.encode())), 1)
        written = ''.join(call[0][0] for call in stderr.write.call_args_list)
        self.assertIn('cannot start', written)
        self.assertIn('left in place', written)
        self.assertEqual(os.readlink(self.receiver / 'current'), OTHER)

    def test_a_restore_names_the_receiver_it_went_back_to(self):
        # The other restore path: the receiver passed the probe but never finished,
        # so current had already moved and must go back with a message naming it.
        self.install_revision(OTHER)
        (self.receiver / 'current').unlink()
        (self.receiver / 'current').symlink_to(OTHER)
        self.serve(REVISION)
        with patch.object(bootstrap, 'run', side_effect=[bootstrap.STARTED, bootstrap.TIMED_OUT]):
            with patch('sys.stderr') as stderr:
                self.assertEqual(self.run_main(payload()), 1)
        written = ''.join(call[0][0] for call in stderr.write.call_args_list)
        self.assertIn(OTHER, written)
        self.assertIn('did not take effect', written)

    def test_the_receiver_runs_under_the_run_timeout(self):
        # The deadline only protects anything if it is actually passed to the call that
        # starts the receiver. Removing the argument leaves every other test green.
        self.serve(REVISION)
        seen = []
        original = bootstrap.run

        def record(receiver, payload, timeout=None, arguments=()):
            if Path(receiver).parent.name == 'current':
                seen.append(timeout)
            return original(receiver, payload, timeout, arguments)

        with patch.object(bootstrap, 'run', side_effect=record):
            self.assertEqual(self.run_main(payload()), 0)
        self.assertEqual(seen, [bootstrap.RUN_TIMEOUT], 'the receiver ran without its deadline')

    def test_the_configuration_problem_is_named_as_such(self):
        # A new receiver may need keys the host does not have yet. The bootstrap must
        # report which step failed and quote the receiver, so that is not mistaken for
        # a corrupt download.
        self.serve(REVISION, BROKEN.encode())
        with patch('sys.stderr') as stderr:
            self.assertEqual(self.run_main(payload(data=BROKEN.encode())), 1)
        written = ''.join(call[0][0] for call in stderr.write.call_args_list)
        self.assertIn('self-check step', written)
        self.assertIn('configuration is missing key', written)


class LintTests(unittest.TestCase):
    def test_the_bootstrap_stays_small_enough_to_be_reviewed(self):
        # It is the trust root; growth is how it stops being reviewable.
        source = Path(__file__).with_name('deploy-bootstrap.py').read_text().splitlines()
        code = [line for line in source if line.strip() and not line.strip().startswith('#')]
        self.assertLess(len(code), 260, f'the bootstrap grew to {len(code)} non-blank lines')

    def test_the_self_check_runs_before_any_switch(self):
        # Both names appear inside their own definitions, so the comparison is
        # against the calls in main(), not the first mention in the file.
        source = Path(__file__).with_name('deploy-bootstrap.py').read_text()
        install = source.index('def install(')
        body = source[install:source.index('def main()')]
        self.assertLess(body.index('check(staged)'), body.index('switch(directory, revision)'))
        self.assertLess(body.index('probe(staged'), body.index('switch(directory, revision)'))
        # The switch itself stays inside the lock: a concurrent deployment must not
        # see current mid-move, and the receiver takes the same lock.
        switch = body.index('switch(directory, revision)')
        self.assertGreater(switch, body.index('fcntl.flock(guard, fcntl.LOCK_EX)'),
                           'the switch runs outside the deployment lock')
        # And the lock must be released before the receiver runs, or the receiver
        # deadlocks on the lock it takes itself. install() is where it is held.
        self.assertLess(source.index('fcntl.flock(guard, fcntl.LOCK_EX)'), source.index('def main()'))
        self.assertEqual(source.count('fcntl.flock('), 1, 'a second lock site can hold it across run()')

    def test_the_run_timeout_leaves_room_under_the_ssh_timeout(self):
        # Both files are read here because no runtime assertion can span them: the SSH
        # client enforces its own timeout, so bootstrap must finish first, print which
        # receiver it restored, and let that reach the workflow log. Equal timeouts make
        # the SSH failure the only visible one even though the rollback did happen.
        #
        # The SSH timeout runs from process start; run()'s starts when run() is called.
        # Every step before run() therefore counts against the same budget, so the
        # comparison is pre_run + RUN_TIMEOUT, not RUN_TIMEOUT alone. That is the
        # mistake that made 900 look safe next to 1500 while 1500 did not fit.
        #
        # REPORT_MARGIN is the part after the last timeout: killing the receiver,
        # restoring the pointer, printing which revision came back. A budget that
        # merely equals the SSH timeout leaves no room for it, so the operator sees
        # the timeout instead of the explanation.
        sender = (Path(__file__).parent.parent / '.github/scripts/send-deployment.py').read_text()
        ssh_timeout = int(re.search(r'\btimeout=(\d+)\)', sender).group(1))
        pre_run = bootstrap.FETCH_TIMEOUT + 2 * bootstrap.CHECK_TIMEOUT + bootstrap.START_TIMEOUT
        self.assertEqual(pre_run, bootstrap.PRE_RUN_LIMIT, 'PRE_RUN_LIMIT no longer sums its parts')
        self.assertLess(pre_run + bootstrap.RUN_TIMEOUT + bootstrap.REPORT_MARGIN, ssh_timeout,
                        'the bootstrap may be killed by the SSH client before it can report '
                        'which receiver it restored')
        self.assertGreater(bootstrap.REPORT_MARGIN, 0,
                           'a zero margin leaves no room to print what was restored')
        self.assertEqual(bootstrap.BUDGET_LIMIT, pre_run + bootstrap.RUN_TIMEOUT + bootstrap.REPORT_MARGIN,
                         'BUDGET_LIMIT no longer sums its parts')
        # And the probe must fit inside the run budget, not be counted beside it.
        self.assertLess(bootstrap.START_TIMEOUT, bootstrap.RUN_TIMEOUT)

    def test_the_probe_uses_the_side_effect_free_entry_point(self):
        # Running main() with an empty payload would answer the question and edit the
        # host: main() sweeps stale temporary files before it reads standard input.
        # The receiver therefore answers --probe itself, and the bootstrap must use it.
        source = Path(__file__).with_name('deploy-bootstrap.py').read_text()
        probe_body = source[source.index('def probe('):source.index('def install(')]
        self.assertIn("['--probe']", probe_body, 'the probe runs main() and edits the host')
        receiver = (Path(__file__).parent.parent / 'ops/github-deploy.py').read_text()
        self.assertIn("'--probe' in sys.argv[1:]", receiver, 'the receiver has no --probe entry point')
        # The probe must open the lock without truncating it and without following a
        # symlink planted at that path: open('w') empties the victim and open('a')
        # still creates a file wherever the link points. open_lock() is the one place
        # that carries O_NOFOLLOW, so the probe has to go through it.
        entry = receiver[receiver.index('def probe():'):receiver.index('def main():')]
        self.assertIn('open_lock(lock_path, os.O_RDONLY)', entry,
                      'the probe opens the lock path without O_NOFOLLOW')
        self.assertNotIn(".open('a')", entry, 'the probe opens the lock path directly')
        self.assertNotIn(".open('w')", entry, 'the probe truncates the lock file')
        self.assertNotIn('sweep_stale_temp_files', entry, 'the probe edits the host')
        helpers = receiver[receiver.index('def open_lock('):receiver.index('def probe():')]
        self.assertIn('os.O_NOFOLLOW', helpers, 'open_lock does not refuse a symlinked lock')
        self.assertIn('0o600', helpers, 'open_lock leaves the lock readable by the umask')
        self.assertIn('os.fchmod', helpers, 'open_lock does not repair a lock an earlier version left 0644')

    def test_the_receivers_busy_code_matches_the_bootstrap(self):
        # PROBE_BUSY is defined in both files and only the bootstrap's copy was pinned
        # by a test, so changing the receiver's copy alone raised no failure. A drifted
        # code folds "another deployment is running" into "the receiver cannot start",
        # and an ordinary concurrent deployment is then reported as a broken receiver.
        # Compare the two live values instead of the source text.
        spec = importlib.util.spec_from_file_location(
            'receiver_under_test', Path(__file__).parent.parent / 'ops/github-deploy.py')
        receiver = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(receiver)
        self.assertEqual(receiver.PROBE_BUSY, bootstrap.PROBE_BUSY,
                         'the receiver answers --probe with a different busy code than the bootstrap reads')

    def test_busy_is_its_own_probe_answer(self):
        # The receiver's main() exits 1 for a lock it cannot take and for every other
        # failure alike, so a success/failure pair cannot tell a running deployment
        # from a broken receiver. --probe answers 2 for busy, and the bootstrap must
        # report it as busy rather than as a receiver that cannot start.
        receiver = (Path(__file__).parent.parent / 'ops/github-deploy.py').read_text()
        entry = receiver[receiver.index('def probe():'):receiver.index('def main():')]
        self.assertIn('PROBE_BUSY', entry, 'the receiver does not give busy its own exit code')
        busy = re.search(r'^PROBE_BUSY = (\d+)$', receiver, re.MULTILINE)
        self.assertIsNotNone(busy, 'the receiver does not define PROBE_BUSY as a literal')
        self.assertEqual(bootstrap.PROBE_BUSY, int(busy.group(1)),
                         'deploy-bootstrap.py and github-deploy.py disagree on PROBE_BUSY')
        self.assertEqual(bootstrap.PROBE_BUSY, 2)
        source = Path(__file__).with_name('deploy-bootstrap.py').read_text()
        body = source[source.index('def install('):source.index('def main()')]
        self.assertIn('status == PROBE_BUSY', body, 'install() does not separate busy from broken')
        self.assertIn('another deployment holds', body, 'a busy lock is reported as a broken receiver')

    def test_probe_passes_busy_through_and_collapses_everything_else(self):
        # probe() decides which receiver statuses survive as their own answer. Read
        # directly, so folding busy back into the success set fails here even though
        # install()'s wording is unchanged.
        for status, expected in ((bootstrap.STARTED, bootstrap.STARTED),
                                 (bootstrap.PROBE_BUSY, bootstrap.PROBE_BUSY),
                                 (bootstrap.DEPLOY_FAILED, bootstrap.EXEC_FAILED),
                                 (bootstrap.TIMED_OUT, bootstrap.EXEC_FAILED)):
            with self.subTest(status=status):
                with patch.object(bootstrap, 'run', return_value=status):
                    self.assertEqual(bootstrap.probe(Path('/receiver'), 10), expected)

    def test_the_check_steps_cannot_wait_on_standard_input(self):
        steps = []

        def fake_run(arguments, **kwargs):
            steps.append(kwargs)
            return subprocess.CompletedProcess(arguments, 0)

        with patch.object(bootstrap.subprocess, 'run', side_effect=fake_run):
            bootstrap.check(Path('/staged/github-deploy.py'))
        self.assertEqual(len(steps), 2, 'both the compile step and the self-check step run')
        for kwargs in steps:
            self.assertIs(kwargs.get('stdin'), subprocess.DEVNULL, 'a check step inherits stdin')

    def test_the_pre_run_deadlines_are_the_constants_the_budget_counts(self):
        # PRE_RUN_LIMIT is only the real worst case if each step is given exactly the
        # constant it is summed from. A hard-coded 30 or 120 in the body drifts the
        # moment someone edits the constant, and the SSH budget test stays green
        # because it reads the constants rather than the calls.
        #
        # The constants are patched to a value they do not currently hold: with
        # timeout == the default, a body hard-coding that same number would satisfy
        # an equality assertion without ever reading the constant.
        steps = []

        def fake_check(arguments, **kwargs):
            steps.append(kwargs)
            return subprocess.CompletedProcess(arguments, 0)

        with patch.multiple(bootstrap, CHECK_TIMEOUT=7), \
                patch.object(bootstrap.subprocess, 'run', side_effect=fake_check):
            bootstrap.check(Path('/staged/github-deploy.py'))
        self.assertEqual(len(steps), 2, 'both check steps run')
        for kwargs in steps:
            self.assertEqual(kwargs.get('timeout'), 7,
                             'a check step does not take its deadline from CHECK_TIMEOUT')

        seen = []

        class Answer:
            def __enter__(self):
                return self

            def __exit__(self, *exception):
                return False

            def read(self, limit):
                return b'x'

        def fake_urlopen(url, timeout=None):
            seen.append(timeout)
            return Answer()

        with patch.multiple(bootstrap, FETCH_TIMEOUT=11), \
                patch.object(bootstrap.urllib.request, 'urlopen', side_effect=fake_urlopen):
            bootstrap.fetch(REVISION)
        self.assertEqual(seen, [11],
                         'the fetch does not take its deadline from FETCH_TIMEOUT')


if __name__ == '__main__':
    unittest.main()
