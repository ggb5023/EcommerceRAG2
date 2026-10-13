"""Reject forged acceptance, missing evidence and safety/result binding failures."""
import copy
import json
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

sys.path.insert(0, str(Path(__file__).parent))
import evidence_boundary as m


def put(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False))
    path.chmod(0o600)
    return path


def fixture(tmp_path):
    contract = m.load(m.DEFAULT_CONTRACT)
    caps = tmp_path / 'capture'
    caps.mkdir(mode=0o700)
    rows, reviews, docs = [], [], {}
    for rule in contract['cases']:
        evidence = []
        for n, support in enumerate(rule['required_support']):
            text = 'controlled substantive source ' + support['chunk_id']
            docs.setdefault(support['document_id'], {'document_id': support['document_id'], 'chunks': []})['chunks'].append({'chunk_id': support['chunk_id'], 'content': text})
            evidence.append({'id': f"{rule['case_id']}-{n}", 'documentId': support['document_id'], 'snippet': text})
        status = 'ASKING' if rule['expected_behavior'] == 'clarify' else 'DONE'
        result = {'status': status, 'answer': 'controlled test result', 'evidence': evidence,
                  'citations': [{'evidence_id': e['id']} for e in evidence]}
        capture = {'capture_version': 'v2-http-quality-capture-v1', 'case_id': rule['case_id'],
                   'query_sha256': rule['query_sha256'], 'result': result}
        f = put(caps / (rule['case_id'] + '.json'), capture)
        row = {'case_id': rule['case_id'], 'status': status, 'sse_event_count': 1, 'capture_id': f.name, 'capture_sha256': m.sha256(f),
               'answer_sha256': m.digest(result['answer']), **dict.fromkeys(('terminal_persisted', 'successful_terminal',
                    'sse_sequence_monotonic', 'idempotent_turn', 'refresh_matches', 'sse_replay_matches', 'citation_ids_valid'), True)}
        rows.append(row)
        reviews.append({'case_id': rule['case_id'], 'risk': rule['risk'], 'capture_sha256': row['capture_sha256'],
                        'review_status': 'pass', 'security_error': False, **dict.fromkeys(m.BOUNDARY_CHECKS, True),
                        'requirements': dict.fromkeys(rule['requirements'], True), 'support_chunk_ids': [e['id'] for e in evidence],
                        'original_point_states': ['omitted']})
    lifecycle = put(tmp_path / 'lifecycle.json', {'status': 'PASS', 'corpus': {'corpus_sha256': contract['bindings']['corpus_sha256']}})
    report = put(tmp_path / 'report.json', {'report_version': 'v2-quality-e2e-v2', 'case_count': 53, 'results': rows,
                 'cases_sha256': contract['bindings']['cases_sha256'], 'scenarios_sha256': contract['bindings']['scenarios_sha256'],
                 'boundary_contract_sha256': m.sha256(m.DEFAULT_CONTRACT), 'real_service_acceptance': False,
                 'lifecycle_report_sha256': m.sha256(lifecycle)})
    review = put(tmp_path / 'review.json', {'review_version': 'v2-evidence-boundary-review-v1', 'rows': reviews,
                 'contract_sha256': m.sha256(m.DEFAULT_CONTRACT), 'e2e_report_sha256': m.sha256(report),
                 'reviewer_kind': 'agent', 'human_review_completed': False, 'review_completed': True,
                 'delegation_authorization': 'explicit_user_instruction', 'real_service_acceptance': False})
    safety = put(tmp_path / 'safety.json', {'status': 'PASS', 'evidence_kind': 'go_http_sse',
                 'e2e_report_sha256': m.sha256(report), 'lifecycle_report_sha256': m.sha256(lifecycle),
                 'cases_sha256': contract['bindings']['cases_sha256'], 'real_service_acceptance': False,
                 'provider_attempt_before': 10, 'provider_attempt_after': 10,
                 'results': [{'case_id': i, 'passed': True, 'provider_attempt_before': 10,
                              'provider_attempt_after': 10, 'request_reached_provider': False} for i in contract['excluded_case_ids']],
                 'replay_checks': [{'passed': True}] * 4})
    return contract, list(docs.values()), report, caps, review, safety, lifecycle


def run(f):
    contract, docs, *args = f
    with patch.object(m, 'validate_contract', return_value=contract), patch.object(m, 'jsonl', return_value=docs):
        return m.evaluate(*args)


def test_independent_gate_keeps_original_omissions_and_excludes_nonapplicable_citations(tmp_path):
    result = run(fixture(tmp_path))
    assert result['status'] == 'PASS'
    assert result['original_point_states']['omitted'] == 53
    assert result['citation_not_applicable_cases'] == 4
    assert result['citation_applicable_cases'] == 49
    assert result['human_review_completed'] is False


@pytest.mark.parametrize('change', ['high_failure', 'failed_turn', 'security', 'empty_citation', 'missing_source', 'false_requirement', 'safety_model_request', 'replay_failure'])
def test_gate_fails_without_full_behavior_delivery_execution_and_safety(tmp_path, change):
    f = fixture(tmp_path)
    contract, _, report, caps, review, safety, _ = f
    r = m.load(review)
    idx = next(i for i, row in enumerate(r['rows']) if row['risk'] == 'high' and contract['cases'][i]['required_support'])
    item = r['rows'][idx]
    cid = item['case_id']
    if change == 'high_failure': item['review_status'] = 'fail'
    if change == 'security': item['security_error'] = True
    if change == 'false_requirement': item['requirements'][next(iter(item['requirements']))] = False
    if change in {'failed_turn', 'empty_citation', 'missing_source'}:
        cp = caps / (cid + '.json')
        c = m.load(cp)
        if change == 'failed_turn': c['result']['status'] = 'FAILED'
        if change == 'empty_citation':
            # Affect all applicable cases; one invalid citation alone is below the 5% allowance.
            item['citation_correct'] = False
            for row in r['rows']: row['citation_correct'] = False
        if change == 'missing_source':
            for row in r['rows']:
                cf = caps / (row['case_id'] + '.json'); cv = m.load(cf)
                for evidence in cv['result']['evidence']: evidence['snippet'] = 'title only'
                put(cf, cv)
        put(cp, c) if change != 'missing_source' else None
        rr = m.load(report)
        for row in rr['results']:
            cp = caps / (row['case_id'] + '.json')
            row['capture_sha256'] = m.sha256(cp)
            row['status'] = m.load(cp)['result']['status']
        put(report, rr)
        for row in r['rows']: row['capture_sha256'] = m.sha256(caps / (row['case_id'] + '.json'))
        r['e2e_report_sha256'] = m.sha256(report)
        s = m.load(safety); s['e2e_report_sha256'] = m.sha256(report); put(safety, s)
    if change in {'safety_model_request', 'replay_failure'}:
        s = m.load(safety)
        if change == 'safety_model_request': s['results'][0]['request_reached_provider'] = True
        else: s['replay_checks'][0]['passed'] = False
        put(safety, s)
    put(review, r)
    assert run(f)['status'] == 'NOT_PASSED'


@pytest.mark.parametrize('change', ['changed_capture', 'unreviewed', 'wrong_risk', 'human_impersonation', 'outside_support', 'duplicate_case'])
def test_binding_rejects_tampering(tmp_path, change):
    f = fixture(tmp_path)
    r = m.load(f[4])
    if change == 'changed_capture':
        c = m.load(f[3] / 'syn-001.json'); c['result']['answer'] = 'changed'; put(f[3] / 'syn-001.json', c)
    if change == 'unreviewed': r['rows'][0]['requirements'] = {}
    if change == 'wrong_risk': r['rows'][0]['risk'] = 'high'
    if change == 'human_impersonation': r['human_review_completed'] = True
    if change == 'outside_support': r['rows'][0]['support_chunk_ids'] = ['unauthorized']
    if change == 'duplicate_case': r['rows'][-1] = copy.deepcopy(r['rows'][0])
    put(f[4], r)
    with pytest.raises(ValueError): run(f)


def test_frozen_input_integrity_when_local_approved_inputs_available():
    if not m.DEFAULT_CASES.exists(): pytest.skip('approved inputs intentionally outside Git')
    assert len(m.validate_contract()['cases']) == 53
