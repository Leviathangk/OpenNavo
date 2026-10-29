#!/usr/bin/env python3
"""Bounded public HTML warmup; edge failures are reported to the caller without leaking bodies."""

import argparse
import json
import signal
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
import runpy


POLICY = runpy.run_path(str(Path(__file__).with_name('cf-readonly.py')), run_name='warmup_policy')
MANDATORY_PATHS = POLICY['MANDATORY_PATHS']
public_path = POLICY['public_path']
WARM_ORIGIN = 'https://opennavo.com'
WARM_HOST = 'opennavo.com'
MAX_BODY_BYTES = 2 * 1024 * 1024
PATH_LIMIT = 66
DEFAULT_MAX_SECONDS = 300


class WarmupBudgetExceeded(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def load_paths(path):
    try:
        data = json.loads(Path(path).read_text())
        candidate = data.get('paths')
        if not isinstance(candidate, list):
            raise ValueError('paths is not a list')
    except Exception as error:
        print(f'Warning: edge warm paths unavailable ({type(error).__name__}); using fixed public paths.', file=sys.stderr)
        candidate = []
    return list(dict.fromkeys([*MANDATORY_PATHS, *(path for path in candidate if public_path(path))]))[:PATH_LIMIT]


def warm_one(path, opener, *, origin=WARM_ORIGIN, host=WARM_HOST):
    request = urllib.request.Request(
        origin + path,
        headers={'Host': host, 'Accept': 'text/html', 'User-Agent': 'OpenNavo-Warmup/1.0'},
    )
    try:
        with opener.open(request, timeout=15) as response:
            body = response.read(MAX_BODY_BYTES + 1)
            status = response.status
            content_type = response.headers.get('Content-Type', '')
        if status >= 500:
            print(f'Edge warmup server failure: {path} status={status}', file=sys.stderr)
            return False
        if status != 200:
            print(f'Edge warmup recorded status={status}: {path}', file=sys.stderr)
        if status == 200 and len(body) > MAX_BODY_BYTES:
            print(f'Edge warmup recorded a response larger than {MAX_BODY_BYTES} bytes: {path}', file=sys.stderr)
        if status == 200 and 'text/html' not in content_type.lower():
            print(f'Edge warmup recorded non-HTML content type for {path}: {content_type}', file=sys.stderr)
        return True
    except urllib.error.HTTPError as error:
        # HTTPError includes no body in this path; 3xx/404/410 are expected for retired public URLs.
        if 300 <= error.code < 500:
            print(f'Edge warmup recorded status={error.code}: {path}', file=sys.stderr)
            error.close()
            return True
        print(f'Edge warmup server failure: {path} status={error.code}', file=sys.stderr)
        error.close()
        return False
    except (TimeoutError, OSError, urllib.error.URLError) as error:
        print(f'Edge warmup connection failure for {path}: {type(error).__name__}', file=sys.stderr)
        return False


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('paths_file')
    parser.add_argument('--max-seconds', type=int, default=DEFAULT_MAX_SECONDS,
                        help='total warmup time budget in seconds (default: 300)')
    args = parser.parse_args(argv)
    if args.max_seconds <= 0:
        parser.error('--max-seconds must be positive')
    paths = load_paths(args.paths_file)
    opener = urllib.request.build_opener(NoRedirect)
    failed = []
    completed = 0

    def expire_budget(_signum, _frame):
        raise WarmupBudgetExceeded()

    previous_handler = signal.signal(signal.SIGALRM, expire_budget)
    try:
        # A socket timeout alone cannot bound retries or a response that trickles bytes.
        signal.setitimer(signal.ITIMER_REAL, args.max_seconds)
        for path in paths:
            success = False
            for attempt in range(2):
                if warm_one(path, opener):
                    success = True
                    break
                if attempt == 0:
                    time.sleep(2)
            if not success:
                failed.append(path)
            completed += 1
            if completed < len(paths):
                time.sleep(1)
    except WarmupBudgetExceeded:
        raise SystemExit(f'Warning: edge warmup exhausted its {args.max_seconds}s time budget; '
                         f'completed={completed}, failed={len(failed)}, skipped={len(paths) - completed}.')
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous_handler)
    if failed:
        raise SystemExit('edge warmup failed for approved public paths; inspect privately')
    print('Public edge warmup passed:', len(paths), 'paths')


if __name__ == '__main__':
    main()
