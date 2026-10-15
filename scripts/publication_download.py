#!/usr/bin/env python3
"""Download a private publication, verify hashes, reconstruct safe copies only."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sys
import uuid

from publication_archive import OSSStore, file_sha
from publication_scan import safe_path


def safe_target(root, rel):
    if not safe_path(rel):
        raise ValueError('unsafe archive path')
    path = root / rel
    if not path.resolve().is_relative_to(root.resolve()):
        raise ValueError('archive path escapes destination')
    if any(p.is_symlink() for p in (path, *path.parents) if p.is_relative_to(root)):
        raise ValueError('symlink in destination')
    return path


def download(store, key, path, expected):
    if path.exists():
        if file_sha(path) != expected:
            raise ValueError('local conflict')
        return
    path.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    temp = path.with_name(path.name+'.partial-'+uuid.uuid4().hex)
    try:
        # Stream from authenticated SDK. Never produce signed URLs.
        obj = store.bucket.get_object(key)
        with temp.open('xb') as out:
            os.chmod(temp,0o600)
            shutil.copyfileobj(obj,out)
        obj.close()
        if file_sha(temp) != expected:
            raise ValueError('download digest mismatch')
        os.link(temp,path)  # Atomic no-overwrite install.
    finally:
        temp.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config',type=Path,required=True)
    parser.add_argument('--prefix',required=True)
    parser.add_argument('--destination',type=Path,required=True)
    parser.add_argument('--index',type=Path,default=Path(__file__).resolve().parents[1]/'project-records/archive.json')
    args = parser.parse_args()
    try:
        index = json.loads(args.index.read_text())
        if index['oss_prefix'] != args.prefix:
            raise ValueError('prefix must match public index')
        root = args.destination.resolve()
        root.mkdir(parents=True,exist_ok=True,mode=0o700)
        os.chmod(root,0o700)
        store = OSSStore(args.config)
        manifest_path = safe_target(root,'private-manifest.json')
        download(store,args.prefix+'private-manifest.json',manifest_path,index['private_manifest_sha256'])
        manifest = json.loads(manifest_path.read_text())
        count = 0
        for record in manifest['files']:
            if record['status'] != 'READY':
                continue
            rel = record['object_path']
            obj = safe_target(root,rel)
            download(store,args.prefix+rel,obj,record['export_sha256'])
            target = safe_target(root,'restored/'+record['source_alias']+'/'+record['original_path'])
            target.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
            if target.exists():
                if file_sha(target) != record['export_sha256']:
                    raise ValueError('reconstruction conflict')
            else:
                os.link(obj,target)
            count += 1
        print('PASS verified source mappings:',count,'archive status:',manifest['archive_status'])
        return 0
    except Exception:
        print('FAILED private retrieval; details withheld',file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
