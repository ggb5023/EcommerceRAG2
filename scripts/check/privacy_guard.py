"""Check tracked content and reachable history without printing matched values."""
import argparse
import hashlib
import ipaddress
import json
from pathlib import PurePosixPath
import re
import subprocess
import sys

sys.path.insert(0, str(__import__("pathlib").Path(__file__).resolve().parent))
from publication_scan import Scanner, load_known, safe_path

SECRET_SCANNER = Scanner()

PUBLIC_NAME = 'EcommerceRAG Maintainers'
PUBLIC_EMAIL = 'maintainers@example.invalid'
PUBLIC_ARCHITECTURE_PATH = 'docs/architecture.md'
PUBLIC_DOCUMENT_PATHS = {PUBLIC_ARCHITECTURE_PATH, 'scripts/README.md', 'docs/product.md', 'docs/install.md', 'docs/knowledge.md', 'docs/support.md', 'docs/backup.md', 'docs/development.md', 'docs/publication.md'}
PUBLIC_DECISION = re.compile(r'docs/decisions/[0-9]{4}-[a-z0-9-]+\.md')


def public_document(name):
    return name in PUBLIC_DOCUMENT_PATHS or PUBLIC_DECISION.fullmatch(name) is not None
PROJECT_RECORDS_MANIFEST = 'project-records/manifest.json'
PATTERNS = (
    rb'-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----',
    rb'LTAI[A-Za-z0-9]{16,}',
    rb'sk-[A-Za-z0-9._-]{24,}',
    rb'tvly-[A-Za-z0-9_-]{24,}',
)
IP = re.compile(rb'(?<![\w.])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![\w.])')
UUID = re.compile(rb'\b[0-9a-fA-F]{8}-(?:[0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}\b')
EXAMPLES = tuple(ipaddress.ip_network(network) for network in ('192.0.2.0/24', '198.51.100.0/24', '203.0.113.0/24'))


def git(*args, data=None):
    return subprocess.check_output(['git', *args], input=data, stderr=subprocess.PIPE)


def blocked_path(name, allow_project_record=False):
    path = PurePosixPath(name)
    lower = name.lower()
    base = path.name.lower()
    if not safe_path(name):
        return True
    if lower.startswith('project-records/'):
        if any(p.lower() in {'.local', '.ssh', '.git', '.codex', '.private', '.venv', 'node_modules', 'backups'} for p in path.parts):
            return True
        if base.startswith(('.env', 'id_rsa', 'id_ed25519')) or path.suffix.lower() in {'.pem', '.key', '.dump', '.sqlite', '.db', '.log'}:
            return True
        if name != PROJECT_RECORDS_MANIFEST and not allow_project_record:
            return True
        if allow_project_record and path.suffix.lower() in {'.md', '.markdown', '.txt', '.json', '.yaml', '.yml', '.csv', '.html', '.docx', '.bin'}:
            return False
        if allow_project_record:
            return True
    private_dirs = {'docs', 'archive', 'backups', '.private', '.local', '.codex', '.workbuddy-ai', '.ssh', '.venv', 'node_modules'}
    if not public_document(name) and any(part.lower() in private_dirs for part in path.parts):
        return True
    public_markdown = (
        name == 'README.md'
        # FACTS/HANDOFF are retired but remain curated public files in history.
        or name in {'AGENTS.md', 'ROADMAP.md', 'FACTS.md', 'HANDOFF.md'}
        or public_document(name)
        or name == 'eval/README.md'
        or (name.startswith('data/synthetic/') and path.suffix.lower() in {'.md', '.markdown'})
    )
    if path.suffix.lower() in {'.md', '.markdown', '.doc', '.docx', '.pem', '.key', '.dump', '.bundle', '.log', '.bak'} and not public_markdown:
        return True
    if (base.startswith('.env') or base.endswith('.env')) and not base.endswith('.example'):
        return True
    if base.startswith('id_ed25519') or base.startswith('id_rsa'):
        return True
    return any(word in lower for word in ('chat-history', 'chat_history', 'conversation-history', 'conversation_history'))


def project_records_policy(entries, blob_reader):
    """Return the manifest-declared project-record paths and policy failures."""
    names = {name for name, _ in entries if name.startswith('project-records/')}
    if not names:
        return set(), []
    failures = []
    manifest_oids = [oid for name, oid in entries if name == PROJECT_RECORDS_MANIFEST]
    if len(manifest_oids) != 1:
        return set(), ['project-records manifest is required exactly once']
    try:
        payload = json.loads(blob_reader(manifest_oids[0]).decode('utf-8'))
    except (UnicodeDecodeError, json.JSONDecodeError, OSError):
        return set(), ['project-records manifest is invalid']
    if not isinstance(payload, dict) or payload.get('manifest_version') != 'project-records-v1':
        failures.append('project-records manifest version is invalid')
    records = payload.get('files') if isinstance(payload, dict) else None
    if not isinstance(records, list) or not records:
        failures.append('project-records manifest files must be a non-empty list')
        records = []
    allowed = set()
    by_name = dict(entries)
    for index, record in enumerate(records):
        if not isinstance(record, dict):
            failures.append(f'project-records entry {index} is not an object')
            continue
        path = record.get('path')
        if (not isinstance(path, str) or not safe_path(path) or not path.startswith('project-records/')
                or path == PROJECT_RECORDS_MANIFEST):
            failures.append(f'project-records path {index} is invalid')
            continue
        if path in allowed:
            failures.append(f'project-records duplicate: {path}')
        allowed.add(path)
        digest = record.get('export_sha256')
        if not isinstance(digest, str) or not re.fullmatch('[0-9a-f]{64}', digest):
            failures.append(f'project-records invalid digest: {path}')
        source = record.get('source_sha256')
        if source is None:
            if record.get('source_kind') != 'generated':
                failures.append(f'project-records absent source: {path}')
        elif not isinstance(source, str) or not re.fullmatch('[0-9a-f]{64}', source):
            failures.append(f'project-records invalid source digest: {path}')
        if record.get('redistribution_status') != 'internal_project_record':
            failures.append(f'project-records unapproved license: {path}')
        if path in by_name:
            data = blob_reader(by_name[path])
            if hashlib.sha256(data).hexdigest() != digest or record.get('size') != len(data):
                failures.append(f'project-records actual hash/size mismatch: {path}')
            inspected, reasons = SECRET_SCANNER.inspect(data, path)
            if inspected is None or inspected != data:
                failures.append(f'project-records content requires review: {path}')
    actual = names - {PROJECT_RECORDS_MANIFEST}
    for path in sorted(actual - allowed):
        failures.append(f'project-records file is not declared: {path}')
    for path in sorted(allowed - actual):
        failures.append(f'project-records manifest file is missing: {path}')
    return allowed, failures


def private_content(data):
    if SECRET_SCANNER.has_secret(data) or any(re.search(pattern, data) for pattern in PATTERNS):
        return True
    if b'\0' in data:
        return False
    if re.search(rb'(?i)(?<![A-Za-z0-9_])[A-Z]:[/\\][A-Za-z0-9_]', data):
        return True
    if any(value != b'00000000-0000-0000-0000-000000000000' for value in UUID.findall(data)):
        return True
    for value in IP.findall(data):
        try:
            address = ipaddress.ip_address(value.decode('ascii'))
        except ValueError:
            continue
        if not (address.is_loopback or address.is_unspecified or any(address in network for network in EXAMPLES)):
            return True
    return False


def scan_entries(entries, seen):
    entries = list(entries)
    failures = []
    allowed_project, manifest_failures = project_records_policy(
        entries, lambda oid: git('cat-file', 'blob', oid)
    )
    failures.extend(manifest_failures)
    for name, oid in entries:
        if blocked_path(name, allow_project_record=name in allowed_project):
            failures.append('private path: '+name)
        if oid not in seen:
            seen.add(oid)
            # Go module checksum manifests contain dependency UUID-like text and
            # public IP-looking tokens; they are integrity metadata, not secrets.
            data = git('cat-file', 'blob', oid)
            if name.endswith('/go.sum'):
                continue
            if private_content(data):
                failures.append('private content in: '+name)
    return failures


def tree_entries(commit):
    for item in git('ls-tree', '-r', '-z', commit).split(b'\0'):
        if item:
            meta, name = item.split(b'\t', 1)
            _, kind, oid = meta.split()
            if kind == b'blob':
                yield name.decode('utf-8'), oid.decode('ascii')


def index_entries():
    for item in git('ls-files', '--stage', '-z').split(b'\0'):
        if item:
            meta, name = item.split(b'\t', 1)
            _, oid, stage = meta.split()
            if stage != b'0':
                raise ValueError('Unmerged index cannot be checked')
            yield name.decode('utf-8'), oid.decode('ascii')


def check_identity():
    prefix = (PUBLIC_NAME+' <'+PUBLIC_EMAIL+'> ').encode()
    return all(git('var', kind).startswith(prefix) for kind in ('GIT_AUTHOR_IDENT', 'GIT_COMMITTER_IDENT'))


def scan_history(extra_refs=()):
    failures = []
    seen = set()
    commits = git('rev-list', '--all', *extra_refs, '--').decode().splitlines()
    for commit in commits:
        metadata = git('show', '-s', '--format=%an%x00%ae%x00%cn%x00%ce%x00%B', commit).split(b'\0', 4)
        expected = [PUBLIC_NAME.encode(), PUBLIC_EMAIL.encode()] * 2
        if metadata[:4] != expected or private_content(metadata[4]):
            failures.append('private commit metadata: '+commit[:12])
        failures.extend(scan_entries(tree_entries(commit), seen))
    for line in git('for-each-ref', '--format=%(objecttype) %(objectname)', 'refs/tags').decode().splitlines():
        kind, oid = line.split()
        if kind == 'tag':
            data = git('cat-file', 'tag', oid)
            tagger = re.search(rb'^tagger (.*?) <(.*?)> ', data, re.M)
            if private_content(data) or (tagger and list(tagger.groups()) != [PUBLIC_NAME.encode(), PUBLIC_EMAIL.encode()]):
                failures.append('private tag metadata: '+oid[:12])
    return failures


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--history', action='store_true')
    parser.add_argument('--pre-push', action='store_true')
    parser.add_argument('--identity', action='store_true')
    parser.add_argument('--known-root', action='append', default=[])
    args = parser.parse_args()
    try:
        load_known(SECRET_SCANNER, ['/etc/ecommerce-rag', '/root/.codex/auth.json', *args.known_root])
        extra = []
        if args.pre_push:
            for line in sys.stdin:
                fields = line.split()
                if len(fields) != 4 or not re.fullmatch('[0-9a-f]{40,64}', fields[1]):
                    raise ValueError('Invalid push input')
                if set(fields[1]) != {'0'}:
                    extra.append(fields[1])
        failures = scan_history(extra) if args.history or args.pre_push else scan_entries(index_entries(), set())
        if args.identity and not check_identity():
            failures.append('repository commit identity must be the public project identity')
    except (OSError, ValueError, subprocess.CalledProcessError):
        print('BLOCKED privacy check could not complete; details withheld')
        return 2
    for failure in sorted(set(failures)):
        print('BLOCKED '+failure)
    if failures:
        return 1
    print('PASS privacy exclusions, credential patterns, environment values and requested metadata')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
