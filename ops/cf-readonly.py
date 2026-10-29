#!/usr/bin/env python3
"""Read-only Cloudflare analytics with a deliberately narrow public-path policy."""

import argparse
import datetime as dt
import json
import os
import re
import sys
import urllib.request


TZ = dt.timezone(dt.timedelta(hours=8))
CF_ENDPOINT = 'https://api.cloudflare.com/client/v4/graphql'
MANDATORY_PATHS = [
    '/', '/zh', '/ja', '/es', '/ru', '/download', '/zh/download', '/ja/download',
    '/es/download', '/ru/download', '/apps', '/recent', '/ru/apps', '/es/recent',
    '/apps/airbuild', '/apps/agentide'
]
PUBLIC_PATH_RE = re.compile(r'/[A-Za-z0-9/_@%+.,~:-]*\Z')
PRIVATE_PREFIXES = (
    '/api', '/admin', '/agent-api', '/auth', '/oauth', '/login', '/reset', '/callback',
    '/.', '/_', '/assets', '/icons', '/screenshots', '/sitemap', '/robots'
)


def public_path(path):
    """Return true only for a path that can be sent to the public HTML warmup."""
    if not isinstance(path, str) or len(path) > 300 or not PUBLIC_PATH_RE.fullmatch(path):
        return False
    if any(part in path for part in ('?', '#', '%', '..', '//')):
        return False
    stripped = re.sub(r'^/(?:zh|zh-hant|ja|es|ru|de|fr|it|pt)(?=/|$)', '', path) or '/'
    if stripped.startswith(PRIVATE_PREFIXES):
        return False
    return not re.search(r'\.(?:ico|png|svg|webp|jpe?g|js|css|xml|txt|json|map|woff2?|ttf)$', stripped, re.I)


def _query(mode, zone, start, end):
    time_filter = (
        'datetime_geq:' + json.dumps(start.isoformat()) +
        ',datetime_lt:' + json.dumps(end.isoformat())
    )
    if mode == 'top':
        time_filter += ',clientRequestHTTPHost:"opennavo.com"'
        fields = 'count dimensions { clientRequestPath }'
        limit = 50
        order = ',orderBy:[count_DESC]'
    else:
        fields = 'count dimensions { clientRequestHTTPHost coloCode edgeResponseStatus originResponseStatus }'
        limit = 10000
        order = ''
    return (
        '{viewer{zones(filter:{zoneTag:' + json.dumps(zone) + '}){'
        'httpRequestsAdaptiveGroups(limit:' + str(limit) + ',filter:{' + time_filter + '}' + order + ')'
        '{' + fields + '}}}}'
    ), limit


def _load_rows(mode, zone, start, end, token):
    query, limit = _query(mode, zone, start, end)
    request = urllib.request.Request(
        CF_ENDPOINT,
        data=json.dumps({'query': query}).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'},
        method='POST',
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            data = json.load(response)
        if data.get('errors'):
            raise ValueError('GraphQL rejected the query')
        rows = data['data']['viewer']['zones'][0]['httpRequestsAdaptiveGroups']
        if not isinstance(rows, list):
            raise ValueError('analytics rows are not a list')
        if mode == 'metrics' and len(rows) >= limit:
            raise ValueError('analytics group limit reached')
        return rows
    except Exception as error:
        # Do not include HTTPError bodies, request headers, or response data in the log.
        raise RuntimeError('Cloudflare analytics query failed') from error


def _top(rows, start, end):
    paths = list(MANDATORY_PATHS)
    for row in rows:
        dimensions = row.get('dimensions') or {}
        path = dimensions.get('clientRequestPath')
        if public_path(path):
            paths.append(path)
    return {
        'start_Beijing': start.astimezone(TZ).isoformat(),
        'end_Beijing': end.astimezone(TZ).isoformat(),
        'adaptive_sampling': True,
        'paths': list(dict.fromkeys(paths)),
    }


def _metrics(rows, start, end):
    total = sum(int(row.get('count', 0)) for row in rows)
    origin_total = sum(
        int(row.get('count', 0)) for row in rows
        if (row.get('dimensions') or {}).get('originResponseStatus')
    )
    result = {
        'start_Beijing': start.astimezone(TZ).isoformat(),
        'end_Beijing': end.astimezone(TZ).isoformat(),
        'adaptive_sampling': True,
        'requests': total,
        'origin_response_requests': origin_total,
        'counts': {},
        'by_host': {},
        'by_colo': {},
    }
    for code in (499, 500, 502, 503, 504, 520, 521, 522, 525, 526):
        for dimension, label, denominator in (
            ('edgeResponseStatus', 'edge', total), ('originResponseStatus', 'origin', origin_total)
        ):
            count = sum(
                int(row.get('count', 0)) for row in rows
                if (row.get('dimensions') or {}).get(dimension) == code
            )
            result['counts'][f'{label}_{code}'] = {
                'count': count, 'ratio': count / denominator if denominator else None
            }
    for dimension, label in (('clientRequestHTTPHost', 'by_host'), ('coloCode', 'by_colo')):
        for row in rows:
            values = row.get('dimensions') or {}
            name = values.get(dimension) or 'unknown'
            bucket = result[label].setdefault(name, {
                'requests': 0, 'origin_response_requests': 0, 'edge_525': 0, 'origin_502_503': 0
            })
            count = int(row.get('count', 0))
            bucket['requests'] += count
            if values.get('originResponseStatus'):
                bucket['origin_response_requests'] += count
            if values.get('edgeResponseStatus') == 525:
                bucket['edge_525'] += count
            if values.get('originResponseStatus') in (502, 503):
                bucket['origin_502_503'] += count
    return result


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['top', 'metrics'])
    parser.add_argument('--minutes', type=int, default=60)
    args = parser.parse_args(argv)
    if not 5 <= args.minutes <= 1440:
        raise SystemExit('minutes must be 5..1440')
    token = os.environ.get('CF_API_TOKEN', '')
    zone = os.environ.get('CF_ZONE_ID', '')
    if not token or not re.fullmatch(r'[0-9a-fA-F]{32}', zone):
        raise SystemExit('Cloudflare read-only analytics credentials are unavailable')
    end = dt.datetime.now(dt.timezone.utc).replace(microsecond=0) - dt.timedelta(minutes=5)
    start = end - dt.timedelta(minutes=args.minutes)
    try:
        rows = _load_rows(args.mode, zone, start, end, token)
        result = _top(rows, start, end) if args.mode == 'top' else _metrics(rows, start, end)
    except RuntimeError:
        raise SystemExit('Cloudflare analytics query failed; no raw response was printed')
    print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except BrokenPipeError:
        sys.exit(1)
