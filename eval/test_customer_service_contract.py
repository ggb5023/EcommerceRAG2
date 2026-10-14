"""The customer rail is frozen before generation and cannot pass unreviewed."""
import hashlib
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]


def test_frozen_customer_cases_and_sources():
    data=json.loads((ROOT/'eval/customer-service-cases.v1.json').read_text())
    ids=set()
    for path,digest in data['source_files'].items():
        assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==digest
    for case in data['cases']:
        assert case['case_id'] not in ids
        ids.add(case['case_id'])
        assert hashlib.sha256(case['query'].encode()).hexdigest()==case['query_sha256']
        assert case['expected_behavior'] in {'ANSWER','CLARIFY','HANDOFF'}
        assert case['required_support']
    assert len(ids)==15
    assert data['reviewer_kind']=='agent' and data['human_review_completed'] is False
    assert data['real_service_acceptance'] is False
