import importlib.util
import pathlib
import subprocess
import threading
import time
import unittest
from email.utils import formatdate
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('probe', pathlib.Path(__file__).with_name('http-probe.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ProbeTest(unittest.TestCase):
    def setUp(self):
        self.now = 0.0
        self.clock = patch.object(module.time, 'monotonic', side_effect=lambda: self.now)
        self.sleep = patch.object(module.time, 'sleep', side_effect=self.advance)
        self.clock.start()
        self.sleep.start()
        self.addCleanup(self.clock.stop)
        self.addCleanup(self.sleep.stop)

    def advance(self, seconds):
        self.now += seconds

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

    def test_retry_after_and_cooldown_across_scrapes(self):
        target = dict(service='web', layer='origin', url='https://example.org/')
        for header in ['120', formatdate(time.time() + 120, usegmt=True)]:
            with self.subTest(header=header), patch.object(module.subprocess, 'run') as run:
                run.return_value = subprocess.CompletedProcess([], 0, stdout=f'HTTP/1.1 503 Busy\r\nRetry-After: {header}\r\n\r\n503 0.1')
                state = {}
                result = module.probe(target, state=state)
                self.assertEqual(run.call_count, 1)
                self.assertIn('temporary_failure{service="web",layer="origin"} 1', result)
                self.assertGreater(state['due'], self.now + 118)
                module.probe(target, state=state)
                self.assertEqual(run.call_count, 1, 'the next scrape must honor the same cooldown')
                self.now = state['due'] + 1
                run.return_value = subprocess.CompletedProcess([], 0, stdout='200 0.1')
                result = module.probe(target, state=state)
                self.assertIn('success{service="web",layer="origin"} 1', result)
                self.assertEqual(state['failures'], 0)

    def test_invalid_retry_after_falls_back_to_bounded_jitter(self):
        with patch.object(module.random, 'uniform', return_value=0.5):
            for header in [None, 'invalid', '-1', '-9999', '9' * 4000, '9999999999', '\u00b2',
                           formatdate(time.time() - 120, usegmt=True)]:
                with self.subTest(header_length=len(header or '')):
                    self.assertEqual(module.retry_delay(header, 2), 4.5)

    def test_retry_after_cap_bounds_cooldown_and_allows_recovery(self):
        target = dict(service='web', layer='origin', url='https://example.org/')
        for header in ['2147483647', 'Fri, 31 Dec 9999 23:59:59 GMT']:
            for configured in [None, 45]:
                with self.subTest(header=header, configured=configured), patch.object(module.subprocess, 'run') as run:
                    policy = module.ProbePolicy({} if configured is None else {'maxRetryAfterSeconds': configured})
                    cap = configured or 900
                    self.assertEqual(module.retry_delay(header, 2, policy.max_retry_after), cap)
                    state = {}
                    run.return_value = subprocess.CompletedProcess([], 0, stdout=f'HTTP/1.1 503 Busy\r\nRetry-After: {header}\r\n\r\n503 0.1')
                    result = module.probe(target, policy=policy, state=state)
                    self.assertEqual(state['due'], self.now + cap)
                    self.assertIn(f'retry_after_seconds{{service="web",layer="origin"}} {float(cap)}', result)
                    module.probe(target, policy=policy, state=state)
                    self.assertEqual(run.call_count, 1)
                    self.advance(cap)
                    run.return_value = subprocess.CompletedProcess([], 0, stdout='200 0.1')
                    self.assertIn('success{service="web",layer="origin"} 1', module.probe(target, policy=policy, state=state))
                    self.assertEqual(run.call_count, 2)
                    self.assertEqual(state['due'], 0)

    def test_bounded_retries_jitter_and_recovery(self):
        target = dict(service='web', layer='origin', url='https://example.org/')
        with patch.object(module.random, 'uniform', return_value=0.5), patch.object(module.subprocess, 'run') as run:
            run.side_effect = [subprocess.CompletedProcess([], 0, stdout=f'{code} 0.1') for code in [429, 503, 200]]
            self.assertIn('success{service="web",layer="origin"} 1', module.probe(target))
            self.assertEqual(run.call_count, 3)
            self.assertEqual(self.now, 7.0)
            run.side_effect = None
            run.return_value = subprocess.CompletedProcess([], 0, stdout='503 0.1')
            state = {}
            result = module.probe(target, state=state)
            self.assertEqual(run.call_count, 6)
            self.assertIn('temporary_failure{service="web",layer="origin"} 1', result)
            self.assertGreater(state['due'], self.now)

    def test_retry_cap_configuration_clamps_at_24_hours_and_warns(self):
        for configured in [86400, 86400.1, 90000, 10 ** 300]:
            with self.subTest(configured=configured):
                with patch.object(module.logging, 'warning') as warning:
                    policy = module.ProbePolicy({'maxRetryAfterSeconds': configured})
                    self.assertEqual(policy.max_retry_after, 86400)
                    if configured > 86400:
                        warning.assert_called_once_with('probe retry cap exceeds 24h; clamping to 24h')
                    else:
                        warning.assert_not_called()
                self.assertEqual(module.retry_delay('2147483647', 0, policy.max_retry_after), 86400)

    def test_total_scrape_deadline_bounds_all_targets(self):
        target = dict(service='web', layer='origin', url='https://example.org/')
        def timeout(*_args, **kwargs):
            self.assertLessEqual(kwargs['timeout'], 12)
            self.advance(kwargs['timeout'])
            raise subprocess.TimeoutExpired('curl', kwargs['timeout'])
        with patch.object(module.subprocess, 'run', side_effect=timeout) as run:
            body = module.probe_targets([target] * 8, [{} for _ in range(8)], module.ProbePolicy())
            self.assertEqual(run.call_count, 1)
            self.assertEqual(self.now, 12)
            self.assertEqual(body.count(b'temporary_failure'), 8)

    def test_concurrency_is_configurable_and_defaults_to_one(self):
        self.clock.stop()
        self.sleep.stop()
        target = dict(service='web', layer='origin', url='https://example.org/')
        active = peak = 0
        lock = threading.Lock()
        def request(*_args, **_kwargs):
            nonlocal active, peak
            with lock:
                active += 1
                peak = max(active, peak)
            time.sleep(0.01)
            with lock:
                active -= 1
            return subprocess.CompletedProcess([], 0, stdout='200 0.1')
        for concurrency in [None, 2]:
            peak = 0
            policy = module.ProbePolicy({} if concurrency is None else {'concurrency': concurrency})
            with patch.object(module.subprocess, 'run', side_effect=request):
                module.probe_targets([target] * 4, [{} for _ in range(4)], policy)
            self.assertEqual(peak, concurrency or 1)
        for config in [{'concurrency': 0}, {'concurrency': 100}, {'maxAttempts': 4}, {'budgetSeconds': 100}, {'budgetSeconds': float('nan')}]:
            with self.subTest(config=config), self.assertRaises(ValueError):
                module.ProbePolicy(config)
        for cap in [0, -1, 'invalid', True, float('nan'), float('inf')]:
            with self.subTest(cap=cap), self.assertRaises(ValueError):
                module.ProbePolicy({'maxRetryAfterSeconds': cap})


if __name__ == '__main__':
    unittest.main()
