#!/usr/bin/env python3
"""Read-only, metadata-only diagnosis of one explicitly scoped customer execution."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import stat
from contextlib import ExitStack
from pathlib import Path

from quality_capture import secure_directory

ROOT = Path(__file__).resolve().parents[1]
STAGES = ('v2_candidates', 'v2_rerank', 'v2_generation_input')
HASH = re.compile(r'^[a-f0-9]{64}$')
IDENTIFIER = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$')


class DiagnosticError(ValueError):
    """Only fixed, non-sensitive error codes may leave this tool."""


def digest(data):
    return hashlib.sha256(data).hexdigest()


def protected_bytes(path: Path):
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise DiagnosticError('protected_file_symlink')
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.getuid() or info.st_size > 16 * 1024 * 1024):
        raise DiagnosticError('owned_regular_0600_file_required')
    return path.read_bytes()


def load_connections(path):
    values = {}
    for line in protected_bytes(path).decode().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        key, sep, value = line.partition('=')
        if not sep or key in values:
            raise DiagnosticError('invalid_runtime_configuration')
        values[key] = value.strip().strip('"\'')
    if not all(values.get(k) for k in ('V2_GATEWAY_DSN', 'V2_AI_DSN')):
        raise DiagnosticError('restricted_connections_required')
    return values


def identity(tenant, shop, conversation, execution):
    if not str(tenant).isdigit() or int(tenant) <= 0 or any(
        not IDENTIFIER.fullmatch(value) for value in (shop, conversation, execution)
    ):
        raise DiagnosticError('invalid_explicit_identity')
    return {'tenant_id': str(tenant), 'shop_id': shop, 'conversation_id': conversation, 'execution_id': execution}


def read_execution(values, bound, connect=None):
    if connect is None:
        import psycopg
        connect = psycopg.connect
    with ExitStack() as stack:
        connections = []
        databases = []
        for key, expected in (('V2_GATEWAY_DSN', 'rag_v2_gateway'), ('V2_AI_DSN', 'rag_v2_ai')):
            conn = stack.enter_context(connect(values[key], connect_timeout=5))
            conn.execute('SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY')
            conn.execute("SET LOCAL statement_timeout = '5s'")
            database, role, superuser, bypass = conn.execute(
                'SELECT current_database(),current_user,rolsuper,rolbypassrls '
                'FROM pg_roles WHERE rolname=current_user').fetchone()
            if role != expected or superuser or bypass:
                raise DiagnosticError('restricted_service_role_required')
            databases.append(database)
            connections.append(conn)
        if databases[0] != databases[1]:
            raise DiagnosticError('connection_database_mismatch')
        gateway, ai = connections
        row = gateway.execute(
            'SELECT e.state,e.behavior,e.reason_code,e.answer,e.evidence_json,e.created_at,e.finished_at,e.epoch '
            'FROM customer_execution e JOIN customer_conversation c ON c.tenant_id=e.tenant_id AND c.id=e.conversation_id '
            'WHERE e.tenant_id=%s AND c.shop_id=%s AND c.id=%s AND e.id=%s',
            (bound['tenant_id'], bound['shop_id'], bound['conversation_id'], bound['execution_id'])).fetchone()
        if row is None:
            raise DiagnosticError('execution_identity_mismatch_or_missing')
        state, behavior, reason, answer, citations, created, finished, epoch = row
        messages = gateway.execute(
            "SELECT id,source,content,citations_json FROM customer_message "
            "WHERE tenant_id=%s AND conversation_id=%s AND execution_id=%s ORDER BY created_at,id",
            (bound['tenant_id'], bound['conversation_id'], bound['execution_id'])).fetchall()
        # The FK binds trace identity; no current ACL query is used as a
        # substitute for authorization at execution time.
        traces = ai.execute(
            'SELECT id,stage,candidates_json,created_at FROM customer_retrieval_trace '
            'WHERE tenant_id=%s AND execution_id=%s ORDER BY id',
            (bound['tenant_id'], bound['execution_id'])).fetchall()
        return {'identity': bound, 'state': state, 'behavior': behavior, 'reason_code': reason,
                'answer_sha256': digest((answer or '').encode()), 'citations': citations,
                'created_at': created.isoformat(), 'finished_at': finished.isoformat() if finished else None,
                'epoch': epoch, 'messages': [
                    {'message_id': m[0], 'source': m[1], 'content_sha256': digest(m[2].encode()), 'citations': m[3]}
                    for m in messages],
                'traces': [{'id': t[0], 'stage': t[1], 'items': t[2], 'created_at': t[3].isoformat()} for t in traces]}


def evidence(items, generation=False):
    if not isinstance(items, list):
        raise DiagnosticError('invalid_evidence_metadata')
    result = []
    seen = set()
    for item in items:
        if not isinstance(item, dict):
            raise DiagnosticError('invalid_evidence_metadata')
        row = {}
        for key in ('id', 'document_id', 'version_id'):
            value = str(item.get(key, ''))
            if not IDENTIFIER.fullmatch(value):
                raise DiagnosticError('invalid_evidence_identity')
            row[key] = value
        if row['id'] in seen:
            raise DiagnosticError('duplicate_evidence_identity')
        seen.add(row['id'])
        rank = item.get('rank')
        if type(rank) is not int or rank <= 0:
            raise DiagnosticError('invalid_evidence_rank')
        row['rank'] = rank
        for key in ('raw_score', 'rrf_score', 'rerank_score'):
            score = item.get(key)
            if isinstance(score, (float, int)) and not isinstance(score, bool):
                import math
                if math.isfinite(score):
                    row[key] = score
        if generation:
            for key in ('content_sha256', 'context_sha256', 'request_sha256'):
                value = item.get(key, '')
                if not isinstance(value, str) or not HASH.fullmatch(value):
                    raise DiagnosticError('invalid_generation_input_hash')
                row[key] = value
        result.append(row)
    return result


def binding_key(row):
    return row['id'], row['document_id'], row['version_id']


def diagnose(snapshot, attachments=None):
    """Stage observations are not semantic judgments or historical ACL proof."""
    attachments = attachments or {}
    stages = {}
    traces = []
    for trace in snapshot['traces']:
        stage = trace['stage']
        if stage not in STAGES:
            continue
        items = evidence(trace['items'], generation=stage == 'v2_generation_input')
        traces.append({'trace_id': trace['id'], 'stage': stage, 'created_at': trace['created_at'], 'items': items})
        stages.setdefault(stage, []).append(items)
    # Multiple records are retained, never silently collapsed into a fabricated
    # single retrieval/generation attempt.
    complete = all(len(stages.get(s, [])) == 1 for s in STAGES)
    selected = {s: values[0] for s, values in stages.items() if len(values) == 1}
    citations = evidence(snapshot['citations'])
    published = []
    for message in snapshot['messages']:
        if not IDENTIFIER.fullmatch(message['message_id']):
            raise DiagnosticError('invalid_message_identity')
        if message['source'] == 'assistant':
            published.append({k: message[k] for k in ('message_id', 'source', 'content_sha256')})
    hashes = {name + '_sha256': value['sha256'] for name, value in attachments.items()}
    stage_hash = digest(json.dumps(traces, sort_keys=True, separators=(',', ':')).encode())
    requirements = attachments.get('evidence_sidecar', {}).get('data', {}).get('required_evidence', [])
    bound = snapshot['identity']
    for attachment in attachments.values():
        if attachment['data'].get('identity') != bound:
            raise DiagnosticError('attachment_identity_mismatch')
    capture = attachments.get('capture', {}).get('data')
    if capture is not None and (capture.get('answer_sha256') != snapshot['answer_sha256']
            or evidence(capture.get('final_citations', [])) != citations):
        raise DiagnosticError('capture_execution_binding_mismatch')
    findings = []
    indexed = {s: {binding_key(e): e for e in rows} for s, rows in selected.items()}
    for item in evidence(requirements):
        key = binding_key(item)
        category = 'UNKNOWN'
        for stage, missing in zip(STAGES, ('CANDIDATE_MISS', 'RERANK_LOSS', 'CONTEXT_EXCLUSION')):
            if stage not in indexed:
                break
            if key not in indexed[stage]:
                category = missing
                break
        else:
            category = 'CITED' if key in {binding_key(e) for e in citations} else 'CITATION_MISSING'
        findings.append({**{k: item[k] for k in ('id', 'document_id', 'version_id')}, 'observation': category})
    transitions = []
    for left, right in zip(STAGES, STAGES[1:]):
        if left in indexed and right in indexed:
            transitions.append({'from': left, 'to': right, 'removed_ids': sorted(
                item['id'] for key, item in indexed[left].items() if key not in indexed[right])})
    generation = selected.get('v2_generation_input')
    citation_binding = 'UNKNOWN' if generation is None else (
        'CONSISTENT' if all(binding_key(c) in indexed['v2_generation_input'] for c in citations) else 'MISMATCH')
    semantic = 'UNKNOWN'
    review = attachments.get('review', {}).get('data', {})
    required_artifacts = ('contract', 'evidence_sidecar', 'capture')
    if review:
        if not all(name in attachments for name in required_artifacts):
            raise DiagnosticError('semantic_review_requires_bound_artifacts')
        if (review.get('reviewer_kind') != 'agent' or review.get('human_review_completed') is not False
                or review.get('answer_sha256') != snapshot['answer_sha256']
                or review.get('trace_sha256') != stage_hash
                or any(review.get(name+'_sha256') != hashes[name+'_sha256'] for name in required_artifacts)
                or review.get('verdict') not in ('PASS', 'FAIL', 'UNKNOWN')):
            raise DiagnosticError('semantic_review_binding_mismatch')
        semantic = review['verdict']
    reason = snapshot.get('reason_code') or ''
    # Fixed categories only; unrecognized reason text is never echoed.
    if reason in ('permission_changed', 'provider_failed_evidence_validation'):
        outcome = 'PUBLICATION_REFUSED'
    elif reason.startswith('provider_failed_') and any(reason.startswith('provider_failed_'+s) for s in ('control', 'search', 'generation')):
        outcome = 'PROVIDER_FAILURE'
    elif published:
        outcome = 'PUBLISHED'
    elif snapshot['state'] == 'ASKING':
        outcome = 'CLARIFICATION'
    elif reason in ('knowledge_missing', 'unsupported_request', 'customer_requested', 'conflicting_evidence'):
        outcome = 'HANDOFF'
    else:
        outcome = 'UNKNOWN'
    if snapshot['state'] not in ('DONE', 'ASKING', 'FAILED', 'CANCELLED'):
        semantic = 'UNKNOWN'
    return {'schema_version': 1, 'identity': bound, 'execution_state': snapshot['state'],
            'execution_epoch': snapshot['epoch'], 'created_at': snapshot['created_at'], 'finished_at': snapshot['finished_at'],
            'answer_sha256': snapshot['answer_sha256'], 'trace_sha256': stage_hash,
            'stage_records': traces, 'complete_stage_observation': complete, 'transitions': transitions,
            'required_evidence_observations': findings, 'final_citations': citations,
            'citation_to_constructed_input': citation_binding, 'published_messages': published,
            'outcome_observation': outcome, 'semantic_review': semantic, 'artifact_hashes': hashes,
            'provider_receipt': 'UNKNOWN', 'historical_authorization': 'UNKNOWN',
            'real_service_acceptance': False,
            'limitations': ['generation_input_is_constructed_request_not_provider_receipt',
                            'final_citations_do_not_prove_full_context', 'stage_membership_does_not_prove_semantic_support',
                            'current_authorization_is_not_historical_authorization',
                            'two_read_only_connections_do_not_share_a_snapshot']}


def markdown(report):
    lines = ['# Customer execution diagnosis', '',
             f"Execution: `{report['identity']['execution_id']}`", '',
             f"Outcome: {report['outcome_observation']}; semantic review: {report['semantic_review']}", '',
             '| Stage | Records | Items |', '|---|---:|---:|']
    for stage in STAGES:
        rows = [r for r in report['stage_records'] if r['stage'] == stage]
        lines.append(f"| {stage} | {len(rows)} | {sum(len(r['items']) for r in rows)} |")
    lines += ['', f"Citation/input binding: {report['citation_to_constructed_input']}", '',
              'Constructed input does not prove provider reception. Missing records remain UNKNOWN.',
              'No lexical or ID match establishes semantic support. No current ACL replaces historical authorization.',
              '`real_service_acceptance=false`', '']
    return '\n'.join(lines)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--runtime-config', type=Path, required=True)
    for name in ('tenant', 'shop', 'conversation', 'execution'):
        p.add_argument('--'+name, required=True)
    p.add_argument('--output-dir', type=Path, required=True)
    for name in ('contract', 'evidence-sidecar', 'capture', 'review'):
        p.add_argument('--'+name, type=Path)
    args = p.parse_args()
    try:
        bound = identity(args.tenant, args.shop, args.conversation, args.execution)
        if args.output_dir.exists():
            raise DiagnosticError('new_output_directory_required')
        attachments = {}
        for name in ('contract', 'evidence_sidecar', 'capture', 'review'):
            path = getattr(args, name)
            if path:
                raw = protected_bytes(path)
                attachments[name] = {'sha256': digest(raw), 'data': json.loads(raw)}
        snapshot = read_execution(load_connections(args.runtime_config), bound)
        report = diagnose(snapshot, attachments)
        report['runtime_config_sha256'] = digest(protected_bytes(args.runtime_config))
        out = secure_directory(args.output_dir)
        for name, body in (('diagnosis.json', json.dumps(report, indent=2)+'\n'), ('diagnosis.md', markdown(report))):
            fd = os.open(out/name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, 'w') as stream:
                stream.write(body)
        print('PASS read-only diagnosis written; semantic status: '+report['semantic_review'])
    except DiagnosticError as error:
        raise SystemExit(str(error)) from None
    except Exception:
        raise SystemExit('diagnosis_failed_no_sensitive_details_emitted') from None


if __name__ == '__main__':
    main()
