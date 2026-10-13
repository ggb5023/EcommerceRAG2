import importlib.util
import json
import math
from pathlib import Path

import pytest
from quality_capture import capture

SPEC = importlib.util.spec_from_file_location('quality_gate', Path(__file__).with_name('quality_gate.py'))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def save(path, value):
    path.write_text(json.dumps(value))
    return path


def inputs(tmp_path):
    directory = tmp_path / 'private'
    rows, contracts = [], []
    for i in range(53):
        identifier = f'case-{i}'
        raw = capture(directory, {'case_id': identifier, 'query_sha256': 'query-hash',
                                  'evidence': [{'chunk_id': 'chunk'}], 'text': 'synthetic answer'})
        contracts.append({'case_id': identifier, 'risk': 'ordinary' if i < 20 else 'high',
                          'answer_points_total': 1, 'query_sha256': 'query-hash'})
        rows.append({'case_id': identifier, **raw, 'expected_document_ids': ['doc'],
                     'embedding_top5': [{'document_id': 'doc'}], 'rerank_top5': [{'document_id': 'doc'}],
                     'approved_support_chunk_coverage': [{'point_index': 0, 'required_chunk_count': 1,
                                                         'delivered_chunk_count': 1, 'all_delivered': True}]})
    report = save(tmp_path / 'report.json', {'report_version': MODULE.REPORT_VERSION, 'status': 'PASS',
        'real_service_acceptance': False, 'm1_connected': False, 'eligible_case_count': 53,
        'excluded_case_count': 7, 'excluded_case_ids': [f'denied-{i}' for i in range(7)],
        'identity': {'cases_sha256': 'cases-hash'}, 'case_contracts': contracts, 'case_results': rows})
    review = MODULE.review_template(report)
    for row in review['rows']:
        row.update(review_status='pass', **dict.fromkeys(MODULE.CHECKS, True))
        row['points'][0].update(status='supported', support_chunk_ids=['chunk'])
    review_path = save(tmp_path / 'review.json', review)
    safety = save(tmp_path / 'safety.json', {'report_version': 'v2-quality-safety-v1', 'status': 'PASS',
        'evidence_kind': 'go_http_sse',
        'real_service_acceptance': False, 'lifecycle_report_sha256': 'lifecycle',
        **dict.fromkeys(('database_identity_sha256', 'runtime_identity_sha256', 'ledger_identity_sha256'), 'a' * 64),
        'provider_attempt_before': 10, 'provider_attempt_after': 10,
        'replay_checks': [{'scenario': s, 'passed': True} for s in (
            'shop_grant_removed', 'shop_grant_removed', 'identity_revoked', 'permission_revision_changed')],
        'cases_sha256': 'cases-hash', 'results': [{'case_id': f'denied-{i}', 'passed': True,
            'provider_attempt_delta': 0, 'provider_attempt_before': 10, 'provider_attempt_after': 10,
            'request_reached_provider': False, 'denied_before_turn': True,
            **dict.fromkeys(('restricted_evidence_absent', 'citation_ids_valid', 'evidence_customer_qualified'), True)} for i in range(7)]})
    e2e = save(tmp_path / 'e2e.json', {'report_version': 'v2-quality-e2e-v2', 'status': 'PASS',
        'cases_sha256': 'cases-hash', 'real_service_acceptance': False, 'lifecycle_report_sha256': 'lifecycle',
        'results': [dict(r, status='DONE', **dict.fromkeys(('terminal_persisted', 'successful_terminal',
            'sse_sequence_monotonic', 'idempotent_turn', 'refresh_matches', 'sse_replay_matches',
            'citation_ids_valid'), True)) for r in rows], 'safety_report_sha256': 'safety'})
    bound_safety = MODULE.load_json(safety)
    bound_safety['e2e_report_sha256'] = MODULE.sha256(e2e)
    save(safety, bound_safety)
    http_review = dict(review, reviewer_kind='human', baseline_report_sha256=MODULE.sha256(e2e))
    save(tmp_path / 'http-review.json', http_review)
    return report, review_path, directory, safety, e2e


def evaluate(paths):
    r, review, directory, safety, e2e = paths
    return MODULE.evaluate(r, review, capture_dir=directory, safety_path=safety, e2e_path=e2e,
                           e2e_review_path=r.parent / 'http-review.json', e2e_capture_dir=directory)


def test_rank_metrics_multiple_relevant_documents_and_duplicates():
    rows = [{'expected_document_ids': ['a', 'b', 'c'], 'rerank_top5': [
        {'document_id': 'a'}, {'document_id': 'a'}, {'document_id': 'x'}, {'document_id': 'b'}]}]
    result = MODULE._rank_metrics(rows, 'rerank_hit_at_5')
    assert result['recall_at_5'] == round(2 / 3, 6)
    assert result['mrr'] == 1
    assert result['ndcg_at_5'] == round((1 + 1 / math.log2(5)) / (1 + 1 / math.log2(3) + .5), 6)
    assert result['hit_rate_at_5'] == 1


def test_no_review_no_semantic_claim(tmp_path):
    paths = inputs(tmp_path)
    result = MODULE.evaluate(paths[0])
    assert result['answer_quality_status'] == 'NOT_PASSED'
    assert result['semantic']['status'] == 'NOT_RUN'
    assert result['real_service_acceptance'] is False


def test_full_layered_gate_and_missing_e2e_safety(tmp_path):
    paths = inputs(tmp_path)
    assert evaluate(paths)['answer_quality_status'] == 'PASS'
    result = MODULE.evaluate(paths[0], paths[1], capture_dir=paths[2])
    assert result['component_quality_status'] == 'PASS'
    assert result['answer_quality_status'] == 'NOT_PASSED'
    assert 'seven_case_safety_evidence_missing' in result['gate_reasons']


def test_component_approval_cannot_approve_new_http_answers_or_offline_safety(tmp_path):
    r, review, directory, safety, e2e = inputs(tmp_path)
    offline = MODULE.load_json(safety)
    offline.pop('evidence_kind')
    save(safety, offline)
    result = MODULE.evaluate(r, review, capture_dir=directory, safety_path=safety, e2e_path=e2e)
    assert result['component_quality_status'] == 'PASS'
    assert result['answer_quality_status'] == 'NOT_PASSED'
    assert 'entrypoint_safety_evidence_missing' in result['gate_reasons']
    assert 'e2e_semantic_review_missing' in result['gate_reasons']


def test_http_semantic_scores_actual_points_and_never_exports_review_notes(tmp_path):
    paths = inputs(tmp_path)
    review_path = paths[0].parent / 'http-review.json'
    review = MODULE.load_json(review_path)
    for index, state in enumerate(('omitted', 'contradicted', 'distorted')):
        row = review['rows'][index]
        row.update(review_status='fail', notes='private reviewer text')
        row['points'][0].update(status=state)
    review['rows'][25].update(review_status='fail', refusal_clarification_correct=False)
    save(review_path, review)
    report = MODULE.load_json(paths[0])
    for index in (24, 25):
        report['case_contracts'][index]['requires_refusal_or_clarification'] = True
    save(paths[0], report)
    component_review = MODULE.load_json(paths[1])
    component_review['baseline_report_sha256'] = MODULE.sha256(paths[0])
    save(paths[1], component_review)
    result = evaluate(paths)
    assert result['semantic']['passed_cases'] == 53
    http = result['e2e_semantic']
    assert http['reviewed_cases'] == 53 and http['passed_cases'] == 49
    assert http['failed_case_ids'] == ['case-0', 'case-1', 'case-2', 'case-25']
    assert http['high_risk_failure_case_ids'] == ['case-25']
    assert http['answer_point_support_rate'] == 50 / 53
    assert http['omission_rate'] == http['contradiction_rate'] == http['distortion_rate'] == 1 / 53
    assert http['refusal_clarification_cases'] == 2
    assert http['refusal_clarification_correct_rate'] == .5
    assert result['answer_quality_status'] == 'NOT_PASSED'
    assert 'private reviewer text' not in json.dumps(result)


@pytest.mark.parametrize('defect', ['binding', 'replay', 'provider', 'lifecycle'])
def test_http_safety_label_cannot_hide_missing_runtime_evidence(tmp_path, defect):
    paths = inputs(tmp_path)
    safety = MODULE.load_json(paths[3])
    if defect == 'binding':
        safety['e2e_report_sha256'] = 'different-run'
    elif defect == 'replay':
        safety['replay_checks'] = []
    elif defect == 'provider':
        safety['results'][0]['request_reached_provider'] = True
    else:
        safety['lifecycle_report_sha256'] = 'different-database'
    save(paths[3], safety)
    result = evaluate(paths)
    assert result['answer_quality_status'] == 'NOT_PASSED'
    assert 'entrypoint_safety_binding_or_execution_invalid' in result['gate_reasons']


@pytest.mark.parametrize('authorized', [False, True])
def test_delegated_review_keeps_agent_provenance_and_requires_authorization(tmp_path, authorized):
    paths = inputs(tmp_path)
    review_path = paths[0].parent / 'http-review.json'
    review = MODULE.load_json(review_path)
    review.update(reviewer_kind='agent', review_method='delegated_agent_semantic_review_v1',
                  review_completed=True, human_review_completed=False, agent_attestation_sha256='b' * 64)
    if authorized:
        review['delegation_authorization'] = 'explicit_user_instruction'
    save(review_path, review)
    result = evaluate(paths)
    assert result['answer_quality_status'] == ('PASS' if authorized else 'NOT_PASSED')
    if authorized:
        assert result['e2e_semantic']['reviewer_kind'] == 'agent'
    else:
        assert 'e2e_semantic_review_invalid' in result['gate_reasons']


def test_ordinary_ninety_percent_allows_two_failures(tmp_path):
    paths = inputs(tmp_path)
    review = MODULE.load_json(paths[1])
    for row in review['rows'][:2]:
        row.update(review_status='fail', citation_correct=False)
        row['points'][0].update(status='omitted', support_chunk_ids=[])
    save(paths[1], review)
    result = evaluate(paths)
    assert result['semantic']['ordinary_pass_rate'] == .9
    assert result['answer_quality_status'] == 'PASS'
    review['rows'][2]['review_status'] = 'fail'
    save(paths[1], review)
    assert evaluate(paths)['answer_quality_status'] == 'NOT_PASSED'


@pytest.mark.parametrize('change', ['high_failure', 'security', 'risk', 'support', 'contradiction', 'count', 'pending', 'duplicate', 'qualifier'])
def test_review_cannot_forge_pass(tmp_path, change):
    paths = inputs(tmp_path)
    review = MODULE.load_json(paths[1])
    row = review['rows'][25]
    if change == 'high_failure':
        row['review_status'] = 'fail'
    elif change == 'security':
        row['security_error'] = True
    elif change == 'risk':
        row['risk'] = 'ordinary'
    elif change == 'support':
        row['points'][0]['support_chunk_ids'] = ['not-delivered']
    elif change == 'contradiction':
        row['points'][0]['status'] = 'contradicted'
    elif change == 'count':
        row['points'].append(row['points'][0])
    elif change == 'pending':
        row['review_status'] = 'not_reviewed'
    elif change == 'duplicate':
        review['rows'][26] = row
    elif change == 'qualifier':
        row['qualifiers_preserved'] = False
    save(paths[1], review)
    assert evaluate(paths)['answer_quality_status'] == 'NOT_PASSED'


def test_answer_tamper_detected(tmp_path):
    paths = inputs(tmp_path)
    report = MODULE.load_json(paths[0])
    raw = paths[2] / report['case_results'][0]['capture_id']
    raw.write_text('{}')
    result = evaluate(paths)
    assert 'answer_capture_hash_mismatch' in result['gate_reasons']


@pytest.mark.parametrize('change', ['duplicate_case', 'missing_support', 'false_coverage', 'incomplete_contract'])
def test_report_cannot_drop_failed_cases(tmp_path, change):
    paths = inputs(tmp_path)
    report = MODULE.load_json(paths[0])
    if change == 'duplicate_case':
        report['case_results'][1] = report['case_results'][0]
    elif change == 'missing_support':
        report['case_results'][0]['approved_support_chunk_coverage'] = []
    elif change == 'false_coverage':
        report['case_results'][0]['approved_support_chunk_coverage'][0]['delivered_chunk_count'] = 0
    elif change == 'incomplete_contract':
        report['case_contracts'].pop()
    save(paths[0], report)
    assert evaluate(paths)['answer_quality_status'] == 'NOT_PASSED'


def test_safety_paid_call_blocks_gate(tmp_path):
    paths = inputs(tmp_path)
    safety = MODULE.load_json(paths[3])
    safety['results'][0]['provider_attempt_delta'] = 1
    save(paths[3], safety)
    assert 'safety_evidence_invalid' in evaluate(paths)['gate_reasons']


def test_report_write_does_not_overwrite(tmp_path):
    path = tmp_path / 'result.json'
    MODULE.write_report(path, {'value': 1})
    with pytest.raises(FileExistsError):
        MODULE.write_report(path, {'value': 2})
    assert MODULE.load_json(path) == {'value': 1}
