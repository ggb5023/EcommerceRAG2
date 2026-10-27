#!/usr/bin/env python3
"""Create an immutable private backup after an explicit maintenance stop."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import tarfile
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]


def database_evidence(cmd, database):
    def query(statement):
        return subprocess.check_output(cmd+['exec','-T','postgres','psql','-U','postgres','-d',database,'-At','-c',statement],stderr=subprocess.PIPE,text=True).strip()
    names=query("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename").splitlines()
    rows={}
    for name in names:
        identifier='"'+name.replace('"','""')+'"'
        value=query(f"SELECT count(*),md5(COALESCE(string_agg(md5(row_to_json(t)::text),'' ORDER BY md5(row_to_json(t)::text)),'')) FROM public.{identifier} t")
        count,checksum=value.split('|');rows[name]={'count':int(count),'rows_md5':checksum}
    extensions=query("SELECT extname||':'||extversion FROM pg_extension ORDER BY extname").splitlines()
    return {'tables':rows,'extensions':extensions}


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--runtime',type=Path,required=True);p.add_argument('--output',type=Path,required=True)
    p.add_argument('--maintenance-stopped',action='store_true',required=True)
    args=p.parse_args();runtime=args.runtime.resolve();out=args.output.resolve()
    if out.exists() or out.is_relative_to(runtime):raise ValueError('independent new backup directory required')
    env={}
    for line in (runtime/'compose.env').read_text().splitlines():
        k,_,v=line.partition('=');env[k]=v
    cmd=['docker','compose','--env-file',str(runtime/'compose.env'),'-f',str(ROOT/'docker/selfhost/compose.yml')]
    ps=subprocess.check_output(cmd+['ps','--status','running','--services'],text=True,stderr=subprocess.PIPE).splitlines()
    if any(s in ps for s in ('gateway','ai','ingest','worker','keycloak')):raise ValueError('writers must stop before backup')
    out.mkdir(mode=0o700,parents=True)
    databases={}
    for db in ('ecr_v2','keycloak'):
        databases[db]=database_evidence(cmd,db)
        with os.fdopen(os.open(out/(db+'.dump'),os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'wb') as stream:
            subprocess.run(cmd+['exec','-T','postgres','pg_dump','-U','postgres','-Fc',db],stdout=stream,stderr=subprocess.PIPE,check=True)
    fd=os.open(out/'runtime.tar.gz',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'wb') as f,tarfile.open(fileobj=f,mode='w:gz') as tar:
        # Symlinks are refused; this archive is sensitive and never Git material.
        for path in runtime.rglob('*'):
            if path.is_symlink():raise ValueError('unsafe runtime symlink')
        def protect(member):
            member.mode = 0o700 if member.isdir() else 0o600
            return member
        for name in ('compose.env','gateway.env','ai.env','ingest.env','worker.env','identity.env',
                     'bootstrap','gateway','ai','ingest','worker','identity','redis','web','artifacts'):
            path=runtime/name
            tar.add(path,arcname=path.name,filter=protect)
    hashes={path.name:hashlib.sha256(path.read_bytes()).hexdigest() for path in out.iterdir() if path.is_file()}
    with os.fdopen(os.open(out/'manifest.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as f:json.dump({'schema_version':1,'hashes':hashes,'databases':databases,'restore_verified':False},f,indent=2)
    print('PASS restricted backup created; independent restore verification still required')


if __name__=='__main__':
    try:main()
    except Exception:raise SystemExit('FAIL backup; confirm stopped writers, protected inputs and independent output') from None
