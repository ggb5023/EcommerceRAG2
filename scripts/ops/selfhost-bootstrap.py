"""Explicit database/identity bootstrap job. Applications never execute DDL."""
from __future__ import annotations

import json
import os
from pathlib import Path

import psycopg
from psycopg.conninfo import make_conninfo

ROOT = Path(os.environ.get('ECR_BOOTSTRAP_ROOT','/run/bootstrap'))
PG_HOST=os.environ.get('ECR_POSTGRES_HOST','postgres')
PG_PORT=os.environ.get('ECR_POSTGRES_PORT','5432')


def main():
    config = json.loads((ROOT / 'config.json').read_text())
    dsn = make_conninfo(host=PG_HOST, port=PG_PORT, dbname='ecr_v2', user='rag_migration',
                        password=config['database_passwords']['rag_migration'])
    with psycopg.connect(dsn) as conn:
        if conn.execute("SELECT to_regclass('public.schema_migration') IS NOT NULL").fetchone()[0] is not True:
            raise ValueError('schema is not migrated; run the migrate one-shot first')
        # Initial merchant identities are operator provisioning, never final AI
        # results or ingestion status. Reruns preserve existing authorization.
        row = conn.execute("SELECT id FROM tenant WHERE external_id='merchant'").fetchone()
        if row:
            tenant = row[0]
        else:
            tenant = conn.execute("INSERT INTO tenant(name,external_id) VALUES(%s,'merchant') RETURNING id", (config['shop_name'],)).fetchone()[0]
            conn.execute("INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled,welcome_message) VALUES(%s,'main-shop',%s,'my-shop',true,%s)", (tenant, config['shop_name'], '欢迎咨询店铺知识，无法解决的问题可联系人工客服。'))
        if config['accounts']:
            for account in config['accounts']:
                row = conn.execute('SELECT id FROM app_user WHERE tenant_id=%s AND external_id=%s', (tenant, account['username'])).fetchone()
                if not row:
                    user = conn.execute("INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,%s,%s,ARRAY['main-shop']) RETURNING id", (tenant, account['username'], account['role'])).fetchone()[0]
                    if account['role'] == 'owner':
                        for role in ('merchant_admin','knowledge_reviewer','access_admin'):
                            conn.execute("INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by) VALUES(%s,%s,%s,'shops',ARRAY['main-shop'],'active',%s)", (tenant,user,role,user))
        else:
            # Local profile: create the owner skeleton without a credential, so
            # /setup only has to set the password. Grants stay with the migrator;
            # the gateway role cannot write admin_role_grant.
            owner_username = config.get('owner_username', 'admin')
            row = conn.execute('SELECT id FROM app_user WHERE tenant_id=%s AND external_id=%s', (tenant, owner_username)).fetchone()
            if row:
                user = row[0]
            else:
                user = conn.execute("INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,%s,'owner',ARRAY['main-shop']) RETURNING id", (tenant, owner_username)).fetchone()[0]
            for role in ('merchant_admin','knowledge_reviewer','access_admin'):
                conn.execute("INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,status,granted_by) VALUES(%s,%s,%s,'shops',ARRAY['main-shop'],'active',%s) ON CONFLICT (tenant_id,user_id,role) DO NOTHING", (tenant,user,role,user))
            if not conn.execute("SELECT 1 FROM admin_audit WHERE tenant_id=%s AND action='setup.owner_skeleton' AND resource_id=%s", (tenant, str(user))).fetchone():
                conn.execute("INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json) VALUES(%s,%s,'setup.owner_skeleton','app_user',%s,'bootstrap created the local owner skeleton','success','{}'::jsonb)", (tenant, user, str(user)))
        # Historical fixture identities remain disabled in installed systems.
        conn.execute("UPDATE tenant SET status='disabled' WHERE external_id IS DISTINCT FROM 'merchant' AND status='active'")
    if config['accounts']:
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
