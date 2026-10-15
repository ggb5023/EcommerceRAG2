#!/usr/bin/env python3
"""Restore a sanitized export in an ephemeral, networkless database container."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
import time
import uuid

from publication_archive import file_sha, write_json


def restore_sql(folder, evidence):
    yield (folder/'pre-data.sql').read_bytes()
    for table, count in evidence['table_counts'].items():
        if not re.fullmatch(r'[a-z_][a-z0-9_]*', table):
            raise ValueError('invalid table identifier')
        path = folder/(table+'.jsonl')
        if file_sha(path) != evidence['table_sha256'][table]:
            raise ValueError('table hash mismatch')
        rows = 0
        for line in path.read_text().splitlines():
            envelope = json.loads(line)
            data = envelope['data']
            escaped = json.dumps(data,ensure_ascii=False).replace("'", "''")
            payload = "'"+escaped+"'::json"
            values = []
            for col in evidence['columns'][table]:
                if col in evidence['json_columns'][table] and col not in envelope['sql_null_columns']:
                    values.append(f"({payload} -> '{col}')")
                else:
                    values.append(f'(json_populate_record(NULL::public."{table}",{payload}))."{col}"')
            yield (f'INSERT INTO public."{table}" OVERRIDING SYSTEM VALUE SELECT '+','.join(values)+';\n').encode()
            rows += 1
        if rows != count:
            raise ValueError('row count mismatch')
    yield (folder/'post-data.sql').read_bytes()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--export', type=Path, required=True)
    p.add_argument('--image', required=True, help='already installed image digest; never pulls')
    args = p.parse_args()
    name = 'ecr-publication-verify-'+uuid.uuid4().hex[:10]
    started = False
    try:
        evidence = json.loads((args.export/'database-evidence.json').read_text())
        subprocess.run(['docker','run','-d','--pull=never','--name',name,'--network','none',
                        '--tmpfs','/var/lib/postgresql/data:rw,size=512m','-e','POSTGRES_HOST_AUTH_METHOD=trust',
                        args.image,'postgres','-c','listen_addresses='],check=True,capture_output=True)
        started = True
        for _ in range(30):
            result = subprocess.run(['docker','exec',name,'pg_isready','-U','postgres'],capture_output=True)
            if result.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('isolated database readiness failed')
        roles = b'CREATE ROLE rag_app; CREATE ROLE rag_v2_gateway; CREATE ROLE rag_v2_ai; CREATE ROLE rag_v2_ingest;\n'
        process = subprocess.Popen(['docker','exec','-i',name,'psql','-X','-q','-U','postgres','-v','ON_ERROR_STOP=1'],
                                   stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        process.stdin.write(roles)
        try:
            for part in restore_sql(args.export,evidence):
                process.stdin.write(part)
            process.stdin.close()
        except BrokenPipeError:
            pass
        error = process.stderr.read()
        if process.wait():
            # Keep nonsecret SQL diagnostics in local protected evidence only.
            (args.export/'restore-diagnostic.txt').write_bytes(error)
            (args.export/'restore-diagnostic.txt').chmod(0o600)
            raise RuntimeError('restore rejected; inspect protected local diagnostics')
        for table, count in evidence['table_counts'].items():
            actual = subprocess.check_output(['docker','exec',name,'psql','-X','-U','postgres','-Atc',f'SELECT count(*) FROM public."{table}"']).decode().strip()
            if int(actual) != count:
                raise ValueError('restored row count mismatch')
        active = subprocess.check_output(['docker','exec',name,'psql','-X','-U','postgres','-Atc',
                                         'SELECT count(*) FROM customer_session WHERE revoked_at IS NULL OR expires_at>now() OR idle_until>now()']).decode().strip()
        if active != '0':
            raise ValueError('restored sessions must be disabled')
        evidence.update(restore_status='PASS', restored_tables=len(evidence['table_counts']),
                        restored_rows=sum(evidence['table_counts'].values()), restored_active_sessions=0,
                        verification_environment='ephemeral_networkless_container', verification_image=args.image)
        write_json(args.export/'database-evidence.json', evidence)
        print('PASS sanitized database restore, table counts, constraints and disabled sessions')
        return 0
    except Exception as exc:
        print('FAILED isolated export verification; '+type(exc).__name__,file=sys.stderr)
        return 1
    finally:
        if started:
            subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    raise SystemExit(main())
