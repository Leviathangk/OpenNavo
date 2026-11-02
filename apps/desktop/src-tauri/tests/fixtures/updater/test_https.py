"""The updater TLS fixture must not ask the resolver before it prints its port.

The Rust tests read the fixture's port from its first stdout line. Whatever this file does
before that line is start-up budget: `https.py` already spends it on an RSA key, and
HTTPServer.server_bind would add a reverse-DNS lookup that has no timeout of its own.
"""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

FIXTURE = Path(__file__).parent / 'https.py'

# Runs the real fixture with socket.getfqdn replaced by a function that never returns, the
# way a resolver with no answer behaves. The fixture binds its socket before this point,
# so a fixture that asks the resolver still has a listening port and still prints nothing:
# the Rust test waits on a server it could already have talked to.
PROBE = """
import runpy, socket, sys, time

def blocked(*args, **kwargs):
    time.sleep(600)

socket.getfqdn = blocked
sys.argv = [FIXTURE, PAYLOAD]
runpy.run_path(FIXTURE, run_name='__main__')
"""


def read_first_line(stream, seconds):
    """Return the first line of `stream`, or '' if none arrives within `seconds`."""
    return subprocess.run(
        [sys.executable, '-c',
         'import select, sys\n'
         'ready, _, _ = select.select([sys.stdin], [], [], %d)\n'
         'sys.stdout.write(sys.stdin.readline() if ready else "")\n' % seconds],
        stdin=stream, capture_output=True, text=True, timeout=seconds + 15).stdout


class FixtureTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.mkdtemp(prefix='opennavo-fixture-test-')
        self.payload = Path(self.directory, 'payload')
        self.payload.write_bytes(b'fixture-body')
        self.probe = Path(self.directory, 'probe.py')
        self.probe.write_text(PROBE.replace('FIXTURE', repr(str(FIXTURE)))
                                   .replace('PAYLOAD', repr(str(self.payload))))

    def tearDown(self):
        shutil.rmtree(self.directory, ignore_errors=True)

    def test_the_fixture_prints_its_port_without_a_reverse_dns_lookup(self):
        process = subprocess.Popen([sys.executable, '-I', '-u', str(self.probe)],
                                   stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        try:
            line = read_first_line(process.stdout, 5)
        finally:
            process.kill()
            process.wait()
            process.stdout.close()
        self.assertRegex(line.strip(), r'^\d+$',
                         'the fixture printed no port within five seconds while the '
                         'resolver was hung, so it blocks on a lookup before the line '
                         'the Rust tests wait for')


if __name__ == '__main__':
    unittest.main()
