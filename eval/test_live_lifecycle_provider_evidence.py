import pytest
from run_v2_live_lifecycle import AcceptanceFailure, validate_provider_evidence


def report(extra_slot='control', extra_count=1):
    slots = ['control', 'embedding', 'rerank', 'generation'] + [extra_slot]*extra_count
    return {'phase_expectations': {'markdown-query': dict.fromkeys(('control', 'embedding', 'rerank', 'generation'), 1)},
        'phase_optional_requests': {'markdown-query': {'control': 1}},
        'phases': [{'phase': 'markdown-query', 'attempt_after': 0, 'attempt_through': len(slots)}],
        'provider_attempts': [{'sequence': i, 'slot': slot, 'model': slot, 'state': 'response', 'http_status': 200,
                              'error_code': None, 'request_id_sha256': 'a'*64, 'usage_json': '{}'}
                             for i, slot in enumerate(slots, 1)],
        'published': {'guide': '1'}, 'indexed_chunks': [{'document_id': 'guide', 'version_id': '1',
        'embedding_model': 'embedding', 'embedding_dim': 1024}]}


@pytest.mark.parametrize('extra', [0, 1])
def test_applicability_control_is_an_explicit_bounded_request(extra):
    validate_provider_evidence(report(extra_count=extra), dict.fromkeys((), '') | {
        slot: slot for slot in ('control', 'embedding', 'rerank', 'generation')})


@pytest.mark.parametrize('slot,count', [('control', 2), ('generation', 1), ('embedding', 1)])
def test_extra_generation_retrieval_or_unbounded_control_is_rejected(slot, count):
    with pytest.raises(AcceptanceFailure, match='coverage'):
        validate_provider_evidence(report(slot, count), {slot: slot for slot in ('control', 'embedding', 'rerank', 'generation')})


def test_optional_control_still_requires_successful_paid_evidence():
    r = report();r['provider_attempts'][-1]['http_status'] = 500
    with pytest.raises(AcceptanceFailure, match='incomplete or failed'):
        validate_provider_evidence(r, {slot: slot for slot in ('control', 'embedding', 'rerank', 'generation')})
