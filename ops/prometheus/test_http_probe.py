import importlib.util
import pathlib
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('probe', pathlib.Path(__file__).with_name('http-probe.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ProbeTest(unittest.TestCase):
    def test_failure_signals(self):
        target = dict(service='web', layer='origin', url='https://example.org/',
                      resolve='example.org:443:127.0.0.1')
        for returncode, output, expected in [(0, '200 0.1', 1), (0, '502 0.1', 0),
                                              (60, '200 0.1', 0), (0, '302 0.1', 0)]:
            with self.subTest(output=output, returncode=returncode), patch.object(module.subprocess, 'run') as run:
                run.return_value = subprocess.CompletedProcess([], returncode, stdout=output)
                self.assertIn('success{service="web",layer="origin"} ' + str(expected), module.probe(target))
                self.assertIn('--resolve', run.call_args.args[0])
        with patch.object(module.subprocess, 'run', side_effect=subprocess.TimeoutExpired('curl', 5)):
            self.assertIn('success{service="web",layer="origin"} 0', module.probe(target))


if __name__ == '__main__':
    unittest.main()
