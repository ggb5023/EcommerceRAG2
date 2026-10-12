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
        'cases_sha256': 'cases-hash', 'results': [{'case_id': f'denied-{i}', 'passed': True,
                                               'provider_attempt_delta': 0} for i in range(7)]})
    e2e = save(tmp_path / 'e2e.json', {'report_version': 'v2-quality-e2e-v2', 'status': 'PASS',
        'cases_sha256': 'cases-hash', 'real_service_acceptance': False,
        'results': [{'case_id': r['case_id']} for r in rows], 'safety_report_sha256': 'safety'})
    return report, review_path, directory, safety, e2e


def evaluate(paths):
    r, review, directory, safety, e2e = paths
    return MODULE.evaluate(r, review, capture_dir=directory, safety_path=safety, e2e_path=e2e)


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
