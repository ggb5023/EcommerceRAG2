import hashlib
import json

import pytest
from customer_service_quality import FIELDS, score


def package(tmp_path):
    c = {'case_id': 'example', 'query': 'Question', 'expected_behavior': 'ANSWER', 'high_risk': True}
    contract = tmp_path / 'contract.json'
    contract.write_text(json.dumps({'cases': [c]}))
    capture = tmp_path / 'example.json'
    capture.write_text(json.dumps({'case': c, 'actual_behaviors': ['ANSWER'],
                                  'snapshot': {'messages': [{'source': 'assistant', 'citations': [{'id': 'c'}]}]}}))
    review = {'contract_sha256': hashlib.sha256(contract.read_bytes()).hexdigest(),
              'reviewer_kind': 'agent', 'human_review_completed': False,
              'cases': [{'case_id': 'example', 'capture_sha256': hashlib.sha256(capture.read_bytes()).hexdigest(),
                         **dict.fromkeys(FIELDS, True), 'severe_safety_error': False}]}
    return contract, capture, review


@pytest.mark.parametrize('fault', ['missing', 'changed_capture', 'failed', 'wrong_decision', 'unsafe', 'omission', 'no_citation'])
def test_gate_fails_closed(tmp_path, fault):
    contract, capture, review = package(tmp_path)
    if fault == 'missing':
        review['cases'] = []
    elif fault == 'changed_capture':
        capture.write_text('{}')
    elif fault == 'unsafe':
        review['cases'][0]['severe_safety_error'] = True
    elif fault == 'omission':
        review['cases'][0]['required_support_delivered'] = False
    else:
        data = json.loads(capture.read_text())
        if fault == 'failed':
            data['events'] = [{'data': {'reason_code': 'provider_failed_search'}}]
        elif fault == 'wrong_decision':
            data['actual_behaviors'] = ['HANDOFF']
        else:
            data['snapshot']['messages'][0]['citations'] = []
        capture.write_text(json.dumps(data))
        review['cases'][0]['capture_sha256'] = hashlib.sha256(capture.read_bytes()).hexdigest()
    assert score(contract, tmp_path, review)['status'] == 'NOT_PASSED'


def test_bound_review_passes_and_cannot_claim_human(tmp_path):
    contract, _, review = package(tmp_path)
    assert score(contract, tmp_path, review)['status'] == 'PASS'
    review['human_review_completed'] = True
    with pytest.raises(ValueError, match='provenance'):
        score(contract, tmp_path, review)
