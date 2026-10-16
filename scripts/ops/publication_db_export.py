#!/usr/bin/env python3
"""Consistent read-only V2 export. Credentials are disabled only in the copy.

Run with the project's locked psycopg environment. Uses the existing local
container; passwords are read from a 0600 file and never passed as CLI options.
"""
import argparse
from collections import Counter
from datetime import datetime, timezone
import json
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'check'))
from publication_scan import Scanner, SECRET_KEY, load_known, sha
from publication_archive import write_json


def sanitize_row(table, row, scanner):
    methods = Counter()
    def clean(v, key=''):
        if isinstance(v, dict):
            return {k: clean(value, k) for k,value in v.items()}
        if isinstance(v, list):
            return [clean(value) for value in v]
        if isinstance(v, str):
            if SECRET_KEY.search(key):
                methods['credential-field-invalidated'] += 1
                return '[REDACTED:credential-field]'
            if v.startswith('\\x') and key in ('blob', 'value'):
                methods['serialized-state-cleared'] += 1
                return '\\x'
            data, why = scanner.redact(v.encode())
            methods.update(why)
            return data.decode()
        return v
    result = clean(row)
    if table == 'customer_session':
        # Replacement is a deliberately unusable identifier, not an original
        # credential hash. Revocation + expired timestamps prevent reuse.
        result['token_sha256'] = sha(('disabled-export-session:'+str(row['id'])).encode())
        result['csrf_token'] = '[REDACTED:disabled-session]'
        result['revoked_at'] = result['expires_at'] = result['idle_until'] = '2000-01-01T00:00:00+00:00'
        methods['all-customer-sessions-disabled'] += 1
    if table == 'customer_conversation' and row.get('state') == 'HUMAN':
        result.update(state='WAITING_HUMAN', owner_user_id=None, owner_session_hash=None, lease_until=None)
        result['epoch'] = row['epoch'] + 1
        methods['staff-claim-invalidated'] += 1
    # Clear worker lease owner identifiers (not user data).
    if 'owner_token' in result:
        result['owner_token'] = '[REDACTED:disabled-lease]' if table == 'ingest_artifact_object' else None
        if result.get('lease_expires_at'):
            result['lease_expires_at'] = '2000-01-01T00:00:00+00:00'
        methods['worker-lease-invalidated'] += 1
    return result, methods


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--password-file', type=Path, required=True)
    parser.add_argument('--database', default='ecr_v2')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        import psycopg
        from psycopg import sql
        if args.password_file.stat().st_mode & 0o077:
            raise ValueError('protected credential file required')
        info = json.loads(subprocess.check_output(['docker','inspect',args.container]))[0]
        host = next(iter(info['NetworkSettings']['Networks'].values()))['IPAddress']
        scanner = Scanner()
        load_known(scanner, ['/etc/ecommerce-rag', '/root/.codex/auth.json', '/var/lib/ecommerce-rag'])
        args.output.mkdir(parents=True, exist_ok=True, mode=0o700)
        with psycopg.connect(host=host, dbname=args.database, user='postgres', password=args.password_file.read_text().strip()) as conn:
            conn.execute('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
            snapshot = conn.execute('SELECT pg_export_snapshot()').fetchone()[0]
            tables = [r[0] for r in conn.execute("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")]
            for section in ('pre-data', 'post-data'):
                data = subprocess.check_output(['docker','exec',args.container,'pg_dump','-U','postgres','-d',args.database,
                                                '--schema-only','--no-owner','--no-privileges','--section='+section,'--snapshot='+snapshot])
                safe, why = scanner.inspect(data, 'schema.sql')
                if safe is None or safe != data:
                    raise ValueError('schema requires dedicated redaction')
                path = args.output/(section+'.sql')
                path.write_bytes(safe)
                path.chmod(0o600)
            counts, digests, changes = {}, {}, Counter()
            columns = {}
            json_columns = {}
            for table in tables:
                columns[table] = [r[0] for r in conn.execute("SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=%s ORDER BY ordinal_position",(table,))]
                json_columns[table] = [r[0] for r in conn.execute("SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=%s AND data_type IN ('json','jsonb') ORDER BY ordinal_position",(table,))]
                cases = [sql.SQL('CASE WHEN t.{} IS NULL THEN {} END').format(sql.Identifier(c),sql.Literal(c)) for c in json_columns[table]]
                nulls = sql.SQL('ARRAY_REMOVE(ARRAY[{}]::text[],NULL)').format(sql.SQL(',').join(cases))
                rows = conn.execute(sql.SQL('SELECT row_to_json(t), {} FROM public.{} t').format(nulls,sql.Identifier(table)))
                target = args.output/(table+'.jsonl')
                n = 0
                with target.open('w') as out:
                    target.chmod(0o600)
                    for raw, sql_nulls in rows:
                        row, why = sanitize_row(table,raw,scanner)
                        changes.update(why)
                        text = json.dumps(dict(data=row,sql_null_columns=sql_nulls), ensure_ascii=False)
                        if scanner.has_secret(text.encode()):
                            raise ValueError('secret remains in structured export')
                        out.write(text+'\n')
                        n += 1
                counts[table] = n
                digests[table] = sha(target.read_bytes())
                if n != conn.execute(sql.SQL('SELECT count(*) FROM public.{}').format(sql.Identifier(table))).fetchone()[0]:
                    raise ValueError('snapshot row count mismatch')
            evidence = dict(export_version='sanitized-v2-jsonl-v1', generated_at=datetime.now(timezone.utc).isoformat(),
                            database_alias='active_v2', consistency='single_repeatable_read_read_only_exported_snapshot',
                            schema_sections=['pre-data.sql','post-data.sql'], table_counts=counts, columns=columns,
                            table_sha256=digests, json_columns=json_columns, transformations=dict(changes), original_sessions_reusable=False,
                            role_passwords_included=False, snapshot_credentials_included=False,
                            restore_status='NOT_YET_VALIDATED', real_service_acceptance=False)
            write_json(args.output/'database-evidence.json',evidence)
            conn.rollback()
        print(json.dumps(dict(tables=len(counts),rows=sum(counts.values()),transformations=dict(changes))))
        return 0
    except Exception as exc:
        print('FAILED read-only database export; exception type='+type(exc).__name__,file=sys.stderr)
        import traceback
        print('failure line='+str(traceback.extract_tb(exc.__traceback__)[-1].lineno),file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
