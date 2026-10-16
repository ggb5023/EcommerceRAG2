#!/usr/bin/env python3
"""Generate public file hashes from actual exports and originals; never hand-edit."""
import argparse
import json
from pathlib import Path

import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'check'))
from publication_scan import Scanner, sha

ROOT = Path(__file__).resolve().parents[2]
SOURCES = {
    'customer-milestone-summary.json': 'customer-milestone-20261014/acceptance/milestone-acceptance-r1.json',
    'customer-quality-summary.json': 'customer-milestone-20261014/acceptance/quality-compose-r4.json',
    'legacy-retirement-summary.json': 'legacy-retirement-20261014/retirement-report.json',
    'build-evidence-summary.json': 'selfhost-build-r5-20261014/build-evidence.json',
}


def generate(root=ROOT, runtime=Path('/var/lib/ecommerce-rag')):
    records = []
    scanner = Scanner()
    for path in sorted((root/'project-records').rglob('*')):
        if not path.is_file() or path.name == 'manifest.json' and path.parent == root/'project-records':
            continue
        if path.is_symlink():
            raise ValueError('public index cannot follow symlinks')
        data = path.read_bytes()
        inspected, why = scanner.inspect(data, path.as_posix())
        if inspected != data:
            raise ValueError('public file needs review: '+path.relative_to(root).as_posix())
        rel = path.relative_to(root).as_posix()
        source = None
        alias = None
        original = None
        if rel.startswith('project-records/fixtures/'):
            alias = 'runtime'
            original = 'real-docs/source-fixtures-v1/' + rel.split('project-records/fixtures/',1)[1]
            source = runtime / original
        elif path.name in SOURCES:
            alias = 'runtime'
            original = SOURCES[path.name]
            source = runtime / original
        records.append(dict(path=rel, size=len(data), classification='reviewed_public_export',
                            source_kind='file' if source else 'generated', source_alias=alias,
                            original_path=original, source_sha256=sha(source.read_bytes()) if source else None,
                            export_sha256=sha(data), redaction_status='reviewed_export' if source else 'generated',
                            redistribution_status='internal_project_record', destination='GitHub_and_OSS'))
    payload = dict(manifest_version='project-records-v1', real_service_acceptance=False, files=records)
    (root/'project-records/manifest.json').write_text(json.dumps(payload, indent=2, ensure_ascii=False)+'\n')
    print('Generated and format-checked public records:', len(records))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args()
    generate()
