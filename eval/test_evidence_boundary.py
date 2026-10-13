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
    for row in rows:
        cap = m.load(caps / row['capture_id'])
        for e in cap['result']['evidence']: e['versionId'] = '1'
        put(caps / row['capture_id'], cap); row['capture_sha256'] = m.sha256(caps / row['capture_id'])
        next(r for r in reviews if r['case_id'] == row['case_id'])['capture_sha256'] = row['capture_sha256']
    indexed = [dict(evidence_id=e['id'], document_id=e['documentId'], version_id='1', chunk_sha256=m.digest(e['snippet']))
               for row in rows for e in m.load(caps / row['capture_id'])['result']['evidence']]
    lifecycle = put(tmp_path / 'lifecycle.json', {'status': 'PASS', 'indexed_chunks': indexed,
                   'corpus': {'corpus_sha256': contract['bindings']['corpus_sha256']}})
    report = put(tmp_path / 'report.json', {'report_version': 'v2-quality-e2e-v2', 'case_count': 53, 'results': rows,
                 'cases_sha256': contract['bindings']['cases_sha256'], 'scenarios_sha256': contract['bindings']['scenarios_sha256'],
                 'boundary_contract_sha256': m.sha256(m.DEFAULT_CONTRACT), 'real_service_acceptance': False,
                 'temporal_mode': 'historical', 'provider_identity': {'business_date': '2026-09-29'},
                 'lifecycle_report_sha256': m.sha256(lifecycle)})
    manifest = put(tmp_path / 'manifest.json', {'boundary_contract_sha256': m.sha256(m.DEFAULT_CONTRACT)})
    attestation = put(tmp_path / 'attestation.json', {
        'attestation_version': 'v2-agent-review-attestation-v1', 'review_method': 'delegated_agent_semantic_review_v1',
        'reviewer_kind': 'agent', 'human_review_completed': False, 'review_completed': True,
        'delegation_authorization': 'explicit_user_instruction', 'baseline_report_sha256': m.sha256(report),
        'cases_sha256': contract['bindings']['cases_sha256'], 'boundary_contract_sha256': m.sha256(m.DEFAULT_CONTRACT),
        'review_package_manifest_sha256': m.sha256(manifest), 'real_service_acceptance': False})
    for row, rule in zip(reviews, contract['cases']):
        row['original_point_states'] = ['omitted'] * len(rule['original_point_sha256'])
    original = put(tmp_path / 'original.json', {
        'agent_attestation_sha256': m.sha256(attestation), 'review_package_manifest_sha256': m.sha256(manifest),
        'baseline_report_sha256': m.sha256(report), 'cases_sha256': contract['bindings']['cases_sha256'],
        'reviewer_kind': 'agent', 'human_review_completed': False, 'review_completed': True,
        'rows': [dict(case_id=r['case_id'], capture_sha256=r['capture_sha256'], points=[dict(status='omitted', expected_sha256=h)
                  for h in rule['original_point_sha256']]) for r, rule in zip(reviews, contract['cases'])]})
    review = put(tmp_path / 'review.json', {'review_version': 'v2-evidence-boundary-review-v1', 'rows': reviews,
                 'original_review_sha256': m.sha256(original), 'agent_attestation_sha256': m.sha256(attestation),
                 'contract_sha256': m.sha256(m.DEFAULT_CONTRACT), 'e2e_report_sha256': m.sha256(report),
                 'reviewer_kind': 'agent', 'human_review_completed': False, 'review_completed': True,
                 'delegation_authorization': 'explicit_user_instruction', 'real_service_acceptance': False})
    safety = put(tmp_path / 'safety.json', {'status': 'PASS', 'evidence_kind': 'go_http_sse',
                 'e2e_report_sha256': m.sha256(report), 'lifecycle_report_sha256': m.sha256(lifecycle),
                 'cases_sha256': contract['bindings']['cases_sha256'], 'real_service_acceptance': False,
                 'database_identity_sha256': 'a'*64, 'runtime_identity_sha256': 'b'*64, 'ledger_identity_sha256': 'c'*64,
                 'provider_attempt_before': 10, 'provider_attempt_after': 10,
                 'results': [{'case_id': i, 'passed': True, 'provider_attempt_before': 10,
                              'provider_attempt_after': 10, 'provider_attempt_delta': 0, 'request_reached_provider': False,
                              'restricted_evidence_absent': True, 'citation_ids_valid': True,
                              'evidence_customer_qualified': True, 'denied_before_turn': True} for i in contract['excluded_case_ids']],
                 'replay_checks': [{'passed': True, 'scenario': s, 'observations': [dict(endpoint=e, http_status=403, error_code='permission_changed')
                                    for e in ['turn_result', 'events']]} for s in ['shop_grant_removed', 'shop_grant_removed',
                     'identity_revoked', 'permission_revision_changed']]})
    return contract, list(docs.values()), report, caps, review, safety, lifecycle


def run(f):
    contract, docs, *args = f
    with patch.object(m, 'validate_contract', return_value=contract), patch.object(m, 'jsonl', return_value=docs):
        report = m.load(args[0]); review = m.load(args[2]); root = args[0].parent
        original = m.load(root / 'original.json'); attestation = m.load(root / 'attestation.json')
        for row in original['rows']:
            row['capture_sha256'] = next(r['capture_sha256'] for r in report['results'] if r['case_id'] == row['case_id'])
        attestation['baseline_report_sha256'] = m.sha256(args[0]); put(root / 'attestation.json', attestation)
        original['baseline_report_sha256'] = m.sha256(args[0]); original['agent_attestation_sha256'] = m.sha256(root / 'attestation.json')
        put(root / 'original.json', original)
        review['agent_attestation_sha256'] = m.sha256(root / 'attestation.json'); review['original_review_sha256'] = m.sha256(root / 'original.json')
        put(args[2], review)
        return m.evaluate(*args, original_review_path=root / 'original.json', attestation_path=root / 'attestation.json', manifest_path=root / 'manifest.json')


def test_independent_gate_keeps_original_omissions_and_excludes_nonapplicable_citations(tmp_path):
    result = run(fixture(tmp_path))
    assert result['status'] == 'PASS'
    assert result['original_point_states']['omitted'] >= 53
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


@pytest.mark.parametrize('field', ['provider_attempt_before', 'provider_attempt_after', 'provider_attempt_delta', 'restricted_evidence_absent'])
def test_missing_safety_observations_cannot_pass(tmp_path, field):
    f = fixture(tmp_path)
    safety = m.load(f[5]); safety['results'][0].pop(field); put(f[5], safety)
    assert run(f)['status'] == 'NOT_PASSED'


@pytest.mark.parametrize('change', ['not_reviewed', 'wrong_time', 'wrong_version', 'empty_replay', 'original_states'])
def test_semantic_time_version_and_replay_are_mandatory(tmp_path, change):
    f = fixture(tmp_path)
    if change == 'not_reviewed':
        r = m.load(f[4]); r['rows'][0]['review_status'] = 'not_reviewed'; put(f[4], r)
    elif change == 'wrong_time':
        r = m.load(f[2]); r['provider_identity']['business_date'] = '2026-10-13'; put(f[2], r)
    elif change == 'wrong_version':
        lifecycle = m.load(f[6]); lifecycle['indexed_chunks'][0]['version_id'] = 'other'; put(f[6], lifecycle)
    elif change == 'empty_replay':
        safety = m.load(f[5]); safety['replay_checks'][0]['observations'] = []; put(f[5], safety)
    else:
        review = m.load(f[4]); review['rows'][0]['original_point_states'] = ['supported']; put(f[4], review)
    if change in {'wrong_version', 'empty_replay'}: assert run(f)['status'] == 'NOT_PASSED'
    else:
        with pytest.raises(ValueError): run(f)


def test_sidecar_without_provenance_is_rejected(tmp_path):
    f = fixture(tmp_path)
    with patch.object(m, 'validate_contract', return_value=f[0]), pytest.raises(ValueError, match='provenance_missing'):
        m.evaluate(*f[2:])
