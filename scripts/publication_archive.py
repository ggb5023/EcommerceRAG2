#!/usr/bin/env python3
"""Read-only inventory, reviewed exports and immutable private OSS publication.

All state lives in a protected directory outside the source roots. Nothing here
modifies source files, live databases, services or bucket policy.
"""
from __future__ import annotations

import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import time

from publication_scan import Scanner, load_known, safe_path, sha


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    content = (json.dumps(value, ensure_ascii=False, indent=2) + '\n').encode()
    fd = os.open(path, os.O_CREAT | os.O_WRONLY | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(content)
    return sha(content)


def file_sha(path):
    h = hashlib.sha256()
    with open(path, 'rb') as stream:
        for block in iter(lambda: stream.read(1024*1024), b''):
            h.update(block)
    return h.hexdigest()


def enumerate_root(root):
    for folder, dirs, files in os.walk(root, followlinks=False):
        dirs.sort()
        files.sort()
        if '.git' in dirs:
            dirs.remove('.git')  # Git history is published by Git, not as raw .git.
        for name in list(dirs):
            path = Path(folder) / name
            if path.is_symlink():
                dirs.remove(name)
                yield path
        for name in files:
            if name != '.git':
                yield Path(folder) / name


def secret_only(path):
    name = path.name.lower()
    return (path.suffix.lower() in ('.key', '.pem') and 'cert' not in name or
            name in ('scope-key', 'browser-secret', 'pg-password', 'keycloak-client-secret') or
            name.startswith(('id_rsa', 'id_ed25519')))


def category(rel):
    if '/dev-docs/' in '/' + rel or rel.endswith(('.md', '.markdown')):
        return 'documents'
    if any(x in rel for x in ('eval/', 'quality-review/', 'acceptance/', 'reports/')):
        return 'evaluation'
    if any(x in rel for x in ('build', 'images', 'venv/', 'node_modules/')):
        return 'builds'
    if 'database' in rel or rel.endswith(('.dump', '.sqlite')):
        return 'database'
    return 'records'


def inventory(roots, output, known_roots=()):
    output = output.resolve()
    for root in roots.values():
        if output.is_relative_to(root.resolve()):
            raise ValueError('output must be outside input roots')
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(output, 0o700)
    scanner = Scanner()
    load_known(scanner, [*known_roots, *roots.values()])
    records = []
    for alias, root in roots.items():
        if not safe_path(alias) or '/' in alias:
            raise ValueError('invalid root alias')
        for path in enumerate_root(root):
            rel = path.relative_to(root).as_posix()
            info = path.lstat()
            record = dict(source_alias=alias, original_path=rel, size=info.st_size,
                          original_sha256=None, export_sha256=None, destination='OSS',
                          license_status='project_or_authorized_private_material',
                          status='PENDING', methods=[], category=category(rel))
            records.append(record)
            if not safe_path(rel):
                record['reason'] = 'unsafe-source-path'
                continue
            if stat.S_ISLNK(info.st_mode):
                record.update(status='PENDING', reason='symlink-recorded-not-followed')
                record['link_target'] = os.readlink(path)
                continue
            if not stat.S_ISREG(info.st_mode):
                record['reason'] = 'special-file-not-copied'
                continue
            if secret_only(path):
                record.update(status='EXCLUDED_SECRET', reason='secret-material-no-content-or-fingerprint-uploaded')
                continue
            try:
                record['original_sha256'] = file_sha(path)
                if 'real-docs-v1/raw/' in rel or 'crawler-smoke-v1/raw/' in rel:
                    record.update(reason='third-party-permission-pending-direct-verification',
                                  license_status='pending_direct_verification')
                    continue
                if any(x in path.parts for x in ('node_modules', '.venv', 'venv', 'cpython')):
                    record.update(reason='dependency-closure-license-and-binary-review-pending',
                                  license_status='third_party_dependency_review_pending')
                    continue
                if info.st_size > 128*1024*1024:
                    record['reason'] = 'large-binary-or-container-needs-review'
                    continue
                content = path.read_bytes()
                if sha(content) != record['original_sha256'] or path.stat().st_mtime_ns != info.st_mtime_ns:
                    record['reason'] = 'source-changed-during-snapshot'
                    continue
                exported, methods = scanner.inspect(content, rel)
                if exported is None:
                    record['reason'] = ','.join(methods)
                    continue
                digest = sha(exported)
                dest = output / 'objects' / 'sha256' / digest
                if not dest.exists():
                    dest.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                    fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, 'wb') as stream:
                        stream.write(exported)
                if file_sha(dest) != digest:
                    raise ValueError('local object conflict')
                record.update(status='READY', export_sha256=digest, export_size=len(exported),
                              methods=methods or ['original-reviewed-unchanged'],
                              object_path='objects/sha256/'+digest)
            except (OSError, ValueError):
                record['reason'] = 'read-or-inspection-failed-details-withheld'
    manifest = dict(manifest_version='private-publication-v1', generated_at=datetime.now(timezone.utc).isoformat(),
                    roots={k: str(v) for k, v in roots.items()}, files=records,
                    explicit_exclusions=['.git: published as complete branch ancestry via Git'],
                    real_service_acceptance=False)
    manifest['counts'] = dict(Counter(r['status'] for r in records))
    manifest['archive_status'] = 'INCOMPLETE' if manifest['counts'].get('PENDING') else 'READY'
    write_json(output / 'private-manifest.json', manifest)
    print(json.dumps(dict(files=len(records), counts=manifest['counts'], status=manifest['archive_status'])))
    return manifest


def read_env(path):
    if path.stat().st_mode & 0o077:
        raise ValueError('configuration must have mode 0600')
    values = {}
    for line in path.read_text().splitlines():
        if '=' in line and not line.lstrip().startswith('#'):
            key, value = line.split('=', 1)
            values[key.strip()] = value.strip().strip('\"\x27')
    return values


class OSSStore:
    def __init__(self, config):
        import oss2
        cfg = read_env(config)
        self.oss2 = oss2
        self.bucket = oss2.Bucket(oss2.Auth(cfg['OSS_ACCESS_KEY_ID'], cfg['OSS_ACCESS_KEY_SECRET']),
                                  cfg['OSS_ENDPOINT'], cfg['OSS_BUCKET'], connect_timeout=30)
        if self.bucket.get_bucket_acl().acl != 'private':
            raise ValueError('archive bucket must remain private')

    def exists(self, key):
        return self.bucket.object_exists(key)

    def digest(self, key):
        h = hashlib.sha256()
        obj = self.bucket.get_object(key)
        try:
            for block in iter(lambda: obj.read(1024*1024), b''):
                h.update(block)
        finally:
            obj.close()
        return h.hexdigest()

    def create(self, key, path, digest):
        # Conditional create, not HEAD-then-unconditional-PUT. Private object ACL
        # is explicit even if an operator later changes the bucket default.
        self.bucket.put_object_from_file(key, str(path), headers={
            'x-oss-forbid-overwrite': 'true', 'x-oss-object-acl': 'private',
            'x-oss-meta-sha256': digest,
        })


def transfer(store, key, path, expected, attempts=3):
    if file_sha(path) != expected:
        raise ValueError('local hash mismatch')
    for attempt in range(attempts):
        try:
            if store.exists(key):
                if store.digest(key) != expected:
                    raise ValueError('immutable object conflict')
                return 'EXISTING_VERIFIED'
            store.create(key, path, expected)
            if store.digest(key) != expected:
                raise ValueError('download hash mismatch')
            return 'UPLOADED_VERIFIED'
        except ValueError:
            raise
        except Exception:
            # Unknown commit is resolved by querying & downloading on next try.
            if attempt + 1 == attempts:
                raise RuntimeError('OSS operation failed; details withheld') from None
            time.sleep(min(2**attempt, 4))
    raise RuntimeError('unreachable')


def upload(output, prefix, config, jobs=4):
    if not re.fullmatch(r'ecommercerag2/publication/20261015/[A-Za-z0-9_-]+/', prefix):
        raise ValueError('invalid immutable batch prefix')
    manifest_path = output / 'private-manifest.json'
    manifest = json.loads(manifest_path.read_text())
    paths = {r['object_path']: r['export_sha256'] for r in manifest['files'] if r['status'] == 'READY'}
    # Control/index files have stable batch-local names and are also immutable.
    for name in ('private-manifest.json', 'directory-index.json', 'database-evidence.json'):
        if (output / name).exists():
            paths[name] = file_sha(output / name)
    store = OSSStore(config)
    journal = output / 'upload-journal.json'
    results = {}
    def one(item):
        rel, digest = item
        if not safe_path(rel):
            raise ValueError('unsafe upload key')
        result = transfer(store, prefix+rel, output/rel, digest)
        return rel, dict(sha256=digest, result=result, bytes=(output/rel).stat().st_size)
    with ThreadPoolExecutor(max_workers=jobs) as pool:
        for i, (rel, result) in enumerate(pool.map(one, sorted(paths.items())), 1):
            results[rel] = result
            if i % 100 == 0:
                write_json(journal, results)
                print(f'verified objects: {i}/{len(paths)}', flush=True)
    write_json(journal, results)
    report = dict(prefix=prefix, verified_objects=len(results), verified_bytes=sum(v['bytes'] for v in results.values()),
                  private_manifest_sha256=file_sha(manifest_path), archive_status=manifest['archive_status'],
                  pending_files=sum(r['status']=='PENDING' for r in manifest['files']), real_service_acceptance=False)
    write_json(output / 'upload-report.json', report)
    print(json.dumps(report))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    inv = sub.add_parser('inventory')
    inv.add_argument('--root', action='append', required=True, help='alias=/absolute/path')
    inv.add_argument('--known-root', action='append', default=[])
    inv.add_argument('--output', type=Path, required=True)
    up = sub.add_parser('upload')
    up.add_argument('--output', type=Path, required=True)
    up.add_argument('--prefix', required=True)
    up.add_argument('--config', type=Path, required=True)
    up.add_argument('--jobs', type=int, default=4)
    args = parser.parse_args()
    try:
        if args.command == 'inventory':
            roots = {k: Path(v).resolve() for k,v in (s.split('=',1) for s in args.root)}
            inventory(roots, args.output, args.known_root)
        else:
            upload(args.output, args.prefix, args.config, args.jobs)
        return 0
    except Exception:
        print('FAILED publication operation; values withheld', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
