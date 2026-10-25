#!/usr/bin/env python3
"""Build the same service images from local locked toolchains and a verified base.

This optional path avoids downloading build-tool images. It still needs the
locked Python/npm/Go dependencies and the pinned pgvector runtime base cached.
No runtime configuration, models, data, identities or budget are copied.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
BASE='pgvector/pgvector@sha256:c52e32a172e768f2edb0e1c7493b9ef5264f4b182a357d9d53ee2d3d0c440aaf'


def command(args,cwd=ROOT,env=None):
    subprocess.run(args,cwd=cwd,env=env,check=True)


def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--directory',type=Path,required=True);args=p.parse_args()
    dest=args.directory.resolve()
    if dest.exists() or dest.is_relative_to(ROOT):raise ValueError('new build directory outside repository required')
    if sys.version_info[:2]!=(3,12) or 'cpython-' not in str(Path(sys.base_prefix)):
        raise ValueError('uv-managed standalone CPython 3.12 required')
    inspected=json.loads(subprocess.check_output(['docker','image','inspect',BASE],stderr=subprocess.PIPE))[0]
    lock=json.loads((ROOT/'docker/db/images.lock.json').read_text())['postgres']
    if inspected['Id']!=BASE.split('@',1)[1]:raise ValueError('cached runtime base identity mismatch')
    local_base='ecommerce-rag-local-base:verified-pg16'
    command(['docker','tag',BASE,local_base])
    dest.mkdir(mode=0o700,parents=True)
    command(['npm','ci','--include=dev','--no-audit','--no-fund'],ROOT/'frontend')
    command(['npm','run','build'],ROOT/'frontend')
    command(['node',str(ROOT/'scripts/ops/frontend-notices.mjs')],ROOT/'frontend')
    env={**os.environ,'CGO_ENABLED':'0'}
    for app,tag in [('gateway','go'),('web','web')]:
        command(['go','build','-trimpath','-o',str(dest/app),'./cmd/'+app],ROOT/'go',env)
    command(['uv','sync','--frozen','--no-dev','--project',str(ROOT/'python'),'--active'],env={**os.environ,'VIRTUAL_ENV':str(dest/'venv')})
    shutil.copytree(Path(sys.base_prefix),dest/'cpython',symlinks=True)
    for file in (dest/'venv/bin').iterdir():
        if file.name.startswith('python') and file.is_symlink():file.unlink();file.symlink_to('/opt/cpython/bin/python3.12')
    (dest/'venv/pyvenv.cfg').write_text('home = /opt/cpython/bin\ninclude-system-site-packages = false\nversion = 3.12\n')
    for directory in ['app','rag']:
        shutil.copytree(ROOT/'python'/directory,dest/directory,ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
    shutil.copytree(ROOT/'sql',dest/'sql');(dest/'scripts').mkdir()
    for name in ['v2-migrate.py','selfhost-bootstrap.py','selfhost-health.py']:shutil.copy2(ROOT/'scripts/ops'/name,dest/'scripts'/name)
    shutil.copytree(ROOT/'frontend'/'dist',dest/'frontend-dist')
    shutil.copy2('/etc/ssl/certs/ca-certificates.crt',dest/'ca-certificates.crt')
    notices=dest/'go-notices';notices.mkdir()
    goroot=Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
    shutil.copy2(goroot/'LICENSE',notices/'Go-LICENSE.txt')
    module_root=Path(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip())
    for path in module_root.rglob('*'):
        if path.is_file() and path.name.lower().startswith(('license','notice','copying')):
            target=notices/'modules'/path.relative_to(module_root);target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(path,target)
    specs={
      'go':'FROM scratch\nCOPY gateway /gateway\nCOPY go-notices /licenses\nCOPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt\nUSER 65532:65532\nENTRYPOINT ["/gateway"]\n',
      'web':'FROM scratch\nCOPY web /web\nCOPY go-notices /licenses\nCOPY frontend-dist /srv/frontend\nUSER 65532:65532\nENTRYPOINT ["/web"]\n',
      'python':f'FROM {local_base}\nWORKDIR /app\nCOPY cpython /opt/cpython\nCOPY venv /app/.venv\nCOPY app /app/app\nCOPY rag /app/rag\nCOPY sql /app/sql\nCOPY scripts /app/scripts\nCOPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt\nUSER 65532:65532\nENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 V2_MIGRATION_DIR=/app/sql/migrations SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt\nENTRYPOINT ["/app/.venv/bin/python"]\nCMD ["-m","app.rpc.server"]\n'}
    # The same source-only probe binary is used by canonical and local builds.
    command(['go','build','-trimpath','-o',str(dest/'probe'),'./cmd/probe'],ROOT/'go',env)
    specs['go']=specs['go'].replace('COPY gateway /gateway','COPY gateway /gateway\nCOPY probe /probe')
    for tag,spec in specs.items():
        file=dest/(tag+'.Dockerfile');file.write_text(spec);command(['docker','build','--network=none','-f',str(file),'-t','ecommerce-rag-'+tag+':selfhost',str(dest)])
    hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [ROOT/'python/uv.lock',ROOT/'go/go.sum',ROOT/'web/package-lock.json',ROOT/'admin-web/package-lock.json']}
    (dest/'build-evidence.json').write_text(json.dumps({'schema_version':1,'method':'local-locked-toolchains','runtime_base':BASE,'official_runtime_manifest':lock['source_manifest'],'lock_hashes':hashes,'git_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'real_service_acceptance':False},indent=2))
    print('PASS local service images built; runtime and end-to-end acceptance still required')


if __name__=='__main__':
    try:main()
    except Exception:raise SystemExit('FAIL local build; check standalone Python, locked toolchains, cached base and new output directory') from None
