"""Local synthetic Keycloak protocol acceptance; no production identity claim.

Uses protected accounts and the Go API. Optional authority fixture updates only
the existing synthetic identity, never business results, and restores its state.
Raw HTTP results stay in restricted capture storage; tokens are never captured.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
from quality_capture import capture, secure_directory
from run_v2_quality_e2e import write_report
from v2_services import load_values


def protected_json(path):
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.geteuid() or info.st_size > 65536):
        raise ValueError('protected_input_invalid')
    return json.loads(path.read_text())


class HTTP:
    def __init__(self, base):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or parsed.query or parsed.fragment:
            raise ValueError('local_endpoint_required')
        self.base = base
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, method, path, data=None, token=None, form=False):
        headers = {}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if data is None:
            body = None
        elif form:
            body = urllib.parse.urlencode(data).encode()
            headers['Content-Type'] = 'application/x-www-form-urlencoded'
        else:
            body = json.dumps(data).encode()
            headers['Content-Type'] = 'application/json'
            headers['Idempotency-Key'] = uuid4().hex
        request = urllib.request.Request(self.base + path, data=body, method=method, headers=headers)
        try:
            response = self.opener.open(request, timeout=8)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read(1024 * 1024)
            return response.code, json.loads(raw) if raw else {}


def run(args):
    runtime = load_values(args.runtime_env)
    if runtime.get('V2_IDENTITY_PROFILE') != 'oidc':
        raise ValueError('oidc_runtime_required')
    accounts = protected_json(args.accounts_file)
    oidc = protected_json(args.oidc_config)
    issuer = urllib.parse.urlsplit(oidc['issuer'])
    keycloak = HTTP(urllib.parse.urlunsplit((issuer.scheme, issuer.netloc, '', '', '')))
    gateway = HTTP(args.base_url)
    realm = '/realms/' + accounts['realm']
    admin_prefix = '/admin/realms/' + accounts['realm']
    captures = secure_directory(args.capture_dir)
    rows = args.observations

    def record(name, observed, expected, raw=None):
        row = {'check_id': name, 'passed': observed == expected,
               'observed': observed, 'expected': expected}
        if raw is not None:
            row.update(capture(captures, raw))
        rows.append(row)
        print(json.dumps({'check_id': name, 'passed': row['passed']}), flush=True)

    def token():
        code, result = keycloak.call('POST', realm + '/protocol/openid-connect/token', {
            'grant_type': 'password', 'client_id': accounts['client_id'],
            'client_secret': accounts['client_secret'], 'username': accounts['username'],
            'password': accounts['password']}, form=True)
        if code != 200:
            raise ValueError('test_login_failed')
        return result['access_token']

    code, result = keycloak.call('POST', '/realms/master/protocol/openid-connect/token', {
        'grant_type': 'password', 'client_id': 'admin-cli', 'username': accounts['admin_username'],
        'password': accounts['admin_password']}, form=True)
    if code != 200:
        raise ValueError('test_admin_login_failed')
    admin = result['access_token']
    user_path = admin_prefix + '/users/' + accounts['subject']
    code, profile = keycloak.call('GET', admin_prefix + '/users/profile', token=admin)
    record('identity_attributes_admin_edit_only', (code, profile.get('unmanagedAttributePolicy')), (200, 'ADMIN_EDIT'))
    code, original = keycloak.call('GET', user_path, token=admin)
    if code != 200 or 'attributes' not in original:
        raise ValueError('test_identity_not_found')

    def attributes(values):
        user = copy.deepcopy(original)
        user['attributes'].update(values)
        code, _ = keycloak.call('PUT', user_path, user, token=admin)
        if code != 204:
            raise ValueError('test_identity_update_failed')

    database = None
    snapshot = None
    request_id = None
    try:
        bearer = token()
        code, session = gateway.call('GET', '/v1/session', token=bearer)
        record('valid_signature_discovery_jwks_and_session', code, 200, session)
        record('missing_token_does_not_fallback', gateway.call('GET', '/v1/session')[0], 401)
        parts = bearer.split('.')
        parts[1] = ('A' if parts[1][0] != 'A' else 'B') + parts[1][1:]
        record('modified_signed_token_denied', gateway.call('GET', '/v1/session', token='.'.join(parts))[0], 401)
        for name, value, expected in [
            ('role', ['owner'], 403), ('permission_revision', ['stale-test-revision'], 403),
            ('tenant_id', ['other-synthetic-tenant'], 401), ('shop_ids', ['other-test-shop'], 401),
            ('revoked', ['true'], 403),
        ]:
            attributes({name: value})
            record('claim_' + name + '_does_not_grant_permission', gateway.call('GET', '/v1/session', token=token())[0], expected)
        attributes({})
        bearer = token()
        code, conversation = gateway.call('POST', '/v1/conversations', {'title': 'local OIDC acceptance'}, token=bearer)
        record('conversation_owned_by_verified_identity', code, 201)
        code, turn = gateway.call('POST', '/v1/conversations/' + conversation['id'] + '/turns',
                                  {'query': '请介绍棉麻夏凉被的温度建议和限制。'}, token=bearer)
        record('go_turn_submit', code, 202)
        request_id = turn['request_id']
        deadline = time.monotonic() + 65
        while time.monotonic() < deadline:
            code, result = gateway.call('GET', '/v1/turns/' + request_id, token=bearer)
            if code != 200 or result['status'] in {'DONE', 'ASKING', 'FAILED', 'CANCELLED'}:
                break
            time.sleep(.2)
        record('go_grpc_python_signed_scope_terminal', result.get('status'), 'DONE', result)
        record('copy_eligibility_closed', result.get('customer_reply', {}).get('can_copy'), False)
        code, _ = keycloak.call('POST', user_path + '/logout', token=admin)
        record('local_logout_accepted', code, 204)
        record('old_signed_token_after_logout_denied', gateway.call('GET', '/v1/session', token=bearer)[0], 403)
        record('old_result_token_after_logout_denied', gateway.call('GET', '/v1/turns/' + request_id, token=bearer)[0], 403)

        code, realm_config = keycloak.call('GET', admin_prefix, token=admin)
        if code != 200:
            raise ValueError('test_realm_not_found')
        try:
            short = copy.deepcopy(realm_config)
            short['accessTokenLifespan'] = 2
            code, _ = keycloak.call('PUT', admin_prefix, short, token=admin)
            if code != 204:
                raise ValueError('test_expiry_setup_failed')
            expired = token()
            time.sleep(3)
            record('real_time_expired_token_denied', gateway.call('GET', '/v1/session', token=expired)[0], 401)
        finally:
            keycloak.call('PUT', admin_prefix, realm_config, token=admin)

        if args.authority_env:
            import psycopg
            authority = load_values(args.authority_env)
            database = psycopg.connect(authority['V2_MIGRATION_DSN'])
            db_name, superuser, bypass = database.execute(
                'SELECT current_database(),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user').fetchone()
            if 'v2' not in db_name or superuser or bypass or runtime.get('V2_IDENTITY_PROFILE') != 'oidc':
                raise ValueError('isolated_authority_fixture_required')
            binding = oidc['bindings'][0]
            snapshot = database.execute(
                'SELECT u.id,u.role,u.shop_ids,u.permission_revision,u.revoked_at '
                'FROM app_user u JOIN tenant t ON t.id=u.tenant_id WHERE t.external_id=%s AND u.external_id=%s',
                (binding['tenant'], binding['user'])).fetchone()
            if not snapshot or binding['user'] != 'dev-user' or binding['tenant'] != 'v2-demo-tenant':
                raise ValueError('only_existing_synthetic_identity_may_change')
            bearer = token()
            for name, sql, value in [
                ('shop_grant_removed', 'shop_ids=%s', []),
                ('database_revoked', 'revoked_at=%s', 'now'),
                ('permission_revision_changed', 'permission_revision=%s', 'oidc-test-revision'),
            ]:
                database.execute('UPDATE app_user SET ' + sql + ' WHERE id=%s', (value, snapshot[0]))
                database.commit()
                record(name + '_old_token_denied', gateway.call('GET', '/v1/session', token=bearer)[0], 403)
                if name == 'permission_revision_changed':
                    attributes({'permission_revision': ['oidc-test-revision']})
                    fresh = token()
                    record('fresh_revision_identity_allowed', gateway.call('GET', '/v1/session', token=fresh)[0], 200)
                    record('stale_result_not_replayed_with_fresh_identity', gateway.call('GET', '/v1/turns/' + request_id, token=fresh)[0], 403)
                restore(database, snapshot)
                attributes({})
                bearer = token()
    finally:
        if database is not None:
            if snapshot is not None:
                restore(database, snapshot)
            database.close()
        keycloak.call('PUT', user_path, original, token=admin)
    payload = {'report_version': 'local-keycloak-protocol-v1', 'status': 'PASS' if all(x['passed'] for x in rows) else 'NOT_PASSED',
               'results': rows, 'identity_kind': 'local_keycloak_synthetic_accounts',
               'real_service_acceptance': False, 'model_quality_claim': False,
               'model_profile': runtime['RAG_PROFILE'],
               'authority_fixture_executed': args.authority_env is not None,
               'oidc_config_sha256': hashlib.sha256(args.oidc_config.read_bytes()).hexdigest(),
               'implementation_sha256': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
                                          for p in [ROOT / 'go/internal/identity/oidc.go', ROOT / 'go/internal/gateway/identity.go']}}
    write_report(args.report, payload)
    return 0 if payload['status'] == 'PASS' else 1


def restore(database, snapshot):
    database.execute('UPDATE app_user SET role=%s,shop_ids=%s,revoked_at=%s WHERE id=%s',
                     (snapshot[1], snapshot[2], snapshot[4], snapshot[0]))
    # The permission trigger legitimately changes the revision during fixture
    # rollback; restore the original revision in a separate identity-only update.
    database.execute('UPDATE app_user SET permission_revision=%s WHERE id=%s', (snapshot[3], snapshot[0]))
    database.commit()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--live', action='store_true')
    parser.add_argument('--base-url', default='http://127.0.0.1:18081')
    for name in ['accounts-file', 'oidc-config', 'runtime-env', 'authority-env', 'capture-dir', 'report']:
        parser.add_argument('--' + name, type=Path)
    args = parser.parse_args()
    if not args.live:
        print(json.dumps({'status': 'PLAN', 'online_requests_made': False, 'real_service_acceptance': False}))
        return 0
    if any(getattr(args, name) is None for name in ['accounts_file', 'oidc_config', 'runtime_env', 'capture_dir', 'report']) or args.report.exists():
        raise ValueError('outputs_or_protected_inputs_required')
    args.observations = []
    try:
        return run(args)
    except (OSError, ValueError, KeyError, TypeError):
        payload = {'report_version': 'local-keycloak-protocol-v1', 'status': 'NOT_PASSED',
                   'failure_code': 'local_oidc_input_or_transport_invalid',
                   'results': args.observations, 'fixture_restoration_confirmed': False,
                   'real_service_acceptance': False, 'model_quality_claim': False}
        if not args.report.exists():
            write_report(args.report, payload)
        print(json.dumps({'status': 'NOT_PASSED', 'failure_code': payload['failure_code']}))
        return 2


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError, TypeError):
        print(json.dumps({'status': 'NOT_PASSED', 'failure_code': 'local_oidc_input_or_transport_invalid'}))
        raise SystemExit(2) from None
