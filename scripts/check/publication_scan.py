"""Format-aware archive inspection. Findings never include matched secret values."""
from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
from urllib.parse import quote
import zipfile

SECRET_KEY = re.compile(r'(?:password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|csrf|cookie|(?:^|[_-])token(?:$|[_-])|authorization|session[_-]?hash|scope[_-]?key)', re.I)
PLACEHOLDER = re.compile(r'^(?:\[REDACTED[^\]]*\]|<[^>]*>|\$\{[^}]*\}|(?:change|replace|example|dummy|test|fixture|invalid|redacted|not[-_]set|your)[\w ._-]*|false|true|none|null)$', re.I)
PATTERNS = [
    re.compile(rb'-----BEGIN (?:OPENSSH |RSA |EC |ENCRYPTED )?PRIVATE KEY-----[\s\S]*?-----END (?:OPENSSH |RSA |EC |ENCRYPTED )?PRIVATE KEY-----'),
    re.compile(rb'\b(?:sk-[A-Za-z0-9._-]{24,}|tvly-[A-Za-z0-9_-]{24,}|LTAI[A-Za-z0-9]{16,})'),
    re.compile(rb'(?i)authorization[\"\x27 ]*[:=][\"\x27 ]*(?:Bearer|Basic) [A-Za-z0-9._~+/=-]{20,}'),
    re.compile(rb'(?i)(?:set-cookie|cookie)[\"\x27 ]*[:=][\"\x27 ]*[A-Za-z0-9_-]+=[A-Za-z0-9._~+/=-]{20,}'),
    re.compile('(?:密码|口令|密钥|令牌)[：:=| \t]+[A-Za-z0-9@!#$%&*:._+-]{8,}'.encode()),
    re.compile(rb'\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}'),
    re.compile(rb'(?i)\b(?:postgres(?:ql)?|redis|mysql)://[^\s/\"\x27]+:[^\s@\"\x27]+@[^\s\"\x27]+'),
    re.compile(rb'(?i)https?://[^\s<>\"\x27]+[?&](?:signature|x-oss-signature|x-amz-signature|security-token|ossaccesskeyid)=[^\s<>\"\x27]+'),
    # A complete PHC digest is a credential; a bare "$argon2id$v=" format prefix
    # in source code is not, so the argon2 branch requires every field.
    re.compile(rb'\$(?:2[aby]\$\d\d\$[A-Za-z0-9./]{40,}|argon2(?:id|i|d)\$v=\d+\$m=\d+,t=\d+,p=\d+\$[A-Za-z0-9+/=]{8,}\$[A-Za-z0-9+/=]{8,})|SCRAM-SHA-256\$[^\s\"\x27]+'),
    # Parameter-less PHC (missing the m/t/p segment) is still a digest, not a
    # format prefix: "$argon2id$v=" stays 4 characters and cannot match {8,}.
    re.compile(rb'\$argon2(?:id|i|d)\$[A-Za-z0-9+/=]{8,}\$[A-Za-z0-9+/=]{8,}'),
]
ASSIGN = re.compile(rb'''(?im)(["']?[A-Za-z_][A-Za-z0-9_.-]*(?:["']?)\s*[:=]\s*)("(?:\\.|[^"\\])*"|'[^'\n]*'|[^\s,;\n]+)''')
MAX_EXPANDED = 128 * 1024 * 1024
MAX_MEMBERS = 10000


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe_path(name):
    return bool(name and '\\' not in name and '\x00' not in name and not name.startswith('/')
                and not re.match(r'^[A-Za-z]:', name)
                and all(p not in ('', '.', '..') for p in name.rstrip('/').split('/')))


def sensitive_value(key, value):
    if re.search(r'(?:_env|_file|_path|_url|_endpoint|_name|_header)$', key, re.I):
        return False
    if isinstance(value, str) and (re.fullmatch(r'[A-Z][A-Z_]{5,}', value) or value.startswith(('http://', 'https://'))):
        return False
    if 'authorization' in key.lower() and not (isinstance(value, str) and re.match(r'(?i)^(?:Bearer|Basic) ', value)):
        return False
    return bool(SECRET_KEY.search(key) and isinstance(value, str) and len(value) >= 6
                and not PLACEHOLDER.fullmatch(value) and not value.startswith(('os.', 'settings.', 'config.', 'self.', 'process.', 'env.', '${')))


class Scanner:
    def __init__(self):
        self.known = set()

    def remember(self, value):
        if isinstance(value, str) and len(value) >= 8 and not PLACEHOLDER.fullmatch(value):
            for variant in (value, json.dumps(value, ensure_ascii=False)[1:-1], quote(value, safe='')):
                self.known.add(variant.encode())

    def learn(self, data, name=''):
        try:
            obj = json.loads(data)
            def walk(o):
                if isinstance(o, dict):
                    if isinstance(o.get('name'), str) and re.search(r'cookie|session|csrf|ecr|keycloak|token', o['name'], re.I):
                        self.remember(o.get('value'))
                    for k, v in o.items():
                        if sensitive_value(k, v):
                            self.remember(v)
                        walk(v)
                elif isinstance(o, list):
                    for v in o:
                        walk(v)
            walk(obj)
        except (ValueError, UnicodeDecodeError):
            pass
        for m in ASSIGN.finditer(data):
            if not name.endswith('.env') and not m[1].lstrip().startswith((b'"', b"'")) and not re.split(rb'[:=]', m[1])[0].strip().isupper():
                continue
            k = re.split(rb'[:=]', m[1])[0].strip(b' \t\"\x27').decode('utf-8', 'ignore')
            v = m[2].strip(b'\"\x27').decode('utf-8', 'ignore')
            if sensitive_value(k, v):
                self.remember(v)
        for pattern in PATTERNS:
            for m in pattern.finditer(data):
                self.remember(m[0].decode('utf-8', 'ignore'))
        if re.search(r'(?:^|/)(?:scope-key|browser-secret|pg-password|.*client-secret)$', name):
            try:
                self.remember(data.decode().strip())
            except UnicodeDecodeError:
                pass

    def redact(self, data, env=False):
        original = data
        changes = set()
        for value in sorted(self.known, key=len, reverse=True):
            if value in data:
                data = data.replace(value, b'[REDACTED:known-secret]')
                changes.add('known-secret')
        for i, pattern in enumerate(PATTERNS):
            data, n = pattern.subn(b'[REDACTED:credential]', data)
            if n:
                changes.add('credential-pattern-' + str(i))
        def assignment(m):
            if not env and not m[1].lstrip().startswith((b'"', b"'")) and not re.split(rb'[:=]', m[1])[0].strip().isupper():
                return m[0]
            k = re.split(rb'[:=]', m[1])[0].strip(b' \t\"\x27').decode('utf-8', 'ignore')
            raw = m[2]
            value = raw.strip(b'\"\x27').decode('utf-8', 'ignore')
            if sensitive_value(k, value):
                changes.add('credential-field')
                q = raw[:1] if raw[:1] in (b'"', b"'") else b''
                return m[1] + q + b'[REDACTED:credential-field]' + q
            return m[0]
        data = ASSIGN.sub(assignment, data)
        return data, sorted(changes) if data != original else []

    def has_secret(self, data):
        return any(v in data for v in self.known) or any(p.search(data) for p in PATTERNS)

    def inspect(self, data, name, depth=0):
        """Return safe export or hold reason. Containers are never extracted to disk."""
        if depth > 4:
            return None, ['archive-depth-limit']
        suffix = PurePosixPath(name).suffix.lower()
        if data.startswith(b'PK\x03\x04'):
            try:
                with zipfile.ZipFile(io.BytesIO(data)) as z:
                    infos = z.infolist()
                    if len(infos) > MAX_MEMBERS or sum(i.file_size for i in infos) > MAX_EXPANDED:
                        return None, ['archive-expansion-limit']
                    if len({i.filename for i in infos}) != len(infos):
                        return None, ['archive-duplicate-member']
                    out, methods = io.BytesIO(), set()
                    with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as dest:
                        for item in infos:
                            if not safe_path(item.filename) or (item.external_attr >> 16) & 0o170000 == 0o120000:
                                return None, ['archive-unsafe-path-or-symlink']
                            if item.is_dir():
                                continue
                            value, why = self.inspect(z.read(item), item.filename, depth+1)
                            if value is None:
                                return None, why
                            methods.update(why)
                            dest.writestr(item, value)
                    return (out.getvalue() if methods else data), sorted(methods)
            except (zipfile.BadZipFile, RuntimeError, OSError):
                return None, ['archive-unreadable']
        if suffix in ('.gz', '.tgz', '.tar', '.zip', '.docx') and (data.startswith(b'\x1f\x8b') or suffix == '.tar'):
            try:
                members, total, methods = [], 0, set()
                with tarfile.open(fileobj=io.BytesIO(data), mode='r:*') as archive:
                    for item in archive:
                        if len(members) >= MAX_MEMBERS or not safe_path(item.name):
                            return None, ['archive-limit-or-unsafe-path']
                        if item.isdir():
                            continue
                        if not item.isfile():
                            return None, ['archive-link-or-special-member']
                        total += item.size
                        if total > MAX_EXPANDED:
                            return None, ['archive-expansion-limit']
                        value, why = self.inspect(archive.extractfile(item).read(), item.name, depth+1)
                        if value is None:
                            return None, why
                        methods.update(why)
                        members.append((item, value))
                if not methods:
                    return data, []
                out = io.BytesIO()
                with tarfile.open(fileobj=out, mode='w:gz' if data.startswith(b'\x1f\x8b') else 'w') as dest:
                    for item, value in members:
                        item.size = len(value)
                        dest.addfile(item, io.BytesIO(value))
                return out.getvalue(), sorted(methods)
            except (tarfile.TarError, OSError, EOFError):
                return None, ['archive-unreadable']
        if not data:
            return data, []
        if data.startswith((b'\xff\xfe', b'\xfe\xff')):
            try:
                text = data.decode('utf-16')
                cleaned, why = self.redact(text.encode('utf-8'))
                return (cleaned.decode('utf-8').encode('utf-16') if why else data), why
            except UnicodeError:
                return None, ['invalid-utf16-needs-review']
        if b'\x00' in data or data.startswith((b'%PDF', b'\x89PNG', b'\xff\xd8', b'\x7fELF', b'SQLite format', b'PGDMP')):
            return None, ['binary-needs-format-and-content-review']
        try:
            data.decode('utf-8')
        except UnicodeDecodeError:
            # Invalid parser fixtures can be inspected byte-for-byte; other binary
            # encodings need a dedicated reviewer.
            if not ('invalid' in name and suffix == '.csv' and len(data) < 1024):
                return None, ['non-utf8-needs-review']
        result, methods = self.redact(data, env=suffix=='.env')
        if self.has_secret(result):
            return None, ['secret-remains-after-redaction']
        return result, methods


def load_known(scanner, roots):
    """Only memory retains secret values; do not archive this registry."""
    for root in roots:
        root = Path(root)
        candidates = [root] if root.is_file() else root.rglob('*')
        for path in candidates:
            if path.is_symlink() or not path.is_file():
                continue
            if path.stat().st_size > 4*1024*1024:
                continue
            if any(part in ('node_modules', '.venv', 'venv', 'cpython') for part in path.parts):
                continue
            is_json_config = path.suffix.lower() == '.json' and bool(re.search(r'auth|account|browser.state|identity|realm|config|secret|oidc', path.name, re.I))
            if path.suffix.lower() == '.env' or is_json_config or SECRET_KEY.search(path.name) or path.name in ('scope-key', 'browser-secret', 'pg-password'):
                try:
                    scanner.learn(path.read_bytes(), str(path))
                except OSError:
                    continue
