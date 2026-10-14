#!/usr/bin/env python3
"""Restore-verify and seal the configured legacy DB without deleting anything.

PLAN by default. Runs through the existing ops SSH alias. The legacy application
configuration and deployed infrastructure must both confirm the target. All
restrictions are database-local; shared roles and other databases are unchanged.
"""
from __future__ import annotations

import argparse
import json
import os
import stat
import subprocess
from pathlib import Path

REMOTE = r'''
import datetime,fcntl,hashlib,json,os,subprocess,uuid
from pathlib import Path
phase='ops_binding'
directory=None
def command(args):
 p=subprocess.run(args,capture_output=True,check=False)
 if p.returncode:raise RuntimeError('ops_command_failed')
 return p.stdout.decode()
def query(database,statement):
 return command(['docker','exec','-u','postgres','rag-postgres','psql','-X','-v','ON_ERROR_STOP=1','-U','postgres','-d',database,'-At','-c',statement]).strip()
def snapshot(database):
 tables=query(database,"SELECT json_agg(json_build_array(schemaname,tablename) ORDER BY schemaname,tablename) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')")
 rows=[]
 for schema,table in json.loads(tables or 'null') or []:
  identifier=lambda x:'"'+x.replace('"','""')+'"'
  digest=query(database,"SET timezone='UTC'; SELECT count(*)::text||':'||COALESCE(md5(string_agg(md5(to_jsonb(t)::text),',' ORDER BY md5(to_jsonb(t)::text))),md5('')) FROM "+identifier(schema)+'.'+identifier(table)+' t').splitlines()[-1]
  rows.append([schema,table,digest])
 extensions=query(database,"SELECT extname||':'||extversion FROM pg_extension ORDER BY extname").splitlines()
 schema_acl=query(database,"SELECT nspname||':'||pg_get_userbyid(nspowner)||':'||COALESCE(nspacl::text,'') FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname<>'information_schema' ORDER BY nspname").splitlines()
 table_acl=query(database,"SELECT n.nspname||':'||c.relname||':'||pg_get_userbyid(c.relowner)||':'||COALESCE(c.relacl::text,'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema' ORDER BY n.nspname,c.relname").splitlines()
 return {'tables':rows,'extensions':extensions,'schema_acl':schema_acl,'relation_acl':table_acl}
def v2_settings():
 return query('postgres',"SELECT d.datname||':'||d.datallowconn::text||':'||COALESCE(d.datacl::text,'')||':'||COALESCE(s.setconfig::text,'') FROM pg_database d LEFT JOIN pg_db_role_setting s ON s.setdatabase=d.oid WHERE d.datname LIKE 'ecr_v2_%' ORDER BY d.datname,s.setrole")
def write_json(path,value):
 with os.fdopen(os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as output:json.dump(value,output,indent=2)
def verify_seal():
 if query('rag','SHOW default_transaction_read_only')!='on':raise RuntimeError('legacy_read_only_missing')
 if query('postgres',"SELECT count(*) FROM pg_roles WHERE rolcanlogin AND NOT rolsuper AND has_database_privilege(oid,'rag','CONNECT')")!='0':raise RuntimeError('legacy_application_connect_remains')
 denied=subprocess.run(['docker','exec','-u','postgres','rag-postgres','psql','-X','-v','ON_ERROR_STOP=1','-U','postgres','-d','rag','-c','BEGIN; CREATE TABLE public.v1_seal_probe(x int); ROLLBACK;'],capture_output=True,check=False)
 if denied.returncode==0 or b'read-only transaction' not in denied.stderr:raise RuntimeError('legacy_write_not_denied')
try:
 if not Path('/opt/ecommerce-rag-ops').is_dir():raise RuntimeError('ops_workspace_missing')
 if command(['docker','exec','rag-postgres','printenv','POSTGRES_DB']).strip()!='rag':raise RuntimeError('legacy_init_database_mismatch')
 backup_script=Path('/opt/ecommerce-rag-ops/scripts/backup-db.sh')
 if not backup_script.is_file() or '-d rag' not in backup_script.read_text():raise RuntimeError('legacy_backup_binding_unconfirmed')
 lock=open('/run/lock/ecommerce-rag-v1-seal.lock','a')
 fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 state=Path('/data/backups/v1-sealed/sealed-state.json')
 if state.exists():
  phase='existing_seal_verification'
  existing=json.loads(state.read_text())
  backup=Path(existing['backup_path'])
  if hashlib.sha256(backup.read_bytes()).hexdigest()!=existing['backup_sha256']:raise RuntimeError('existing_backup_hash_mismatch')
  verify_seal()
  if snapshot(existing['restore_database'])!=snapshot('rag'):raise RuntimeError('existing_restore_mismatch')
  print(json.dumps(dict(existing,reverification=True)))
  raise SystemExit(0)
 phase='legacy_usage_inventory'
 if query('postgres',"SELECT count(*) FROM pg_stat_activity WHERE datname='rag'")!='0':raise RuntimeError('legacy_connections_present')
 grantees=query('postgres',"SELECT count(*) FROM pg_database d CROSS JOIN LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a JOIN pg_roles r ON r.oid=a.grantee WHERE d.datname='rag' AND a.privilege_type='CONNECT' AND NOT r.rolsuper AND r.rolname<>'rag_app'")
 if grantees!='0':raise RuntimeError('legacy_unidentified_connect_grantee')
 before=snapshot('rag')
 instant=datetime.datetime.now(datetime.timezone.utc)
 run=instant.strftime('%Y%m%dT%H%M%SZ')+'-'+uuid.uuid4().hex[:8]
 directory=Path('/data/backups/v1-sealed')/run
 directory.mkdir(parents=True,mode=0o700);os.chmod(directory.parent,0o700)
 phase='backup'
 backup=directory/'legacy.dump'
 with os.fdopen(os.open(backup,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'wb') as output:
  p=subprocess.run(['docker','exec','-u','postgres','rag-postgres','pg_dump','-U','postgres','-Fc','-d','rag'],stdout=output,stderr=subprocess.PIPE,check=False)
 if p.returncode:raise RuntimeError('legacy_backup_failed')
 backup_hash=hashlib.sha256(backup.read_bytes()).hexdigest()
 phase='isolated_restore'
 restore='ecr_v1_restore_'+instant.strftime('%Y%m%d')+'_'+uuid.uuid4().hex[:8]
 query('postgres','CREATE DATABASE '+restore)
 query('postgres','REVOKE CONNECT ON DATABASE '+restore+' FROM PUBLIC')
 with backup.open('rb') as source:
  p=subprocess.run(['docker','exec','-i','-u','postgres','rag-postgres','pg_restore','-U','postgres','--exit-on-error','--single-transaction','-d',restore],stdin=source,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=False)
 if p.returncode:raise RuntimeError('legacy_restore_failed')
 if before!=snapshot(restore) or before!=snapshot('rag'):raise RuntimeError('legacy_restore_or_live_contents_mismatch')
 phase='preseal_recheck'
 if query('postgres',"SELECT count(*) FROM pg_stat_activity WHERE datname='rag'")!='0':raise RuntimeError('legacy_connections_appeared')
 v2=v2_settings()
 phase='seal'
 query('postgres',"BEGIN; ALTER DATABASE rag SET default_transaction_read_only=on; REVOKE CONNECT ON DATABASE rag FROM PUBLIC; REVOKE CONNECT ON DATABASE rag FROM rag_app; COMMIT;")
 phase='seal_verification'
 verify_seal()
 if v2_settings()!=v2:raise RuntimeError('v2_database_settings_changed')
 sealed=datetime.datetime.now(datetime.timezone.utc)
 report={'report_version':'v1-seal-v2','status':'PASS','database':'rag','database_binding':'legacy_dev_config_and_deployed_init_and_backup',
 'table_count':len(before['tables']),'extensions':before['extensions'],'snapshot_sha256':hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest(),
 'backup_sha256':backup_hash,'backup_path':str(backup),'restore_database':restore,'restore_verified':True,
 'applications_connected_before_seal':0,'process_stop_required':False,'application_connect_denied':True,'write_denied':True,'default_read_only':True,'v2_database_settings_unchanged':True,
 'sealed_at':sealed.isoformat(),'retain_until':(sealed+datetime.timedelta(days=7)).isoformat(),'automatic_delete':False,'real_service_acceptance':False}
 write_json(directory/'report.json',report)
 write_json(state,report)
 print(json.dumps(report))
except Exception as error:
 report={'status':'NOT_PASSED','phase':phase,'failure_code':str(error) if isinstance(error,RuntimeError) else type(error).__name__,'automatic_delete':False}
 if directory is not None:write_json(directory/'failure.json',report)
 print(json.dumps(report))
 raise SystemExit(1)
'''


def legacy_binding(path: Path) -> bool:
    """Read only database and role identifiers, without exposing other values."""
    try:
        info = path.lstat()
    except OSError:
        return False
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.geteuid() or info.st_size > 65536):
        return False
    values = {}
    for raw in path.read_text().splitlines():
        key, sep, value = raw.partition("=")
        if sep and key in {"PGDATABASE", "PGUSER"}:
            if key in values:
                return False
            values[key] = value.strip().strip("\"'")
    return values == {"PGDATABASE": "rag", "PGUSER": "rag_app"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--legacy-config", type=Path, default=Path("/etc/ecommerce-rag/dev.env"))
    args = parser.parse_args()
    if not args.live:
        print(json.dumps({"status": "PLAN", "database": "rag", "automatic_delete": False}))
        return 0
    if not legacy_binding(args.legacy_config):
        print(json.dumps({"status": "NOT_PASSED", "failure_code": "legacy_app_binding_unconfirmed"}))
        return 1
    result = subprocess.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=6", "ecommerce-rag-db", "python3", "-"],
                            input=REMOTE.encode(), capture_output=True, check=False)
    try:
        report = json.loads(result.stdout)
    except (ValueError, UnicodeError):
        report = {"status": "NOT_PASSED", "failure_code": "ops_transport_or_report_failed"}
    print(json.dumps(report))
    return 0 if result.returncode == 0 and report.get("status") == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
