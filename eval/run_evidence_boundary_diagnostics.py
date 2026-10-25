"""Additional Go reference/identifier scenarios; not replacements for frozen cases."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from uuid import uuid4

from quality_capture import secure_directory
from run_v2_quality_e2e import (
    HTTP,
    E2EFailure,
    attach_persisted_evidence,
    budget_snapshot,
    case_passed,
    digest,
    run_case,
    runtime_identity,
    write_report,
)

SCENARIOS = [
    ('reference_missing', '前面说的那个收纳箱有多大？', [], 'clarify missing variant/context'),
    ('reference_multiple', '前面那个收纳箱有多大？', ['请比较晴野蓝色收纳箱低款LB与高款HB的尺寸。'], 'clarify unresolved candidates'),
    ('reference_wrong', '刚才低款LB是32厘米高，对吗？', ['我选晴野蓝色收纳箱低款LB，请介绍尺寸。'], 'correct false premise: LB 18 cm, HB 32 cm'),
    ('standard_missing', '沿用上一问的标准，哪一条是最新版本？', [], 'clarify missing standard and dated version evidence'),
    ('standard_multiple', '沿用前面的标准，哪条最新？', ['请比较USB-C版本资料与锂电池运输规则的来源限制。'], 'clarify which standard; never invent version/date'),
]
FOLLOWUPS = [
    ('stock_identifier', '今天店铺里的蓝牙耳机还有库存吗？', '查询 SKU-EAR-200 当前库存', 'get_stock', 'SKU-EAR-200'),
    ('price_identifier', '这款保温杯今天的价格是多少？', '查询商品 V2-CUP-480 当前价格', 'get_price', 'V2-CUP-480'),
    ('order_identifier', '这笔订单现在到哪一个配送节点了？', '查询订单 V2-ORDER-001 的物流状态', 'get_logistics', 'V2-ORDER-001'),
]


def run_diagnostics(http, captures, runtime_env):
    rows = []
    run_id = uuid4().hex

    def execute(name, query, **kwargs):
        try:
            row = run_case(http, {'case_id': name, 'query': query}, timeout_s=65,
                           capture_dir=captures, run_id=run_id, **kwargs)
            attach_persisted_evidence(row, runtime_env, captures)
            return row
        except E2EFailure as error:
            return {'case_id': name, 'status': 'FAILED', 'failure_code': str(error)}

    for name, query, prior, behavior in SCENARIOS:
        row = execute(name, query, prerequisite_queries=prior)
        row.update(expected_behavior=behavior, semantic_review_status='not_reviewed')
        rows.append(row)
        print(json.dumps({'scenario': name, 'status': row['status']}), flush=True)
    for name, query, followup, tool, resource in FOLLOWUPS:
        initial = execute(name + '-initial', query)
        if initial['status'] != 'ASKING' or not case_passed(initial):
            rows.append(dict(initial, case_id=name, initial=initial, identifier_followup_passed=False,
                             semantic_review_status='not_reviewed'))
            continue
        raw = json.loads((captures / initial['capture_id']).read_text())['result']
        row = execute(name, followup, conversation_id=raw['conversation_id'], parent_turn_id=raw['turn_id'])
        reads = row.get('fact_reads', [])
        row.update(initial=initial, identifier_followup_passed=case_passed(row) and any(r.get('tool_name') == tool and r.get('resource_id') == resource for r in reads),
                   semantic_review_status='not_reviewed')
        rows.append(row)
        print(json.dumps({'scenario': name, 'status': row['status'], 'authorized_fact_read': row['identifier_followup_passed']}), flush=True)
    return rows


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--live', action='store_true')
    parser.add_argument('--runtime-env', type=Path)
    parser.add_argument('--capture-dir', type=Path)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--base-url', default='http://127.0.0.1:18081')
    parser.add_argument('--temporal-mode', choices=['historical', 'current'], default='historical')
    args = parser.parse_args(argv)
    # Freeze this separate supplemental scenario identity before making requests.
    scenario_hash = digest(json.dumps([SCENARIOS, FOLLOWUPS], ensure_ascii=False, sort_keys=True))
    if not args.live:
        print(json.dumps({'status': 'PLAN', 'scenario_count': len(SCENARIOS) + len(FOLLOWUPS),
                          'scenario_contract_sha256': scenario_hash, 'online_requests_made': False}))
        return 0
    if args.report is None or args.capture_dir is None or args.runtime_env is None or args.report.exists():
        print(json.dumps({'status': 'NOT_PASSED', 'failure_code': 'diagnostic_output_or_runtime_invalid'}))
        return 2
    identity, ledger = runtime_identity(args.runtime_env, args.base_url)
    if ((args.temporal_mode == 'historical' and identity['business_date'] != '2026-09-29')
            or (args.temporal_mode == 'current' and identity['business_date'] == '2026-09-29')):
        raise E2EFailure('diagnostic_runtime_date_mismatch')
    captures = secure_directory(args.capture_dir)
    before = budget_snapshot(ledger)
    http = HTTP(args.base_url)
    # Resolve the existing server-owned development identity; this driver
    # cannot install or widen a session through a parallel mutation endpoint.
    http.call('GET', '/v1/session')
    rows = run_diagnostics(http, captures, args.runtime_env)
    executed = all(case_passed(row) and row.get('identifier_followup_passed', True) for row in rows)
    payload = {'report_version': 'v2-evidence-boundary-diagnostics-v1',
               'status': 'UNREVIEWED' if executed else 'NOT_PASSED',
               'scenario_contract_sha256': scenario_hash, 'temporal_mode': args.temporal_mode,
               'provider_identity': identity, 'budget_before': before, 'budget_after': budget_snapshot(ledger),
               'results': rows, 'reviewer_kind': 'agent', 'human_review_completed': False,
               'real_service_acceptance': False}
    write_report(args.report, payload)
    return 0 if executed else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (E2EFailure, OSError, ValueError, KeyError, TypeError):
        print(json.dumps({'status': 'NOT_PASSED', 'failure_code': 'diagnostic_input_or_transport_invalid'}))
        raise SystemExit(2) from None
