import copy
import json
from datetime import datetime, timezone

import pytest
from diagnose_customer_execution import DiagnosticError, diagnose, digest, identity, load_connections, read_execution


def item(number=1, generation=False):
    row = {'id': f'db-chunk-{number}', 'document_id': 'guide', 'version_id': '1', 'rank': number,
           'content': 'PRIVATE SOURCE BODY', 'source_ref': '/private/source'}
    if generation:
        row.update(content_sha256='a'*64, context_sha256='b'*64, request_sha256='c'*64)
    return row


def snapshot():
    return {'identity': identity('1', 'shop', 'conversation', 'execution'), 'state': 'DONE', 'epoch': 1,
            'behavior': 'ANSWER', 'reason_code': 'grounded', 'answer_sha256': 'd'*64,
            'created_at': '2026-10-14T00:00:00+00:00', 'finished_at': '2026-10-14T00:00:01+00:00',
            'citations': [item()], 'messages': [{'message_id': 'message', 'source': 'assistant', 'content_sha256': 'd'*64}],
            'traces': [{'id': i, 'stage': s, 'items': [item(generation=s=='v2_generation_input')],
                        'created_at': '2026-10-14T00:00:00+00:00'} for i, s in enumerate(
                            ('v2_candidates', 'v2_rerank', 'v2_generation_input'), 1)]}


def attachment(bound, **data):
    body = {'identity': bound, **data}
    return {'data': body, 'sha256': digest(json.dumps(body, sort_keys=True).encode())}


@pytest.mark.parametrize('stage,expected', [(0, 'CANDIDATE_MISS'), (1, 'RERANK_LOSS'), (2, 'CONTEXT_EXCLUSION')])
def test_required_evidence_reports_stage_loss_without_semantic_pass(stage, expected):
    s = snapshot()
    s['traces'][stage]['items'] = []
    a = {'evidence_sidecar': attachment(s['identity'], required_evidence=[item()])}
    result = diagnose(s, a)
    assert result['required_evidence_observations'][0]['observation'] == expected
    assert result['semantic_review'] == 'UNKNOWN'
    assert 'PRIVATE SOURCE' not in json.dumps(result) and '/private/source' not in json.dumps(result)


def test_final_citations_are_not_full_input_and_rank_gaps_remain():
    s = snapshot()
    s['traces'][2]['items'].append(item(5, True))
    r = diagnose(s)
    assert r['citation_to_constructed_input'] == 'CONSISTENT'
    assert r['semantic_review'] == 'UNKNOWN' and r['provider_receipt'] == 'UNKNOWN'
    s['citations'] = [item(7)]
    assert diagnose(s)['citation_to_constructed_input'] == 'MISMATCH'


def test_missing_old_or_duplicate_records_remain_unknown():
    s = snapshot()
    s['traces'].pop()
    r = diagnose(s)
    assert not r['complete_stage_observation'] and r['citation_to_constructed_input'] == 'UNKNOWN'
    s['traces'] = []
    assert diagnose(s)['semantic_review'] == 'UNKNOWN'
    s = snapshot()
    s['traces'].append(copy.deepcopy(s['traces'][2]))
    assert diagnose(s)['citation_to_constructed_input'] == 'UNKNOWN'


def test_provider_and_publication_failures_are_separate():
    s = snapshot()
    s['state'] = 'FAILED'
    s['messages'] = []
    for reason, expected in [('provider_failed_generation_Unavailable', 'PROVIDER_FAILURE'),
                             ('permission_changed', 'PUBLICATION_REFUSED'),
                             ('provider_failed_evidence_validation', 'PUBLICATION_REFUSED'),
                             ('PRIVATE SECRET', 'UNKNOWN')]:
        s['reason_code'] = reason
        r = diagnose(s)
        assert r['outcome_observation'] == expected
        assert reason not in json.dumps(r)


def test_bound_agent_review_is_separate_and_cannot_skip_artifacts():
    s = snapshot()
    report = diagnose(s)
    a = {name: attachment(s['identity']) for name in ('contract', 'evidence_sidecar')}
    a['capture'] = attachment(s['identity'], answer_sha256=s['answer_sha256'], final_citations=s['citations'])
    a['review'] = attachment(s['identity'], reviewer_kind='agent', human_review_completed=False,
        answer_sha256=s['answer_sha256'], trace_sha256=report['trace_sha256'], verdict='PASS',
        **{name+'_sha256': a[name]['sha256'] for name in ('contract', 'evidence_sidecar', 'capture')})
    assert diagnose(s, a)['semantic_review'] == 'PASS'
    a['review']['data']['answer_sha256'] = 'f'*64
    with pytest.raises(DiagnosticError, match='binding'):
        diagnose(s, a)
    a.pop('contract')
    with pytest.raises(DiagnosticError, match='requires_bound'):
        diagnose(s, a)
    with pytest.raises(DiagnosticError, match='identity'):
        diagnose(s, {'contract': attachment({**s['identity'], 'shop_id': 'other'})})


def test_only_two_scoped_read_only_connections_and_no_current_acl():
    s = snapshot()
    calls = []
    now = datetime.now(timezone.utc)
    class Result:
        def __init__(self, rows): self.rows = rows
        def fetchone(self): return self.rows[0] if self.rows else None
        def fetchall(self): return self.rows
    class Connection:
        def __init__(self, role): self.role = role
        def __enter__(self): return self
        def __exit__(self, *_): pass
        def execute(self, sql, params=None):
            calls.append((self.role, sql, params))
            if 'pg_roles' in sql: return Result([('ecr_v2_test', self.role, False, False)])
            if 'FROM customer_execution' in sql:
                assert params == ('1', 'shop', 'conversation', 'execution')
                return Result([('DONE', 'ANSWER', 'grounded', 'PRIVATE ANSWER', [], now, now, 1)])
            if 'FROM customer_message' in sql: return Result([])
            if 'FROM customer_retrieval_trace' in sql:
                assert self.role == 'rag_v2_ai' and params == ('1', 'execution')
                return Result([])
            return Result([])
    result = read_execution({'V2_GATEWAY_DSN': 'gateway', 'V2_AI_DSN': 'ai'}, s['identity'],
                            connect=lambda dsn, **_: Connection('rag_v2_'+dsn))
    assert result['answer_sha256'] == digest(b'PRIVATE ANSWER')
    assert sum('READ ONLY' in c[1] for c in calls) == 2
    assert all(c[1].startswith(('SET ', 'SELECT ')) for c in calls)
    assert not any('acl' in c[1].lower() for c in calls)
    assert 'PRIVATE ANSWER' not in json.dumps(result)


def test_protected_config_permissions_and_identity_validation(tmp_path):
    path = tmp_path/'runtime.env'
    path.write_text('V2_GATEWAY_DSN=private\nV2_AI_DSN=private\n')
    path.chmod(0o644)
    with pytest.raises(DiagnosticError, match='0600'): load_connections(path)
    path.chmod(0o600)
    assert set(load_connections(path)) == {'V2_GATEWAY_DSN', 'V2_AI_DSN'}
    with pytest.raises(DiagnosticError, match='identity'): identity('1', '../bad', 'conversation', 'execution')
