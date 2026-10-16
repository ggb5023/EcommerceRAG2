"""Explicit database/identity bootstrap job. Applications never execute DDL."""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path

import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo

ROOT = Path(os.environ.get('ECR_BOOTSTRAP_ROOT','/run/bootstrap'))
PG_HOST=os.environ.get('ECR_POSTGRES_HOST','postgres')
PG_PORT=os.environ.get('ECR_POSTGRES_PORT','5432')


def main():
    config = json.loads((ROOT / 'config.json').read_text())
    admin = make_conninfo(host=PG_HOST, port=PG_PORT, dbname='postgres', user='postgres', password=(ROOT / 'pg-password').read_text())
    with psycopg.connect(admin, autocommit=True) as conn:
        for role, password in config['database_passwords'].items():
            if not conn.execute('SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=%s)', (role,)).fetchone()[0]:
                conn.execute(sql.SQL('CREATE ROLE {} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD {}').format(sql.Identifier(role), sql.Literal(password)))
        if not conn.execute("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='rag_app')").fetchone()[0]:
            conn.execute('CREATE ROLE rag_app NOLOGIN NOSUPERUSER NOBYPASSRLS')
        for database, owner in (('ecr_v2', 'rag_migration'), ('keycloak', 'keycloak')):
            if not conn.execute('SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=%s)', (database,)).fetchone()[0]:
                conn.execute(sql.SQL('CREATE DATABASE {} OWNER {}').format(sql.Identifier(database), sql.Identifier(owner)))
                conn.execute(sql.SQL('REVOKE CONNECT ON DATABASE {} FROM PUBLIC').format(sql.Identifier(database)))
        for role in ('rag_v2_gateway', 'rag_v2_ai', 'rag_v2_ingest'):
            conn.execute(sql.SQL('GRANT CONNECT ON DATABASE ecr_v2 TO {}').format(sql.Identifier(role)))
    with psycopg.connect(make_conninfo(admin, dbname='ecr_v2'), autocommit=True) as conn:
        conn.execute('CREATE EXTENSION IF NOT EXISTS vector')
        conn.execute('CREATE EXTENSION IF NOT EXISTS pg_trgm')
        if conn.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone()[0] != '0.8.6':
            raise ValueError('expected pgvector 0.8.6')
    os.environ['V2_MIGRATION_DSN'] = make_conninfo(host=PG_HOST, port=PG_PORT, dbname='ecr_v2', user='rag_migration', password=config['database_passwords']['rag_migration'])
    spec = importlib.util.spec_from_file_location('migrate', os.environ.get('ECR_MIGRATION_SCRIPT','/app/scripts/v2-migrate.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.migrate()
    with psycopg.connect(os.environ['V2_MIGRATION_DSN']) as conn:
        # Initial merchant identities are operator provisioning, never final AI
        # results or ingestion status. Reruns preserve existing authorization.
        row = conn.execute("SELECT id FROM tenant WHERE external_id='merchant'").fetchone()
        if row:
            tenant = row[0]
        else:
            tenant = conn.execute("INSERT INTO tenant(name,external_id) VALUES(%s,'merchant') RETURNING id", (config['shop_name'],)).fetchone()[0]
            conn.execute("INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled,welcome_message) VALUES(%s,'main-shop',%s,'my-shop',true,%s)", (tenant, config['shop_name'], '欢迎咨询店铺知识，无法解决的问题可联系人工客服。'))
        for account in config['accounts']:
            row = conn.execute('SELECT id FROM app_user WHERE tenant_id=%s AND external_id=%s', (tenant, account['username'])).fetchone()
            if not row:
                user = conn.execute("INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,%s,%s,ARRAY['main-shop']) RETURNING id", (tenant, account['username'], account['role'])).fetchone()[0]
                if account['role'] == 'owner':
                    for role in ('merchant_admin','knowledge_reviewer','access_admin'):
                        conn.execute("INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by) VALUES(%s,%s,%s,'shops',ARRAY['main-shop'],'active',%s)", (tenant,user,role,user))
        # Historical fixture identities remain disabled in installed systems.
        conn.execute("UPDATE tenant SET status='disabled' WHERE external_id IS DISTINCT FROM 'merchant' AND status='active'")
    realm = config['realm']
    path = Path(os.environ.get('ECR_IDENTITY_DIR','/run/identity'))/'merchant-realm.json'
    if not path.exists():
        path.write_text(json.dumps(realm))
        os.chmod(path, 0o600)
        os.chown(path, 1000, 1000)
    oidc = {'issuer':config['origin']+'/identity/realms/merchant', 'client_id':'merchant-browser', 'secret_file':'/run/runtime/browser-secret',
            'authorization_source':'database',
            'bindings':[{'subject':a['subject'], 'tenant':'merchant', 'user':a['username'], 'shop':'main-shop'} for a in config['accounts']],
            'browser':{'customer_origin':config['origin'],'admin_origin':config['origin'],'callback_url':config['origin']+'/v1/auth/callback'}}
    path = Path(os.environ.get('ECR_GATEWAY_DIR','/run/gateway'))/'oidc.json'
    path.write_text(json.dumps(oidc))
    os.chmod(path, 0o600)
    os.chown(path, 65532, 65532)
    print('PASS independent database migrations and merchant identity provisioning')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('FAIL bootstrap; check protected configuration and database availability') from None
