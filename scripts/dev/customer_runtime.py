#!/usr/bin/env python3
"""Controlled selfhost runtime for the customer/keycloak browser specs.

The runtime is a fresh Compose project with its own volume. The gateway listens
on the origin port inside the container (and is published on the same port).
Because discovery runs before that listener exists, OIDC backchannel calls use
the internal Keycloak service (backchannel_url) while tokens keep the public
issuer https://127.0.0.1:18443/identity/...

Commands:
  up        prepare the protected runtime and start the stack
  status    show Compose service state and origin health
  run       run the four phase-1 specs in the controlled order
  run-spec  run one browser spec (customer-*, keycloak, selfhost-restore, login)
  restore   phase 2: stop writers, back up, restore into a new project, verify
  down      stop the stack (--clean removes volumes and the runtime directory)

Exit codes: 0 pass, 2 unconfigured prerequisite, 1 failure. Secrets are never
printed; the provider file is read once by scripts/ops/selfhost.py into the
protected runtime directory and stays outside the repository.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit
from xml.sax.saxutils import escape
from zipfile import ZIP_DEFLATED, ZipFile

ROOT = Path(__file__).resolve().parents[2]
RUNTIME_ROOT = Path(os.environ.get('CUSTOMER_RUNTIME_ROOT', str(ROOT / '.local' / 'controlled-runtime')))
RUNTIME = Path(os.environ.get('CUSTOMER_RUNTIME_DIR', str(RUNTIME_ROOT / 'runtime')))
# selfhost-restore.py refuses a destination inside the repository, so the
# restored copy lives beside the worktree rather than under it.
RESTORE_ROOT = Path(os.environ.get('CUSTOMER_RESTORE_ROOT',
                                   str(Path(tempfile.gettempdir()) / 'ecommerce-rag-controlled-restore')))
REPORT_ROOT = Path(os.environ.get('CUSTOMER_REPORT_ROOT', '/var/lib/ecommerce-rag/wp91'))
ORIGIN = os.environ.get('CUSTOMER_BASE_URL', 'https://127.0.0.1:18443').rstrip('/')
PROJECT = os.environ.get('CUSTOMER_PROJECT', 'ecr-wp91')
PROVIDER_CONFIG = Path(os.environ.get('ECR_PROVIDER_CONFIG', '/etc/ecommerce-rag/providers.env'))
COMPOSE = ROOT / 'docker/selfhost/compose.yml'
PYTHON = ROOT / 'python/.venv/bin/python'
FRONTEND = ROOT / 'frontend'
CHROMIUM = os.environ.get('CHROMIUM_PATH', '/root/.cache/ms-playwright/chromium-1187/chrome-linux/chrome')
STATE_FILE = RUNTIME_ROOT / 'state.json'
# The gateway rate-limits logins per source IP (localauth.IPMinuteLimit = 10).
# One login spec run sends 8 attempts, so a second run started immediately
# would be rejected with 429 before its fifth check. Remember when the previous
# run ended and let the sliding minute window drain first.
LOGIN_MARKER = ROOT / '.local' / 'local-login' / 'last-run'
LOGIN_RATE_WINDOW = 65
PHASE1 = ('customer-service', 'customer-source', 'customer-control', 'keycloak')
CONTROLLED = ('customer-service', 'customer-source', 'customer-control', 'keycloak', 'selfhost-restore')
WRITERS = ('gateway', 'ai', 'ingest', 'worker', 'keycloak')
# The teardown sweep may only remove projects this harness creates. Anything
# else (notably the long-lived ecr-merchant project) is left untouched.
HARNESS_PROJECT = re.compile(r'^(?:ecr-wp91|ecr-restore-wp91-[a-z0-9-]+)$')


class Unconfigured(Exception):
    """A missing host prerequisite; the gate reports UNCONFIGURED, never PASS."""


def info(message):
    print(message, flush=True)


def unconfigured(reason):
    raise Unconfigured(reason)


def run_id():
    return datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex[:6]


def state():
    try:
        return json.loads(STATE_FILE.read_text())
    except (OSError, ValueError):
        return {}


def save_state(value):
    RUNTIME_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    with open(os.open(STATE_FILE, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as stream:
        json.dump(value, stream, indent=2)


def command(args, *, env=None, cwd=ROOT, timeout=None, check=True, capture=False, input=None):
    completed = subprocess.run(args, cwd=cwd, env=env, timeout=timeout, input=input,
                               stdout=subprocess.PIPE if capture else None,
                               stderr=subprocess.PIPE if capture else None,
                               text=True, check=False)
    if check and completed.returncode != 0:
        raise RuntimeError(f'command failed ({completed.returncode}): {Path(args[0]).name}')
    return completed


def docker_ready():
    if not shutil.which('docker'):
        return False
    try:
        return command(['docker', 'info', '--format', '{{.ServerVersion}}'], timeout=15, capture=True).returncode == 0
    except (OSError, subprocess.TimeoutExpired):
        return False


def compose(env_file, args, *, check=True, capture=False, timeout=None):
    return command(['docker', 'compose', '--env-file', str(env_file), '-f', str(COMPOSE), *args],
                   check=check, capture=capture, timeout=timeout)


def keycloak_image():
    match = re.search(r'image:\s*(quay\.io/keycloak/keycloak@sha256:[0-9a-f]{64})', COMPOSE.read_text())
    if not match:
        unconfigured('docker/selfhost/compose.yml has no pinned Keycloak digest')
    return match.group(1)


def image_present(ref):
    try:
        return command(['docker', 'image', 'inspect', ref], check=False, capture=True, timeout=20).returncode == 0
    except (OSError, subprocess.TimeoutExpired):
        return False


def ensure_keycloak_image():
    ref = keycloak_image()
    if image_present(ref):
        return ref
    info(f'preflight: pulling pinned Keycloak image {ref}')
    try:
        command(['docker', 'pull', ref], check=False, capture=True, timeout=300)
    except (OSError, subprocess.TimeoutExpired):
        pass
    if not image_present(ref):
        # Never substitute a tag for the pinned digest.
        unconfigured(f'pinned Keycloak image {ref} is unavailable; no substitute image is used')
    return ref


def ensure_built_images(env_file, build=False):
    refs = []
    text = COMPOSE.read_text()
    for name in ('ecommerce-rag-go:selfhost', 'ecommerce-rag-python:selfhost'):
        if name in text:
            refs.append(name)
    missing = [ref for ref in refs if not image_present(ref)]
    if build or missing:
        info('building selfhost images from the repository')
        build_args = []
        if os.environ.get('GOPROXY'):
            # Containers do not inherit the host Go proxy; pass it explicitly so
            # a restricted network does not stall the build on proxy.golang.org.
            build_args = ['--build-arg', 'GOPROXY=' + os.environ['GOPROXY']]
        command(['docker', 'compose', '--env-file', str(env_file), '-f', str(COMPOSE),
                 'build', *build_args], timeout=None)


def port_in_use(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.settimeout(0.5)
        return probe.connect_ex(('127.0.0.1', port)) == 0


def origin_parts():
    parsed = urlsplit(ORIGIN)
    if parsed.scheme != 'https' or parsed.hostname not in ('127.0.0.1', 'localhost', '::1'):
        unconfigured('the controlled runtime origin must be loopback HTTPS')
    return parsed


def node_ready():
    return (shutil.which('node') is not None and (FRONTEND / 'node_modules' / 'playwright').exists()
            and Path(CHROMIUM).exists())


def preflight(*, need_node=False):
    if not docker_ready():
        unconfigured('docker is unavailable')
    if not PROVIDER_CONFIG.exists() or not os.access(PROVIDER_CONFIG, os.R_OK):
        unconfigured(f'missing readable provider config {PROVIDER_CONFIG}')
    parsed = origin_parts()
    ensure_keycloak_image()
    if need_node and not node_ready():
        unconfigured('node, frontend/node_modules or Playwright Chromium is missing')
    if not stack_running() and port_in_use(parsed.port or 443):
        unconfigured(f'port {parsed.port or 443} is already in use by another process')


def stack_running():
    env_file = RUNTIME / 'compose.env'
    if not env_file.exists():
        return False
    try:
        result = compose(env_file, ['ps', '--status', 'running', '--services'], check=False, capture=True, timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return 'gateway' in result.stdout.split()


def health(timeout=2):
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    try:
        with urllib.request.urlopen(ORIGIN + '/healthz', timeout=timeout, context=context) as response:
            return response.status == 200
    except Exception:
        return False


def wait_health(seconds=180):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if health():
            return True
        time.sleep(2)
    return False


def docx_bytes(markdown):
    """Minimal WordprocessingML package accepted by the fallback DOCX parser."""
    paragraphs = ''.join(
        '<w:p><w:r><w:t xml:space="preserve">' + escape(line) + '</w:t></w:r></w:p>'
        for line in markdown.splitlines() if line.strip()
    )
    document = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
                '<w:body>' + paragraphs + '</w:body></w:document>')
    content_types = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                     '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
                     '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                     '<Default Extension="xml" ContentType="application/xml"/>'
                     '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>'
                     '</Types>')
    relationships = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                     '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                     '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>'
                     '</Relationships>')
    import io
    buffer = io.BytesIO()
    with ZipFile(buffer, 'w', ZIP_DEFLATED) as archive:
        archive.writestr('[Content_Types].xml', content_types)
        archive.writestr('_rels/.rels', relationships)
        archive.writestr('word/document.xml', document)
    return buffer.getvalue()


def prepare_runtime_files():
    """Create the fixtures the browser specs read from the protected runtime."""
    bootstrap = json.loads((RUNTIME / 'bootstrap/accounts.json').read_text())
    accounts = bootstrap.get('accounts') or []
    dev = next((account for account in accounts if account.get('username') == 'dev-user'), None)
    if not dev:
        raise RuntimeError('runtime has no dev-user OIDC account')
    accounts_path = RUNTIME / 'keycloak-accounts.json'
    with open(os.open(accounts_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as stream:
        json.dump({'username': dev['username'], 'password': dev['password'],
                   'role': dev.get('role'), 'subject': dev.get('subject')}, stream)
    os.chmod(accounts_path, 0o600)
    acceptance = RUNTIME / 'acceptance'
    acceptance.mkdir(mode=0o700, exist_ok=True)
    docx = acceptance / 'returns.docx'
    if not docx.exists():
        docx.write_bytes(docx_bytes((ROOT / 'data/synthetic/customer-service-v1/returns.md').read_text()))
        os.chmod(docx, 0o600)
    REPORT_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(REPORT_ROOT, 0o700)


def init_runtime():
    args = [str(PYTHON), str(ROOT / 'scripts/ops/selfhost.py'), 'init',
            '--directory', str(RUNTIME), '--origin', ORIGIN,
            '--provider-config', str(PROVIDER_CONFIG), '--local-certificate',
            '--identity-profile', 'oidc_browser', '--owner-username', 'admin',
            '--project', PROJECT, '--bind', '127.0.0.1',
            '--listen-port', str(origin_parts().port or 443), '--dev-user']
    if os.geteuid() != 0:
        unconfigured('root is required to prepare the protected runtime')
    if not PYTHON.exists():
        unconfigured(f'missing {PYTHON}; run uv sync --frozen')
    completed = command(args, check=False, capture=True)
    if completed.returncode != 0:
        raise RuntimeError('selfhost.py init failed: check origin, provider config and empty directory')
    info(completed.stdout.strip())


def container_id(env_file, service):
    # Exited one-shot containers are only listed with --all.
    result = compose(env_file, ['ps', '-a', '-q', service], check=False, capture=True, timeout=30)
    ids = result.stdout.strip().splitlines()
    return ids[-1] if result.returncode == 0 and ids else None


def wait_completed(env_file, service, timeout=300):
    """Wait for a one-shot service without `up --wait`, which races an instant exit."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        cid = container_id(env_file, service)
        if cid:
            result = command(['docker', 'inspect', '-f', '{{.State.Status}} {{.State.ExitCode}}', cid],
                             check=False, capture=True, timeout=20)
            fields = result.stdout.split()
            if result.returncode == 0 and len(fields) == 2 and fields[0] == 'exited':
                if fields[1] != '0':
                    raise RuntimeError(f'one-shot {service} exited {fields[1]}')
                return
        time.sleep(2)
    raise RuntimeError(f'one-shot {service} did not complete within {timeout}s')


def start_stack(env_file, *, no_deps=False, build=True):
    # The images must contain this checkout: a stale cached image silently ran
    # an older migration set in the first controlled-runtime bring-up.
    ensure_built_images(env_file, build=build)
    # Ordered startup avoids two real races: Keycloak cannot start before
    # db-init creates its database, and it must not start before bootstrap
    # writes the realm file it imports. The gateway must not start discovery
    # while Keycloak is still importing the realm (Compose then observes the
    # crash-restarting gateway as unhealthy and aborts `up --wait`).
    if not no_deps:
        compose(env_file, ['--profile', 'oidc', 'up', '-d',
                           'postgres', 'db-init', 'migrate', 'bootstrap'], timeout=None)
        for service in ('db-init', 'migrate', 'bootstrap'):
            wait_completed(env_file, service)
    compose(env_file, ['--profile', 'oidc', 'up', '-d', '--wait',
                       *(['--no-deps'] if no_deps else []), 'keycloak'], timeout=None)
    services = ['gateway', 'ai']
    compose(env_file, ['--profile', 'oidc', 'up', '-d', '--wait',
                       *(['--no-deps'] if no_deps else []), *services], timeout=None)
    if not wait_health():
        raise RuntimeError(f'gateway did not become healthy at {ORIGIN}')


def up(args):
    env_file = RUNTIME / 'compose.env'
    if env_file.exists():
        if args.fresh:
            down(argparse.Namespace(clean=True))
        elif stack_running() and health():
            prepare_runtime_files()
            info(f'PASS controlled runtime already up at {ORIGIN}')
            return 0
        elif stack_running():
            if not wait_health():
                raise RuntimeError(f'gateway is running but {ORIGIN}/healthz is not healthy')
        else:
            preflight()
            prepare_runtime_files()
            start_stack(env_file)
            info(f'PASS controlled runtime started at {ORIGIN}')
            return 0
    if not env_file.exists():
        preflight()
        if RUNTIME.exists():
            raise RuntimeError(f'runtime directory {RUNTIME} exists without compose.env; run down --clean')
        init_runtime()
        prepare_runtime_files()
        start_stack(env_file)
        info(f'PASS controlled runtime prepared at {ORIGIN}')
    else:
        info(f'PASS controlled runtime up at {ORIGIN}')
    return 0


def status(_args):
    if not (RUNTIME / 'compose.env').exists():
        info(f'UNCONFIGURED controlled runtime {RUNTIME} has not been prepared')
        return 2
    compose(RUNTIME / 'compose.env', ['--profile', 'oidc', 'ps'], check=False)
    info(f'origin {ORIGIN} health={"ok" if health() else "down"}')
    return 0


def report_path(name, run):
    parent = REPORT_ROOT / 'runs' / run
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(parent, 0o700)
    return parent / name


def remember_report(name, path):
    value = state()
    value.setdefault('reports', {})[name] = str(path)
    save_state(value)


def spec_environment(name, out):
    env = dict(os.environ)
    env.update({'CUSTOMER_RUNTIME_DIR': str(RUNTIME), 'CUSTOMER_BASE_URL': ORIGIN,
                'CUSTOMER_REPORT_DIR': str(out), 'CHROMIUM_PATH': CHROMIUM})
    if name == 'customer-control':
        env['CUSTOMER_ALLOW_OWNED_RESTART'] = '1'
    if name == 'keycloak':
        env.update({'WORKSPACE_URL': ORIGIN,
                    'KEYCLOAK_ACCOUNTS_FILE': str(RUNTIME / 'keycloak-accounts.json'),
                    'KEYCLOAK_BROWSER_REPORT_DIR': str(out),
                    'KEYCLOAK_BROWSER_IGNORE_HTTPS': '1',
                    'KEYCLOAK_BROWSER_PROVIDER_PROFILE': 'v2_langchain_bailian'})
    return env


def local_identity_state(gateway):
    """Health and session state of the local identity stack; never raises urllib errors."""
    health_url = f'http://{gateway}/healthz'
    session_url = f'http://{gateway}/v1/auth/session'
    try:
        with urllib.request.urlopen(health_url, timeout=3) as response:
            if response.status != 200:
                raise RuntimeError(f'local identity stack at http://{gateway} is not healthy')
        with urllib.request.urlopen(session_url, timeout=3) as response:
            return json.loads(response.read())
    except (OSError, ValueError):
        raise RuntimeError(f'local identity stack at http://{gateway} is not reachable') from None


def validate_local_identity(gateway):
    state = local_identity_state(gateway)
    if state.get('mode') != 'local':
        raise RuntimeError(f'local identity stack at http://{gateway} is not running the local profile '
                           f'(mode={state.get("mode") or "unknown"}); restart it with --identity local')


def local_login_environment():
    ports_file = Path(os.environ.get('ECR_LOCAL_PORTS_FILE', '/tmp/ecr-isolated-local/ports.json'))
    browser_env = ports_file.parent / 'browser.env'
    runtime_env = ports_file.parent / 'runtime.env'
    for required in (ports_file, browser_env, runtime_env):
        if not required.exists():
            unconfigured(f'missing {required}; start the local identity stack with '
                         f'isolated-stack.sh --identity local --ports 26091')
    try:
        ports = json.loads(ports_file.read_text())
    except ValueError:
        raise RuntimeError(f'{ports_file} is not valid JSON; restart the local identity stack') from None
    gateway = ports.get('gateway')
    vite = ports.get('vite')
    if not gateway:
        unconfigured(f'{ports_file} has no gateway address')
    if not vite:
        unconfigured('the local identity stack has no Vite port')
    env = dict(os.environ)
    values = {}
    for line in browser_env.read_text().splitlines():
        key, _, value = line.partition('=')
        values[key] = value
    password = values.get('LOGIN_PASSWORD')
    new_password = values.get('LOGIN_NEW_PASSWORD') or os.environ.get('LOGIN_NEW_PASSWORD')
    if not password:
        unconfigured(f'{browser_env} has no LOGIN_PASSWORD')
    if not new_password:
        unconfigured(f'{browser_env} has no LOGIN_NEW_PASSWORD; restart the local identity stack')
    validate_local_identity(gateway)
    env.update({'WORKSPACE_URL': f'http://{vite}', 'ECR_BROWSER_ENV': str(browser_env),
                'ECR_LOCAL_PORTS_FILE': str(ports_file), 'ECR_LOCAL_RUNTIME_ENV': str(runtime_env),
                'LOGIN_USER': values.get('LOGIN_USER', 'stack-login'), 'LOGIN_PASSWORD': password,
                'LOGIN_NEW_PASSWORD': new_password, 'CHROMIUM_PATH': CHROMIUM})
    return env


def reset_local_login(env):
    """Reset the seeded local account before each login spec run.

    reset-password clears the lock state, revokes live sessions and forces a
    password change on the next login, so a failed earlier run cannot poison the
    next one. The password travels on stdin only.
    """
    runtime_env = Path(env['ECR_LOCAL_RUNTIME_ENV'])
    if not runtime_env.exists():
        unconfigured(f'missing {runtime_env}; start the local identity stack')
    user = env['LOGIN_USER']
    completed = command(['go', '-C', str(ROOT / 'go'), 'run', './cmd/local-account',
                         'reset-password', '--env', str(runtime_env), '--user', user],
                        check=False, capture=True, input=env['LOGIN_PASSWORD'] + '\n', timeout=300)
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout or '').strip()
        raise RuntimeError(f'could not reset local login account {user}' + (f': {detail}' if detail else ''))
    info(f'PASS reset local login account {user} (forced change on next login, sessions revoked)')


def wait_login_rate_window():
    """Sleep out the gateway's per-IP login window left by the previous run."""
    try:
        last = LOGIN_MARKER.stat().st_mtime
    except OSError:
        return
    remaining = LOGIN_RATE_WINDOW - (time.time() - last)
    if remaining > 0:
        info(f'WAIT {remaining:.0f}s for the local login rate-limit window '
             f'({LOGIN_RATE_WINDOW}s after the previous login spec run)')
        time.sleep(remaining)


def mark_login_rate_window():
    LOGIN_MARKER.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    LOGIN_MARKER.touch(mode=0o600)
    os.chmod(LOGIN_MARKER, 0o600)


def run_spec(name, *, report=None):
    if name not in CONTROLLED and name != 'login':
        raise RuntimeError(f'unknown spec {name}')
    if name == 'login':
        # Login uses the local identity stack, not the controlled Compose project.
        env = local_login_environment()
        wait_login_rate_window()
        reset_local_login(env)
        try:
            completed = command(['node', 'tests/login.browser.mjs'], cwd=FRONTEND, env=env, check=False)
        finally:
            # The marker is written even on failure: the attempts already hit
            # the limiter and the next run must still wait for the window.
            mark_login_rate_window()
        info(f'{"PASS" if completed.returncode == 0 else "FAIL"} login spec')
        return completed.returncode
    if not (RUNTIME / 'compose.env').exists():
        unconfigured(f'controlled runtime {RUNTIME} has not been prepared')
    if name == 'selfhost-restore':
        return restore(argparse.Namespace(keep=False))
    if not stack_running() and not health():
        unconfigured(f'controlled runtime at {ORIGIN} is not running')
    if not node_ready():
        unconfigured('node, frontend/node_modules or Playwright Chromium is missing')
    out = report or report_path(name, run_id())
    env = spec_environment(name, out)
    remember_report(name, out)
    cmd = ['node', f'tests/{name}.browser.mjs']
    completed = command(cmd, cwd=FRONTEND, env=env, check=False)
    if completed.returncode == 0:
        info(f'PASS controlled spec {name}')
    else:
        info(f'FAIL controlled spec {name} (exit {completed.returncode}); inspect the protected report directory')
    return completed.returncode


def run(_args):
    result = 0
    for name in PHASE1:
        code = run_spec(name)
        if code != 0:
            result = code
            break
    return result


def running_services(env_file):
    result = compose(env_file, ['ps', '--status', 'running', '--services'], capture=True, check=False)
    return set(result.stdout.split())


def stop_writers(env_file):
    services = sorted(running_services(env_file).intersection(WRITERS))
    if services:
        compose(env_file, ['stop', *services], timeout=120)
    remaining = running_services(env_file).intersection(WRITERS)
    if remaining:
        raise RuntimeError('source writers did not stop: ' + ','.join(sorted(remaining)))


def restore(args):
    if not (RUNTIME / 'compose.env').exists():
        unconfigured(f'controlled runtime {RUNTIME} has not been prepared')
    preflight(need_node=True)
    if not health():
        unconfigured(f'controlled runtime at {ORIGIN} is not running; run up first')
    prior = state().get('reports', {}).get('customer-service')
    if not prior or not (Path(prior) / 'browser-answer-capture.json').exists():
        unconfigured('customer-service report is missing; run customer-service before restore')
    if not (RUNTIME / 'keycloak-accounts.json').exists():
        prepare_runtime_files()
    run = run_id()
    phase = REPORT_ROOT / 'runs' / run
    phase.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(phase, 0o700)
    backup = phase / 'backup'
    restored = RESTORE_ROOT / ('restored-' + run.lower())
    if restored.resolve().is_relative_to(ROOT.resolve()):
        raise RuntimeError('CUSTOMER_RESTORE_ROOT must be outside the repository')
    project = 'ecr-restore-wp91-' + run.lower()
    if not re.fullmatch(r'ecr-restore-[a-z0-9-]+', project):
        raise RuntimeError('invalid restore project name')
    restored_env = restored / 'compose.env'
    spec = None
    try:
        stop_writers(RUNTIME / 'compose.env')
        info('source writers stopped; creating the sealed backup')
        completed = command([str(PYTHON), str(ROOT / 'scripts/ops/selfhost-backup.py'),
                             '--runtime', str(RUNTIME), '--output', str(backup), '--maintenance-stopped'],
                            check=False, capture=True)
        if completed.returncode != 0:
            info(completed.stdout.strip() or completed.stderr.strip())
            raise RuntimeError('selfhost backup failed; inspect the protected output')
        info(completed.stdout.strip())
        completed = command([str(PYTHON), str(ROOT / 'scripts/ops/selfhost-restore.py'),
                             '--backup', str(backup), '--directory', str(restored), '--project', project],
                            check=False, capture=True)
        if completed.returncode != 0:
            info(completed.stdout.strip() or completed.stderr.strip())
            raise RuntimeError('selfhost restore failed; inspect the protected backup')
        info(completed.stdout.strip())
        value = state()
        value.setdefault('restored', []).append({'project': project, 'runtime': str(restored)})
        save_state(value)
        # One-shot db-init/migrate/bootstrap already ran before the backup; the
        # restored project only needs the long-running services.
        start_stack(restored_env, no_deps=True, build=False)
        if not wait_health():
            raise RuntimeError(f'restored gateway did not become healthy at {ORIGIN}')
        out = report_path('selfhost-restore', run)
        env = spec_environment('customer-service', out)
        env.update({'CUSTOMER_RUNTIME_DIR': str(restored), 'CUSTOMER_PRIOR_BROWSER_DIR': prior,
                    'CUSTOMER_REPORT_DIR': str(out)})
        spec = command(['node', 'tests/selfhost-restore.browser.mjs'], cwd=FRONTEND, env=env, check=False)
        remember_report('selfhost-restore', out)
    finally:
        # Free the shared origin and keep the restored volumes for inspection.
        # The oidc profile is required: without it Compose leaves Keycloak (and
        # the network it holds) behind after every restore run.
        if restored_env.exists():
            compose(restored_env, ['--profile', 'oidc', 'down'], check=False, timeout=120)
        try:
            start_stack(RUNTIME / 'compose.env', build=False)
        except Exception:
            info('WARN source stack did not restart after restore verification')
    if spec.returncode == 0:
        info('PASS controlled spec selfhost-restore')
    else:
        info('FAIL controlled spec selfhost-restore; inspect the protected report directory')
    return spec.returncode


def docker_ids(args):
    result = command(['docker', *args], check=False, capture=True, timeout=60)
    if result.returncode != 0:
        return []
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def compose_projects():
    """Compose project labels that still have containers or networks."""
    projects = set(docker_ids(['ps', '-a', '--format', '{{.Label "com.docker.compose.project"}}']))
    projects |= set(docker_ids(['network', 'ls', '--format', '{{.Label "com.docker.compose.project"}}']))
    projects.discard('')
    return projects


def sweep_harness_projects():
    """Remove residual containers/networks of harness-owned projects only.

    The allowlist is exactly ecr-wp91 and ecr-restore-wp91-*; every other
    project (notably ecr-merchant) is left running. The sweep is best effort and
    only runs after the Compose teardown reported success.
    """
    projects = compose_projects()
    if 'ecr-merchant' in projects:
        info('WARN Compose project ecr-merchant is not harness-owned; left untouched')
    removed = []
    for project in sorted(projects):
        if not HARNESS_PROJECT.fullmatch(project):
            continue
        containers = docker_ids(['ps', '-aq', '--filter', f'label=com.docker.compose.project={project}'])
        if containers:
            command(['docker', 'rm', '-f', *containers], check=False, capture=True, timeout=120)
        networks = docker_ids(['network', 'ls', '-q', '--filter', f'label=com.docker.compose.project={project}'])
        for attempt in range(3):
            if not networks:
                break
            result = command(['docker', 'network', 'rm', *networks], check=False, capture=True, timeout=120)
            if result.returncode == 0:
                break
            time.sleep(1)
        removed.append(project)
    return removed


def down(args):
    failed = []
    if (RUNTIME / 'compose.env').exists():
        result = compose(RUNTIME / 'compose.env', ['--profile', 'oidc', 'down', '--remove-orphans',
                                                   *(['--volumes'] if args.clean else [])],
                         check=False, timeout=180)
        if result.returncode != 0:
            failed.append(str(RUNTIME))
    for entry in state().get('restored', []):
        env_file = Path(entry['runtime']) / 'compose.env'
        if not env_file.exists():
            continue
        result = command(['docker', 'compose', '--env-file', str(env_file), '-f', str(COMPOSE),
                          '--profile', 'oidc', 'down', '--remove-orphans',
                          *(['--volumes'] if args.clean else [])],
                         check=False, capture=True, timeout=180)
        if result.returncode != 0:
            failed.append(entry['runtime'])
    if failed:
        # Keep the runtime directories: they describe what the next `down` must
        # still tear down, and deleting them would hide the leak.
        info('FAIL controlled runtime teardown failed for ' + ', '.join(failed)
             + '; runtime directories retained')
        return 1
    residual = sweep_harness_projects()
    if residual:
        info('PASS removed residual harness Compose projects: ' + ', '.join(residual))
    if args.clean:
        for entry in state().get('restored', []):
            shutil.rmtree(entry['runtime'], ignore_errors=True)
        shutil.rmtree(RUNTIME, ignore_errors=True)
        STATE_FILE.unlink(missing_ok=True)
        info('PASS controlled runtime volumes and runtime directory removed')
    else:
        info('PASS controlled runtime stopped; volumes retained (use down --clean to remove them)')
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    up_parser = sub.add_parser('up')
    up_parser.add_argument('--fresh', action='store_true', help='remove the previous project first')
    up_parser.set_defaults(handler=up)
    status_parser = sub.add_parser('status')
    status_parser.set_defaults(handler=status)
    run_parser = sub.add_parser('run')
    run_parser.set_defaults(handler=run)
    spec_parser = sub.add_parser('run-spec')
    spec_parser.add_argument('name', choices=[*CONTROLLED, 'login'])
    spec_parser.set_defaults(handler=lambda a: run_spec(a.name))
    restore_parser = sub.add_parser('restore')
    restore_parser.add_argument('--keep', action='store_true')
    restore_parser.set_defaults(handler=restore)
    down_parser = sub.add_parser('down')
    down_parser.add_argument('--clean', action='store_true')
    down_parser.set_defaults(handler=down)
    args = parser.parse_args()
    try:
        return args.handler(args)
    except Unconfigured as error:
        info(f'UNCONFIGURED {error}')
        return 2
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        info(f'FAIL {error}')
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
