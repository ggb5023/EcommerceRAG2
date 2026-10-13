"""Independent, hash-bound evidence-boundary acceptance; never rewrites V1 truth."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
from pathlib import Path

from build_human_review_package import build_package
from build_human_review_sidecar import build_sidecar as original_sidecar
from build_human_review_sidecar import write_once
from quality_capture import secure_directory
from run_v2_quality_e2e import (
    DEFAULT_CASES,
    DEFAULT_REVISION_ROOT,
    DEFAULT_SCENARIOS,
    case_passed,
)

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONTRACT = ROOT / 'eval/evidence-boundary-contract.v1.json'
THRESHOLDS = {'security_errors': 0, 'high_risk_pass_rate': 1.0, 'ordinary_pass_rate': .9,
              'support_delivery_rate': .95, 'citation_validity_rate': .95}
FILES = {'cases_sha256': 'synthetic-m2-v1-revision-1.jsonl', 'corpus_sha256': 'documents.jsonl',
         'alignment_sha256': 'alignment-revision-1.json',
         'evidence_review_sha256': 'answer-point-evidence-review-revision-1-context-v2.json'}
BOUNDARY_CHECKS = ('citation_correct', 'copy_eligibility_correct', 'no_unsupported_assertions')


def load(path):
    return json.loads(Path(path).read_text())


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def jsonl(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def validate_contract(path=DEFAULT_CONTRACT, revision_root=DEFAULT_REVISION_ROOT,
                      scenarios_path=DEFAULT_SCENARIOS):
    value = load(path)
    if (value.get('contract_version') != 'v2-evidence-boundary-contract-v1'
            or value.get('status') != 'FROZEN' or value.get('real_service_acceptance') is not False
            or value.get('thresholds') != THRESHOLDS or value.get('historical_business_date') != '2026-09-29'):
        raise ValueError('boundary_contract_invalid')
    bindings = {key: sha256(revision_root / name) for key, name in FILES.items()}
    bindings['scenarios_sha256'] = sha256(scenarios_path)
    if value.get('bindings') != bindings:
        raise ValueError('boundary_frozen_input_mismatch')
    cases = {c['case_id']: c for c in jsonl(revision_root / FILES['cases_sha256'])}
    corpus = {c['document_id']: c for c in jsonl(revision_root / FILES['corpus_sha256'])}
    alignment = load(revision_root / FILES['alignment_sha256'])['case_to_source_documents']
    scenarios = {s['case_id']: s for s in load(scenarios_path)['scenarios']}
    excluded = {'syn-049', 'syn-050', 'syn-055', 'syn-056', 'syn-057', 'syn-059', 'syn-060'}
    rows = value.get('cases', [])
    if (len(cases) != 60 or len(corpus) != 37 or len(rows) != 53
            or len({r['case_id'] for r in rows}) != 53
            or {r['case_id'] for r in rows} != set(cases) - excluded
            or value.get('excluded_case_ids') != sorted(excluded)):
        raise ValueError('boundary_case_set_invalid')
    for row in rows:
        case = cases[row['case_id']]
        risk = 'high' if set(case['tags']) & {'policy', 'freshness', 'unanswerable', 'negative', 'safety_and_routing'} else 'ordinary'
        if (row['risk'] != risk or row['query_sha256'] != digest(case['query'])
                or row['case_sha256'] != digest(json.dumps(case, ensure_ascii=False, sort_keys=True))
                or row['original_point_sha256'] != [digest(p) for p in case['expected_answer_points']]
                or row['scenario_sha256'] != digest(json.dumps(scenarios.get(row['case_id']), ensure_ascii=False, sort_keys=True))
                or row['expected_behavior'] not in {'answer', 'clarify', 'limited_summary', 'workflow', 'fact_answer'}
                or not row['requirements'] or len(set(row['requirements'])) != len(row['requirements'])
                or type(row['citation_applicable']) is not bool):
            raise ValueError('boundary_case_binding_invalid')
        for support in row['required_support']:
            if support['document_id'] not in alignment[row['case_id']].values():
                raise ValueError('boundary_support_scope_invalid')
            doc = corpus[support['document_id']]
            ch = next((c for c in doc['chunks'] if c['chunk_id'] == support['chunk_id']), None)
            if (ch is None or support['content_sha256'] != digest(ch['content'])
                    or support['source_version_id'] != doc['document_version_id']
                    or ch['content'].startswith('---') or len(ch['content']) <= 40):
                raise ValueError('boundary_support_invalid')
        if row['citation_applicable'] and not row['required_support']:
            raise ValueError('boundary_support_missing')
    return value


def captured_rows(report_path, capture_dir, contract, contract_path=DEFAULT_CONTRACT):
    report = load(report_path)
    secure_directory(capture_dir)
    expected = {c['case_id']: c for c in contract['cases']}
    rows = report.get('results', [])
    if (report.get('report_version') != 'v2-quality-e2e-v2' or report.get('case_count') != 53
            or len(rows) != 53 or len({r['case_id'] for r in rows}) != 53
            or {r['case_id'] for r in rows} != set(expected)
            or report.get('cases_sha256') != contract['bindings']['cases_sha256']
            or report.get('scenarios_sha256') != contract['bindings']['scenarios_sha256']
            or report.get('boundary_contract_sha256') != sha256(contract_path)
            or report.get('real_service_acceptance') is not False):
        raise ValueError('boundary_execution_binding_invalid')
    result = {}
    for row in rows:
        name = row.get('capture_id', '')
        if Path(name).name != name or not name.endswith('.json'):
            raise ValueError('boundary_capture_name_invalid')
        file = capture_dir / name
        if file.is_symlink() or file.stat().st_uid != os.getuid() or file.stat().st_mode & 0o777 != 0o600 or sha256(file) != row.get('capture_sha256'):
            raise ValueError('boundary_capture_invalid')
        cap = load(file)
        if (cap.get('capture_version') != 'v2-http-quality-capture-v1'
                or cap.get('case_id') != row['case_id']
                or cap.get('query_sha256') != expected[row['case_id']]['query_sha256']
                or cap.get('result', {}).get('status') != row.get('status')
                or digest(cap.get('result', {}).get('answer') or '') != row.get('answer_sha256')):
            raise ValueError('boundary_capture_identity_invalid')
        result[row['case_id']] = (row, cap)
    return report, result


def make_package(report_path, capture_dir, output, contract_path=DEFAULT_CONTRACT):
    contract = validate_contract(contract_path)
    captured_rows(report_path, capture_dir, contract, contract_path)
    manifest = build_package(report_path, DEFAULT_CASES, capture_dir, output, reviewer_kind='agent')
    by_id = {c['case_id']: c for c in contract['cases']}
    for entry in manifest['entries']:
        file = output / entry['review_file']
        case = load(file)
        rule = by_id[entry['case_id']]
        case['boundary_contract'] = rule
        case['boundary_review'] = {'review_status': 'not_reviewed', 'security_error': False,
                                  **dict.fromkeys(BOUNDARY_CHECKS, False),
                                  'requirements': dict.fromkeys(rule['requirements'], False),
                                  'support_chunk_ids': [], 'issue_codes': [], 'notes': ''}
        file.write_text(json.dumps(case, ensure_ascii=False, indent=2) + '\n')
    manifest['boundary_contract_sha256'] = sha256(contract_path)
    (output / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return {'status': 'UNREVIEWED', 'case_count': 53, 'human_review_completed': False}


def make_sidecar(report_path, capture_dir, package_dir, output, original_output,
                 contract_path=DEFAULT_CONTRACT):
    contract = validate_contract(contract_path)
    _report, captures = captured_rows(report_path, capture_dir, contract, contract_path)
    manifest = load(package_dir / 'manifest.json')
    if manifest.get('boundary_contract_sha256') != sha256(contract_path):
        raise ValueError('boundary_package_binding_invalid')
    attestation = load(package_dir / 'agent-attestation.json')
    if (attestation.get('boundary_contract_sha256') != sha256(contract_path)
            or attestation.get('review_package_manifest_sha256') != sha256(package_dir / 'manifest.json')):
        raise ValueError('boundary_attestation_binding_invalid')
    # Independently validate every immutable original input and its honest point review.
    original = original_sidecar(report_path, package_dir, capture_dir, DEFAULT_CASES,
                                original_output, reviewer_kind='agent')
    original_by_id = {r['case_id']: r for r in original['rows']}
    rows = []
    for rule in contract['cases']:
        row, cap = captures[rule['case_id']]
        case = load(package_dir / 'cases' / (rule['case_id'] + '.json'))
        review = case.get('boundary_review', {})
        ids = {e['id'] for e in cap.get('result', {}).get('evidence', [])}
        if (case.get('boundary_contract') != rule or review.get('review_status') not in {'pass', 'fail'}
                or type(review.get('security_error')) is not bool
                or any(type(review.get(k)) is not bool for k in BOUNDARY_CHECKS)
                or set(review.get('requirements', {})) != set(rule['requirements'])
                or any(type(v) is not bool for v in review['requirements'].values())
                or not isinstance(review.get('support_chunk_ids'), list)
                or not set(review['support_chunk_ids']).issubset(ids)
                or any(not re.fullmatch(r'[a-z][a-z0-9_]{0,63}', i) for i in review.get('issue_codes', []))):
            raise ValueError('boundary_case_review_invalid')
        if review['review_status'] == 'pass' and (not case_passed(row) or review['security_error']
                or not all(review[k] for k in BOUNDARY_CHECKS) or not all(review['requirements'].values())):
            raise ValueError('boundary_false_pass')
        rows.append({'case_id': rule['case_id'], 'risk': rule['risk'], 'capture_sha256': row['capture_sha256'],
                     'review_status': review['review_status'], 'security_error': review['security_error'],
                     **{k: review[k] for k in BOUNDARY_CHECKS}, 'requirements': review['requirements'],
                     'support_chunk_ids': review['support_chunk_ids'], 'issue_codes': review['issue_codes'],
                     'original_point_states': [p['status'] for p in original_by_id[rule['case_id']]['points']]})
    sidecar = {'review_version': 'v2-evidence-boundary-review-v1', 'contract_sha256': sha256(contract_path),
               'e2e_report_sha256': sha256(report_path), 'original_review_sha256': sha256(original_output),
               'reviewer_kind': 'agent', 'human_review_completed': False, 'review_completed': True,
               'delegation_authorization': 'explicit_user_instruction',
               'agent_attestation_sha256': sha256(package_dir / 'agent-attestation.json'),
               'real_service_acceptance': False, 'rows': rows}
    write_once(output, sidecar)
    return sidecar


def evaluate(report_path, capture_dir, review_path, safety_path, lifecycle_path,
             contract_path=DEFAULT_CONTRACT):
    contract = validate_contract(contract_path)
    report, captures = captured_rows(report_path, capture_dir, contract, contract_path)
    review = load(review_path)
    expected = {c['case_id']: c for c in contract['cases']}
    if (review.get('review_version') != 'v2-evidence-boundary-review-v1'
            or review.get('contract_sha256') != sha256(contract_path)
            or review.get('e2e_report_sha256') != sha256(report_path)
            or review.get('reviewer_kind') != 'agent' or review.get('human_review_completed') is not False
            or review.get('review_completed') is not True
            or review.get('delegation_authorization') != 'explicit_user_instruction'
            or review.get('real_service_acceptance') is not False
            or len(review.get('rows', [])) != 53
            or len({r['case_id'] for r in review['rows']}) != 53
            or {r['case_id'] for r in review['rows']} != set(expected)):
        raise ValueError('boundary_review_binding_invalid')
    reasons, failed, ordinary, high, applicable, valid, support_cases, delivered, security = [], [], [], [], 0, 0, 0, 0, 0
    corpus = {d['document_id']: d for d in jsonl(DEFAULT_REVISION_ROOT / 'documents.jsonl')}
    original_states = []
    for item in review['rows']:
        rule = expected[item['case_id']]
        row, cap = captures[item['case_id']]
        if (item.get('capture_sha256') != row['capture_sha256'] or item.get('risk') != rule['risk']
                or set(item.get('requirements', {})) != set(rule['requirements'])
                or any(type(v) is not bool for v in item['requirements'].values())
                or any(type(item.get(k)) is not bool for k in (*BOUNDARY_CHECKS, 'security_error'))):
            raise ValueError('boundary_review_row_invalid')
        securities = int(item['security_error'])
        security += securities
        ok = (item.get('review_status') == 'pass' and row.get('status') in {'DONE', 'ASKING'} and case_passed(row) and not securities
              and all(item[k] for k in BOUNDARY_CHECKS) and all(item['requirements'].values()))
        (high if rule['risk'] == 'high' else ordinary).append(ok)
        if not ok:
            failed.append(item['case_id'])
        if row.get('status') not in {'DONE', 'ASKING'} or not case_passed(row):
            reasons.append('execution_or_replay_failed')
        result = cap['result']
        evidence = result.get('evidence', [])
        ids = {e['id'] for e in evidence}
        supports = item.get('support_chunk_ids', [])
        if not isinstance(supports, list) or len(set(supports)) != len(supports) or not set(supports).issubset(ids):
            raise ValueError('boundary_support_not_delivered')
        if rule['citation_applicable']:
            applicable += 1
            citations = result.get('citations', [])
            valid += bool(citations) and item['citation_correct'] and all(c.get('evidence_id') in ids for c in citations)
        if rule['required_support']:
            support_cases += 1
            all_delivered = True
            for bound in rule['required_support']:
                source = next(c for c in corpus[bound['document_id']]['chunks'] if c['chunk_id'] == bound['chunk_id'])['content']
                all_delivered &= any(e.get('documentId') == bound['document_id'] and source in e.get('snippet', '')
                                     for e in evidence)
            delivered += all_delivered
        original_states.extend(item.get('original_point_states', []))
    safety, lifecycle = load(safety_path), load(lifecycle_path)
    sr = safety.get('results', [])
    if (safety.get('status') != 'PASS' or safety.get('evidence_kind') != 'go_http_sse'
            or safety.get('e2e_report_sha256') != sha256(report_path)
            or safety.get('lifecycle_report_sha256') != sha256(lifecycle_path)
            or safety.get('cases_sha256') != contract['bindings']['cases_sha256']
            or safety.get('real_service_acceptance') is not False
            or len(sr) != 7 or {r.get('case_id') for r in sr} != set(contract['excluded_case_ids'])
            or any(r.get('passed') is not True or r.get('provider_attempt_before') != r.get('provider_attempt_after')
                   or r.get('request_reached_provider') is not False for r in sr)
            or len(safety.get('replay_checks', [])) != 4
            or any(r.get('passed') is not True for r in safety['replay_checks'])
            or safety.get('provider_attempt_before') != safety.get('provider_attempt_after')):
        reasons.append('safety_evidence_invalid')
    if (lifecycle.get('status') != 'PASS' or report.get('lifecycle_report_sha256') != sha256(lifecycle_path)
            or lifecycle.get('corpus', {}).get('corpus_sha256') != contract['bindings']['corpus_sha256']):
        reasons.append('lifecycle_evidence_invalid')
    support_rate = delivered / support_cases if support_cases else None
    citation_rate = valid / applicable if applicable else None
    if security or not all(high): reasons.append('security_or_high_risk_failure')
    if not ordinary or sum(ordinary) / len(ordinary) < .9: reasons.append('ordinary_pass_rate_below_threshold')
    if support_rate is None or support_rate < .95: reasons.append('support_delivery_below_threshold')
    if citation_rate is None or citation_rate < .95: reasons.append('citation_validity_below_threshold')
    return {'report_version': 'v2-evidence-boundary-gate-v1', 'status': 'PASS' if not reasons else 'NOT_PASSED',
            'contract_sha256': sha256(contract_path), 'e2e_report_sha256': sha256(report_path),
            'review_sha256': sha256(review_path), 'safety_report_sha256': sha256(safety_path),
            'reviewer_kind': 'agent', 'human_review_completed': False, 'real_service_acceptance': False,
            'case_count': 53, 'passed_cases': sum(high) + sum(ordinary), 'failed_case_ids': sorted(failed),
            'high_risk_passed': sum(high), 'high_risk_count': len(high), 'security_errors': security,
            'ordinary_pass_rate': sum(ordinary) / len(ordinary) if ordinary else None,
            'support_applicable_cases': support_cases, 'support_delivered_cases': delivered,
            'support_delivery_rate': support_rate, 'citation_applicable_cases': applicable,
            'citation_valid_cases': valid, 'citation_validity_rate': citation_rate,
            'citation_not_applicable_cases': 53 - applicable,
            'original_point_states': {s: original_states.count(s) for s in ('supported', 'omitted', 'contradicted', 'distorted')},
            'gate_reasons': sorted(set(reasons))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['validate', 'package', 'sidecar', 'gate'])
    parser.add_argument('--contract', type=Path, default=DEFAULT_CONTRACT)
    for name in ('report', 'capture-dir', 'package-dir', 'review', 'safety', 'lifecycle', 'output', 'original-output'):
        parser.add_argument('--' + name, type=Path)
    args = parser.parse_args()
    try:
        if args.command == 'validate':
            c = validate_contract(args.contract)
            result = {'status': 'PASS', 'case_count': len(c['cases']), 'contract_sha256': sha256(args.contract)}
        elif args.command == 'package':
            result = make_package(args.report, args.capture_dir, args.package_dir, args.contract)
        elif args.command == 'sidecar':
            result = make_sidecar(args.report, args.capture_dir, args.package_dir, args.output,
                                  args.original_output, args.contract)
            result = {'status': 'REVIEWED', 'case_count': len(result['rows'])}
        else:
            result = evaluate(args.report, args.capture_dir, args.review, args.safety, args.lifecycle, args.contract)
            write_once(args.output, result)
        print(json.dumps(result, ensure_ascii=False))
        return int(result.get('status') == 'NOT_PASSED')
    except (OSError, ValueError, KeyError, TypeError, StopIteration):
        print(json.dumps({'status': 'NOT_PASSED', 'reason': 'boundary_input_or_review_invalid'}))
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
