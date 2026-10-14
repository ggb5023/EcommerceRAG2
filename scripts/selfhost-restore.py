#!/usr/bin/env python3
"""Restore a sealed backup into a new isolated Compose project, never in place.

Applications remain stopped. This proves database and file recovery; identity
and browser recovery must be checked before changing the installed Web entry.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import re
import subprocess
import tarfile
from pathlib import Path, PurePosixPath

ROOT=Path(__file__).resolve().parents[1]


def checked_archive(backup):
    manifest=json.loads((backup/'manifest.json').read_text())
    if set(manifest['hashes'])!={'ecr_v2.dump','keycloak.dump','runtime.tar.gz','budget.sqlite'}:
        raise ValueError('invalid backup inventory')
    for name,checksum in manifest['hashes'].items():
        path=backup/name
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest()!=checksum:
            raise ValueError('backup integrity mismatch')
    with tarfile.open(backup/'runtime.tar.gz') as tar:
        for member in tar.getmembers():
            path=PurePosixPath(member.name)
            if (path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir())
                    or member.uid not in {0,999,1000,65532} or member.mode&0o077):
                raise ValueError('unsafe archive member')
    return manifest


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--backup',type=Path,required=True);p.add_argument('--directory',type=Path,required=True)
    p.add_argument('--project',required=True)
    p.add_argument('--postgres-image',help='explicit verified local import for disconnected restore tests')
    a=p.parse_args();backup=a.backup.resolve();dest=a.directory.resolve()
    if dest.exists() or dest.is_relative_to(ROOT) or not re.fullmatch(r'ecr-restore-[a-z0-9-]+',a.project):
        raise ValueError('new external directory and ecr-restore-* project required')
    if subprocess.check_output(['docker','volume','ls','--filter','label=com.docker.compose.project='+a.project,'-q'],text=True).strip():
        raise ValueError('restore project already has volumes')
    manifest=checked_archive(backup)
    dest.mkdir(mode=0o700,parents=True)
    with tarfile.open(backup/'runtime.tar.gz') as tar:
        # Inventory was checked before any mutation; only regular restricted
        # files/directories can be extracted, retaining fixed service ownership.
        tar.extractall(dest,filter='fully_trusted')
    (dest/'budget').mkdir(mode=0o700)
    worker=dict(line.split('=',1) for line in (dest/'worker.env').read_text().splitlines())
    ledger=dest/'budget'/Path(worker['V2_ONLINE_BUDGET_LEDGER']).name
    with os.fdopen(os.open(ledger,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'wb') as f:f.write((backup/'budget.sqlite').read_bytes())
    os.chown(ledger,65532,65532);os.chown(dest/'budget',65532,65532)
    env=dict(line.split('=',1) for line in (dest/'compose.env').read_text().splitlines())
    env.update(ECR_RUNTIME=str(dest),ECR_BUDGET_DIR=str(dest/'budget'),ECR_PROJECT=a.project)
    (dest/'compose.env').write_text('\n'.join(k+'='+v for k,v in env.items())+'\n')
    cmd=['docker','compose','--env-file',str(dest/'compose.env'),'-f',str(ROOT/'docker/selfhost/compose.yml')]
    if a.postgres_image:
        override=dest/'restore-image.yml'
        # JSON is a YAML subset, so an operator value cannot inject Compose keys.
        override.write_text(json.dumps({'services':{'postgres':{'image':a.postgres_image}}}))
        os.chmod(override,0o600);cmd+=['-f',str(override)]
    subprocess.run(cmd+['up','-d','--wait','postgres'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
    config=json.loads((dest/'bootstrap/config.json').read_text())
    from psycopg import sql
    statements=[]
    for role,password in config['database_passwords'].items():
        statements.append(sql.SQL('CREATE ROLE {} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD {};').format(sql.Identifier(role),sql.Literal(password)).as_string())
    statements+=['CREATE ROLE rag_app NOLOGIN NOSUPERUSER NOBYPASSRLS;','CREATE DATABASE ecr_v2 OWNER rag_migration;','CREATE DATABASE keycloak OWNER keycloak;']
    subprocess.run(cmd+['exec','-T','postgres','psql','-U','postgres','-v','ON_ERROR_STOP=1'],input='\n'.join(statements).encode(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
    for db in ('ecr_v2','keycloak'):
        with (backup/(db+'.dump')).open('rb') as stream:
            subprocess.run(cmd+['exec','-T','postgres','pg_restore','-U','postgres','--exit-on-error','-d',db],stdin=stream,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
    spec=importlib.util.spec_from_file_location('backup_tool',ROOT/'scripts/selfhost-backup.py')
    tool=importlib.util.module_from_spec(spec);spec.loader.exec_module(tool)
    actual={db:tool.database_evidence(cmd,db) for db in ('ecr_v2','keycloak')}
    if actual!=manifest['databases']:raise ValueError('restored database contents differ')
    # CONNECT grants are database-level privileges and outside pg_dump's scope.
    grants='REVOKE CONNECT ON DATABASE ecr_v2 FROM PUBLIC; GRANT CONNECT ON DATABASE ecr_v2 TO rag_v2_gateway,rag_v2_ai,rag_v2_ingest; REVOKE CONNECT ON DATABASE keycloak FROM PUBLIC;'
    subprocess.run(cmd+['exec','-T','postgres','psql','-U','postgres','-v','ON_ERROR_STOP=1'],input=grants.encode(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
    result={'status':'PASS','database_restore_verified':True,'backup_manifest_sha256':hashlib.sha256((backup/'manifest.json').read_bytes()).hexdigest(),'databases':actual,'applications_started':False,'real_service_acceptance':False}
    with os.fdopen(os.open(dest/'restore-evidence.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as f:json.dump(result,f,indent=2)
    print('PASS isolated database/file restore; applications remain stopped and Web entry unchanged')


if __name__=='__main__':
    try:main()
    except Exception:raise SystemExit('FAIL restore; inspect protected backup integrity, new project and database compatibility') from None
