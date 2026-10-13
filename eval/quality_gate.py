#!/usr/bin/env python3
"""Fail-closed, layered scoring of frozen synthetic V2 evaluation evidence."""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import tempfile
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from quality_capture import secure_directory

REVIEW_VERSION = 'v2-quality-review-v2'
REPORT_VERSION = 'bailian-batch-v1'
REQUIRED_CASES = 53
REQUIRED_EXCLUDED = 7
POINT_STATES = {'supported', 'omitted', 'contradicted', 'distorted'}
CHECKS = ('qualifiers_preserved', 'refusal_clarification_correct', 'freshness_source_correct',
          'copy_eligibility_correct', 'citation_correct')


def _reject_duplicate_keys(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate_json_key')
        result[key] = value
    return result


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=_reject_duplicate_keys)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _rank_metrics(rows: Sequence[Mapping[str, Any]], field: str) -> dict:
    """Macro document recall and binary nDCG; repeated chunks add no gain.

    Keep original chunk positions: duplicates consume a position but cannot
    inflate document recall or DCG. MRR is the first relevant chunk's rank.
    """
    recall = reciprocal = dcg_total = hits = 0.0
    evaluated = 0
    for row in rows:
        expected = set(row.get('expected_document_ids', []))
        ranked = row.get(field.replace('_hit_at_5', '_top5'), [])
        if not expected or not isinstance(ranked, list):
            continue
        seen = set()
        relevance = []
        for item in ranked[:5]:
            identifier = item.get('document_id') if isinstance(item, Mapping) else None
            relevance.append(int(identifier in expected and identifier not in seen))
            seen.add(identifier)
        evaluated += 1
        recall += len(expected & seen) / len(expected)
        if any(relevance):
            hits += 1
            reciprocal += 1 / (relevance.index(1) + 1)
        dcg = sum(gain / math.log2(i + 2) for i, gain in enumerate(relevance))
        ideal = sum(1 / math.log2(i + 2) for i in range(min(5, len(expected))))
        dcg_total += dcg / ideal
    return {'evaluated_cases': evaluated,
            **{name: round(value / evaluated, 6) if evaluated else None for name, value in (
                ('recall_at_5', recall), ('hit_rate_at_5', hits), ('mrr', reciprocal), ('ndcg_at_5', dcg_total))}}


def _contracts(report: Mapping) -> dict:
    contracts = report.get('case_contracts')
    if not isinstance(contracts, list) or len(contracts) != REQUIRED_CASES:
        raise ValueError('frozen_case_contracts_missing')
    result = {}
    for row in contracts:
        if not isinstance(row, Mapping) or not isinstance(row.get('case_id'), str) or row['case_id'] in result:
            raise ValueError('frozen_case_contracts_invalid')
        total = row.get('answer_points_total')
        if row.get('risk') not in {'high', 'ordinary'} or type(total) is not int or total < 1:
            raise ValueError('frozen_case_contracts_invalid')
        result[row['case_id']] = row
    return result


def _evidence_metrics(rows: Sequence[Mapping], contracts: Mapping) -> dict:
    covered = delivered = required = 0
    for row in rows:
        coverage = row.get('approved_support_chunk_coverage')
        count = contracts[row['case_id']]['answer_points_total']
        if not isinstance(coverage, list) or len(coverage) != count:
            raise ValueError('support_coverage_incomplete')
        all_covered = True
        for index, item in enumerate(coverage):
            if not isinstance(item, Mapping) or item.get('point_index') != index:
                raise ValueError('support_coverage_invalid')
            need, got = item.get('required_chunk_count'), item.get('delivered_chunk_count')
            if type(need) is not int or type(got) is not int or need < 1 or not 0 <= got <= need:
                raise ValueError('support_coverage_invalid')
            if item.get('all_delivered') is not (got == need):
                raise ValueError('support_coverage_inconsistent')
            required += need
            delivered += got
            all_covered &= got == need
        covered += all_covered
    return {'cases_with_approved_support': len(rows), 'cases_with_all_support_delivered': covered,
            'approved_support_delivery_rate': round(covered / len(rows), 6) if rows else None,
            'bound_chunk_delivery_rate': round(delivered / required, 6) if required else None}


def _load_review(path: Path, report: Mapping, contracts: Mapping, capture_dir: Path | None,
                 frozen_points: Mapping[str, Sequence[str]] | None = None) -> dict:
    review = load_json(path)
    if (not isinstance(review, Mapping) or review.get('review_version') != REVIEW_VERSION
            or review.get('baseline_report_sha256') != report['report_sha256']):
        raise ValueError('review_report_binding_invalid')
    if review.get('reviewer_kind') not in {'engineering', 'human', 'agent'}:
        raise ValueError('reviewer_kind_invalid')
    if review.get('reviewer_kind') == 'agent' and (
            review.get('review_method') != 'delegated_agent_semantic_review_v1'
            or review.get('delegation_authorization') != 'explicit_user_instruction'
            or review.get('review_completed') is not True or review.get('human_review_completed') is not False
            or not isinstance(review.get('agent_attestation_sha256'), str) or len(review['agent_attestation_sha256']) != 64):
        raise ValueError('delegated_agent_review_attestation_missing')
    if capture_dir is None:
        raise ValueError('review_capture_directory_required')
    secure_directory(capture_dir)
    answers = {r['case_id']: r for r in report['case_results']}
    reviewed = review.get('rows')
    if not isinstance(reviewed, list) or len(reviewed) != len(contracts):
        raise ValueError('review_case_set_incomplete')
    seen = set()
    for row in reviewed:
        if not isinstance(row, Mapping) or row.get('case_id') not in contracts or row['case_id'] in seen:
            raise ValueError('review_case_id_invalid')
        identifier = row['case_id']
        seen.add(identifier)
        answer = answers[identifier]
        contract = contracts[identifier]
        name = answer.get('capture_id')
        if not isinstance(name, str) or Path(name).name != name or not name.endswith('.json'):
            raise ValueError('answer_capture_missing')
        file = capture_dir / name
        if file.is_symlink() or file.stat().st_mode & 0o777 != 0o600 or file.stat().st_uid != os.getuid():
            raise ValueError('answer_capture_permissions_invalid')
        if row.get('capture_sha256') != answer.get('capture_sha256') or sha256(file) != answer['capture_sha256']:
            raise ValueError('answer_capture_hash_mismatch')
        captured = load_json(file)
        if captured.get('case_id') != identifier or captured.get('query_sha256') != contract.get('query_sha256'):
            raise ValueError('answer_capture_identity_mismatch')
        if row.get('risk') != contract['risk']:
            raise ValueError('review_risk_mismatch')
        state = row.get('review_status')
        if state not in {'pass', 'fail', 'not_reviewed'}:
            raise ValueError('review_status_invalid')
        if type(row.get('security_error')) is not bool or any(type(row.get(c)) is not bool for c in CHECKS):
            raise ValueError('review_boolean_invalid')
        points = row.get('points')
        if not isinstance(points, list) or len(points) != contract['answer_points_total']:
            raise ValueError('review_point_count_mismatch')
        evidence_ids = ({c['id'] for c in captured.get('result', {}).get('evidence', [])}
                        if captured.get('capture_version') == 'v2-http-quality-capture-v1'
                        else {c['chunk_id'] for c in captured.get('evidence', [])})
        for index, point in enumerate(points):
            if not isinstance(point, Mapping) or point.get('point_index') != index or point.get('status') not in POINT_STATES:
                raise ValueError('review_point_status_invalid')
            if frozen_points is not None:
                expected_hash = hashlib.sha256(frozen_points[identifier][index].encode()).hexdigest()
                if point.get('expected_sha256') != expected_hash:
                    raise ValueError('review_truth_binding_invalid')
            support = point.get('support_chunk_ids')
            if not isinstance(support, list) or len(set(support)) != len(support) or not set(support).issubset(evidence_ids):
                raise ValueError('review_support_not_delivered')
            if point['status'] == 'supported' and not support:
                raise ValueError('review_supported_without_evidence')
        qualifies = (not row['security_error'] and all(row[c] for c in CHECKS)
                     and any(p['status'] == 'supported' for p in points)
                     and not any(p['status'] in {'contradicted', 'distorted'} for p in points))
        if state == 'pass' and not qualifies:
            raise ValueError('review_pass_inconsistent')
    return dict(review)


def evaluate(report_path: Path, review_path: Path | None = None, *, capture_dir: Path | None = None,
             safety_path: Path | None = None, e2e_path: Path | None = None,
             cases_path: Path | None = None, e2e_review_path: Path | None = None,
             e2e_capture_dir: Path | None = None) -> dict:
    report = load_json(report_path)
    if not isinstance(report, Mapping):
        raise TypeError('report_root_not_object')
    report = dict(report, report_sha256=sha256(report_path))
    issues = []
    for field, expected in (('report_version', REPORT_VERSION), ('status', 'PASS'),
                            ('real_service_acceptance', False), ('m1_connected', False),
                            ('eligible_case_count', REQUIRED_CASES), ('excluded_case_count', REQUIRED_EXCLUDED)):
        if report.get(field) != expected:
            issues.append(field + '_invalid')
    rows = report.get('case_results', [])
    evidence = {'approved_support_delivery_rate': None}
    semantic = {'status': 'NOT_RUN', 'reviewed_cases': 0}
    contracts = {}
    frozen_points = None
    if cases_path is not None:
        frozen_points = {}
        for line in cases_path.read_text(encoding='utf-8').splitlines():
            if line.strip():
                case = json.loads(line)
                frozen_points[str(case['case_id'])] = [str(point) for point in case.get('expected_answer_points', [])]
    try:
        contracts = _contracts(report)
        if (not isinstance(rows, list) or len(rows) != REQUIRED_CASES
                or any(not isinstance(r, Mapping) for r in rows)
                or len({r.get('case_id') for r in rows}) != REQUIRED_CASES
                or {r.get('case_id') for r in rows} != set(contracts)):
            raise ValueError('case_result_set_invalid')
        for row in rows:
            if not row.get('expected_document_ids') or not isinstance(row.get('expected_document_ids'), list):
                raise ValueError('ranking_truth_missing')
            for field in ('embedding_top5', 'rerank_top5'):
                ranked = row.get(field)
                if (not isinstance(ranked, list) or len(ranked) > 5
                        or any(not isinstance(r, Mapping) or not r.get('document_id') for r in ranked)):
                    raise ValueError('ranking_rows_invalid')
        evidence = _evidence_metrics(rows, contracts)
        if review_path is not None:
            review = _load_review(review_path, report, contracts, capture_dir, frozen_points)
            reviewed = [r for r in review['rows'] if r['review_status'] != 'not_reviewed']
            ordinary = [r for r in reviewed if r['risk'] == 'ordinary']
            high = [r for r in review['rows'] if r['risk'] == 'high']
            points = [p for r in reviewed for p in r['points']]
            semantic = {'status': 'COMPLETE' if len(reviewed) == REQUIRED_CASES else 'INCOMPLETE',
                        'reviewer_kind': review['reviewer_kind'], 'reviewed_cases': len(reviewed),
                        'passed_cases': sum(r['review_status'] == 'pass' for r in reviewed),
                        'not_reviewed_cases': REQUIRED_CASES - len(reviewed),
                        'security_errors': sum(r['security_error'] for r in reviewed),
                        'high_risk_failures': sum(r['review_status'] != 'pass' for r in high),
                        'ordinary_pass_rate': sum(r['review_status'] == 'pass' for r in ordinary) / len(ordinary) if ordinary else None,
                        'citation_validity_rate': sum(r['citation_correct'] for r in reviewed) / len(reviewed) if reviewed else None,
                        **{key: sum(p['status'] == status for p in points) / len(points) if points else None
                           for key, status in (('answer_point_support_rate', 'supported'), ('omission_rate', 'omitted'),
                                               ('contradiction_rate', 'contradicted'), ('distortion_rate', 'distorted'))}}
    except (OSError, ValueError, TypeError, KeyError) as error:
        issues.append(str(error) if isinstance(error, ValueError) else 'review_or_report_invalid')
    retrieval = {name: _rank_metrics([r for r in rows if isinstance(r, Mapping)], name + '_hit_at_5')
                 for name in ('embedding', 'rerank')} if isinstance(rows, list) else {}
    reasons = list(issues)
    if evidence['approved_support_delivery_rate'] is None or evidence['approved_support_delivery_rate'] < .95:
        reasons.append('approved_support_delivery_below_0.95')
    if semantic['status'] != 'COMPLETE':
        reasons.append('semantic_review_incomplete')
    if semantic.get('security_errors') or semantic.get('high_risk_failures'):
        reasons.append('security_or_high_risk_failure')
    if semantic.get('ordinary_pass_rate') is not None and semantic['ordinary_pass_rate'] < .90:
        reasons.append('ordinary_case_pass_rate_below_0.90')
    if semantic.get('citation_validity_rate') is None or semantic['citation_validity_rate'] < .95:
        reasons.append('citation_validity_below_0.95')
    component_reasons = sorted(set(reasons))
    safety = {'status': 'NOT_RUN'}
    if safety_path:
        safety = load_json(safety_path)
        expected = set(report.get('excluded_case_ids', []))
        safety_rows = safety.get('results', [])
        if (safety.get('report_version') != 'v2-quality-safety-v1' or safety.get('status') != 'PASS'
                or len(expected) != REQUIRED_EXCLUDED or len(safety_rows) != REQUIRED_EXCLUDED
                or {r.get('case_id') for r in safety_rows} != expected
                or any(r.get('passed') is not True or r.get('provider_attempt_delta') != 0 for r in safety_rows)
                or safety.get('cases_sha256') != report.get('identity', {}).get('cases_sha256')):
            reasons.append('safety_evidence_invalid')
    else:
        reasons.append('seven_case_safety_evidence_missing')
    e2e = {'status': 'NOT_RUN'}
    http_semantic = {'status': 'NOT_RUN'}
    if e2e_path:
        e2e = load_json(e2e_path)
        if (e2e.get('report_version') != 'v2-quality-e2e-v2' or e2e.get('status') != 'PASS'
                or len(e2e.get('results', [])) != REQUIRED_CASES
                or {r.get('case_id') for r in e2e.get('results', [])} != set(contracts)
                or e2e.get('cases_sha256') != report.get('identity', {}).get('cases_sha256')
                or not e2e.get('safety_report_sha256')
                or e2e.get('real_service_acceptance') is not False):
            reasons.append('e2e_evidence_invalid')
        if any(r.get('status') not in {'DONE', 'ASKING'}
               or any(r.get(check) is not True for check in ('terminal_persisted', 'successful_terminal',
                      'sse_sequence_monotonic', 'idempotent_turn', 'refresh_matches',
                      'sse_replay_matches', 'citation_ids_valid')) for r in e2e.get('results', [])):
            reasons.append('e2e_execution_or_replay_failed')
        if safety.get('evidence_kind') != 'go_http_sse':
            reasons.append('entrypoint_safety_evidence_missing')
        else:
            fingerprint_keys = ('database_identity_sha256', 'runtime_identity_sha256', 'ledger_identity_sha256')
            replay_checks = safety.get('replay_checks', [])
            if (safety.get('e2e_report_sha256') != sha256(e2e_path)
                    or safety.get('lifecycle_report_sha256') != e2e.get('lifecycle_report_sha256')
                    or safety.get('real_service_acceptance') is not False
                    or any(not isinstance(safety.get(key), str) or len(safety[key]) != 64
                           or any(c not in '0123456789abcdef' for c in safety[key]) for key in fingerprint_keys)
                    or type(safety.get('provider_attempt_before')) is not int
                    or safety.get('provider_attempt_after') != safety.get('provider_attempt_before')
                    or len(replay_checks) != 4 or any(r.get('passed') is not True for r in replay_checks)
                    or {r.get('scenario') for r in replay_checks} != {
                        'shop_grant_removed', 'identity_revoked', 'permission_revision_changed'}
                    or any(r.get('request_reached_provider') is not False
                           or r.get('provider_attempt_before') != r.get('provider_attempt_after')
                           or any(r.get(key) is not True for key in (
                               'restricted_evidence_absent', 'citation_ids_valid', 'evidence_customer_qualified'))
                           or not (r.get('denied_before_turn') is True or
                                   r.get('terminal_persisted') is True and r.get('sse_replay_valid') is True)
                           for r in safety_rows)):
                reasons.append('entrypoint_safety_binding_or_execution_invalid')
        if e2e_review_path is None:
            reasons.append('e2e_semantic_review_missing')
        else:
            try:
                review = _load_review(e2e_review_path, dict(e2e, report_sha256=sha256(e2e_path),
                                     case_results=e2e.get('results', [])), contracts, e2e_capture_dir, frozen_points)
                ordinary = [r for r in review['rows'] if r['risk'] == 'ordinary']
                reviewed = [r for r in review['rows'] if r['review_status'] != 'not_reviewed']
                http_semantic = {'status': 'COMPLETE' if len(reviewed) == REQUIRED_CASES else 'INCOMPLETE',
                                 'reviewer_kind': review['reviewer_kind'], 'reviewed_cases': len(reviewed),
                                 'high_risk_failures': sum(r['risk'] == 'high' and r['review_status'] != 'pass' for r in review['rows']),
                                 'ordinary_pass_rate': sum(r['review_status'] == 'pass' for r in ordinary) / len(ordinary) if ordinary else None,
                                 'citation_validity_rate': sum(r['citation_correct'] for r in reviewed) / len(reviewed) if reviewed else None,
                                 'security_errors': sum(r['security_error'] for r in review['rows'])}
                if (review['reviewer_kind'] not in {'human', 'agent'} or http_semantic['status'] != 'COMPLETE'
                        or http_semantic['high_risk_failures'] or http_semantic['security_errors']
                        or http_semantic['ordinary_pass_rate'] is None or http_semantic['ordinary_pass_rate'] < .9
                        or http_semantic['citation_validity_rate'] is None or http_semantic['citation_validity_rate'] < .95):
                    reasons.append('e2e_semantic_review_not_passed')
            except (OSError, ValueError, TypeError, KeyError):
                reasons.append('e2e_semantic_review_invalid')
    else:
        reasons.append('v2_e2e_evidence_missing')
    return {'report_version': 'v2-quality-gate-v2', 'baseline_report_sha256': report['report_sha256'],
            'provider_identity': report.get('provider_identity', {}), 'retrieval': retrieval,
            'evidence': evidence, 'semantic': semantic, 'safety_status': safety.get('status'),
            'e2e_status': e2e.get('status'), 'e2e_semantic': http_semantic,
            'component_quality_status': 'PASS' if not component_reasons else 'NOT_PASSED',
            'answer_quality_status': 'PASS' if not reasons else 'NOT_PASSED',
            'model_quality_claim': False, 'real_service_acceptance': False, 'gate_reasons': sorted(set(reasons))}


def review_template(report_path: Path) -> dict:
    report = load_json(report_path)
    contracts = _contracts(report)
    answers = {r['case_id']: r for r in report['case_results']}
    return {'review_version': REVIEW_VERSION, 'baseline_report_sha256': sha256(report_path),
            'reviewer_kind': 'engineering', 'rows': [
                {'case_id': identifier, 'risk': c['risk'], 'capture_sha256': answers[identifier].get('capture_sha256'),
                 'review_status': 'not_reviewed', 'security_error': False, **dict.fromkeys(CHECKS, False),
                 'points': [{'point_index': i, 'status': 'omitted', 'support_chunk_ids': []}
                            for i in range(c['answer_points_total'])], 'issue_codes': []}
                for identifier, c in contracts.items()]}


def write_report(path: Path, payload: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=f'.{path.name}.', dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            json.dump(payload, stream, ensure_ascii=False, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--review', type=Path)
    parser.add_argument('--capture-dir', type=Path)
    parser.add_argument('--safety-report', type=Path)
    parser.add_argument('--e2e-report', type=Path)
    parser.add_argument('--cases', type=Path)
    parser.add_argument('--e2e-review', type=Path)
    parser.add_argument('--e2e-capture-dir', type=Path)
    parser.add_argument('--template', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    try:
        if args.template:
            write_report(args.template, review_template(args.report))
            print(json.dumps({'status': 'TEMPLATE'}))
            return 0
        result = evaluate(args.report, args.review, capture_dir=args.capture_dir,
                          safety_path=args.safety_report, e2e_path=args.e2e_report,
                          cases_path=args.cases, e2e_review_path=args.e2e_review,
                          e2e_capture_dir=args.e2e_capture_dir)
        if args.output:
            write_report(args.output, result)
        print(json.dumps(result, ensure_ascii=False))
        return 0 if result['answer_quality_status'] == 'PASS' else 4
    except (OSError, ValueError, TypeError, KeyError):
        print(json.dumps({'status': 'FAIL', 'issues': ['invalid_input_or_output'], 'real_service_acceptance': False}))
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
