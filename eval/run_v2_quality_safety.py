#!/usr/bin/env python3
"""Verify excluded safety cases are rejected before any Provider request."""
from __future__ import annotations

import argparse
import hashlib
import json
import sqlite3
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_ROOT = Path('/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional')


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding='utf-8'))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--cases', type=Path, default=DEFAULT_ROOT / 'synthetic-m2-v1-revision-1.jsonl')
    parser.add_argument('--audit', type=Path, required=True)
    parser.add_argument('--ledger', type=Path, required=True)
    parser.add_argument('--before', type=int, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    cases = [json.loads(line) for line in args.cases.read_text().splitlines() if line.strip()]
    audit = load(args.audit)
    excluded = [r for r in audit.get('case_results', []) if r.get('status') != 'WITHIN_BUDGET']
    excluded_ids = {r.get('case_id') for r in excluded}
    expected = {str(r['case_id']) for r in cases} - {str(r['case_id']) for r in audit.get('case_results', []) if r.get('status') == 'WITHIN_BUDGET'}
    if len(excluded) != 7 or excluded_ids != expected:
        raise SystemExit('safety partition invalid')
    with sqlite3.connect(f'file:{args.ledger}?mode=ro', uri=True) as connection:
        after = int(connection.execute('SELECT count(*) FROM attempt').fetchone()[0])
    payload = {'report_version': 'v2-quality-safety-v1', 'status': 'PASS',
               'cases_sha256': hashlib.sha256(args.cases.read_bytes()).hexdigest(),
               'audit_sha256': hashlib.sha256(args.audit.read_bytes()).hexdigest(),
               'results': [{'case_id': case_id, 'passed': True, 'provider_attempt_delta': after - args.before,
                            'decision': 'excluded_before_provider'} for case_id in sorted(excluded_ids)],
               'real_service_acceptance': False}
    if after != args.before:
        payload['status'] = 'NOT_PASSED'
        for row in payload['results']:
            row['passed'] = False
    args.report.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with args.report.open('x', encoding='utf-8') as stream:
        json.dump(payload, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
    print(json.dumps({'status': payload['status'], 'case_count': len(excluded_ids),
                      'provider_attempt_delta': after - args.before}))
    return 0 if payload['status'] == 'PASS' else 4


if __name__ == '__main__':
    raise SystemExit(main())
