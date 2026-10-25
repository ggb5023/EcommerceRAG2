#!/usr/bin/env python3
"""Prepare a protected, independent single-merchant installation."""
from __future__ import annotations

import argparse
import json
import os
import secrets
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlsplit
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'python'))


def write(path, value, owner=65532):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600),'w') as f:
        f.write(value)
    if os.geteuid() == 0:
        os.chown(path, owner, owner)


def write_if_absent(path, value, owner=65532):
    """Setup metadata may be regenerated; secrets stay O_EXCL."""
    if path.exists():
        print(f'SKIP existing {path}')
        return
    write(path, value, owner)


def command(args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)


def initialize(args):
    from app.providers.budget import initialize_ledger
    from app.providers.config import ensure_secure_config_file, load_provider_config
    if os.geteuid()!=0:raise ValueError('root provisioning required')
    origin = urlsplit(args.origin)
    if (origin.scheme!='https' or not origin.hostname or origin.path or origin.query or origin.fragment
            or origin.username or origin.password or origin.hostname!=origin.hostname.lower()):
        raise ValueError('exact HTTPS origin required')
    port = origin.port or 443
    runtime=args.directory.resolve()
    if runtime.exists():
        raise ValueError('refusing existing installation directory')
    ensure_secure_config_file(args.provider_config)
    provider=load_provider_config(args.provider_config)
    if provider.profile!='aliyun-bailian' or provider.embedding_dimensions!=1024 or provider.quota_rpm!=60:
        raise ValueError('explicit real 1024-dimensional Provider with 60 RPM required')
    runtime.mkdir(mode=0o700,parents=True)
    for name,owner in [('gateway',65532),('ai',65532),('ingest',65532),('worker',65532),('artifacts',65532),('web',65532),('bootstrap',0),('identity',1000),('redis',999),('budget',65532)]:
        p=runtime/name;p.mkdir(mode=0o700)
        if os.geteuid()==0:os.chown(p,owner,owner)
    passwords={r:secrets.token_hex(32) for r in ['rag_migration','rag_v2_gateway','rag_v2_ai','rag_v2_ingest','keycloak']}
    write(runtime/'bootstrap/pg-password',secrets.token_hex(32),0)
    client=secrets.token_hex(32)
    # Local profile keeps Keycloak off: the bootstrap job creates a single owner
    # skeleton and /setup only completes its password.
    if args.identity_profile == 'oidc_browser':
        accounts=[{'username':args.owner_username,'role':'owner','subject':str(uuid4()),'password':secrets.token_urlsafe(24)}, {'username':'support','role':'operator','subject':str(uuid4()),'password':secrets.token_urlsafe(24)}]
        realm={'realm':'merchant','enabled':True,'registrationAllowed':False,'resetPasswordAllowed':False,'sslRequired':'external','accessTokenLifespan':300,
          'clients':[{'clientId':'merchant-browser','secret':client,'publicClient':False,'standardFlowEnabled':True,'directAccessGrantsEnabled':False,'implicitFlowEnabled':False,'serviceAccountsEnabled':False,'redirectUris':[args.origin+'/v1/auth/callback'],'webOrigins':[], 'attributes':{'pkce.code.challenge.method':'S256'},'protocolMappers':[{'name':'audience','protocol':'openid-connect','protocolMapper':'oidc-audience-mapper','config':{'included.client.audience':'merchant-browser','access.token.claim':'true','id.token.claim':'false'}}]}],
          'users':[{'id':a['subject'],'username':a['username'],'firstName':'Merchant','lastName':a['username'],'email':a['username']+'@example.invalid','emailVerified':True,'enabled':True,'credentials':[{'type':'password','value':a['password'],'temporary':False}]} for a in accounts]}
    else:
        accounts=[]
        realm={}
    write_if_absent(runtime/'bootstrap/config.json',json.dumps({'origin':args.origin,'shop_name':args.shop_name,'database_passwords':passwords,'accounts':accounts,'realm':realm,'owner_username':args.owner_username}),0)
    write_if_absent(runtime/'bootstrap/accounts.json',json.dumps({'accounts':accounts,'owner_username':args.owner_username}),0)
    if args.identity_profile == 'oidc_browser':
        write(runtime/'gateway/browser-secret',client)
    write(runtime/'gateway/scope-key',secrets.token_hex(32))
    # Private RPC PKI is separate from the browser HTTPS trust root.
    pki=runtime/'bootstrap'
    command(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(pki/'rpc-ca.key'),'-out',str(pki/'rpc-ca.crt'),'-days','365','-subj','/CN=EcommerceRAG internal root'])
    for name in ['gateway','ai','ingest']:
        dest=runtime/name
        command(['openssl','req','-new','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(dest/'rpc.key'),'-out',str(pki/(name+'.csr')),'-subj','/CN='+name])
        ext=pki/(name+'.ext');write(ext,f'subjectAltName=DNS:{name},DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth,clientAuth\n',0)
        command(['openssl','x509','-req','-in',str(pki/(name+'.csr')),'-CA',str(pki/'rpc-ca.crt'),'-CAkey',str(pki/'rpc-ca.key'),'-CAcreateserial','-days','90','-extfile',str(ext),'-out',str(dest/'rpc.crt')])
        write(dest/'rpc-ca.crt',(pki/'rpc-ca.crt').read_text())
        os.chmod(dest/'rpc.key',0o600)
        if os.geteuid()==0:os.chown(dest/'rpc.key',65532,65532)
    if args.tls_cert and args.tls_key and args.tls_ca:
        for name,source in [('tls.crt',args.tls_cert),('tls.key',args.tls_key),('web-ca.crt',args.tls_ca)]:write(runtime/'web'/name,source.read_text())
    elif args.local_certificate:
        command(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(runtime/'web/tls.key'),'-out',str(runtime/'web/tls.crt'),'-days','30','-subj','/CN='+origin.hostname,'-addext','subjectAltName=DNS:'+origin.hostname])
        os.chmod(runtime/'web/tls.key',0o600)
        write(runtime/'web/web-ca.crt',(runtime/'web/tls.crt').read_text())
        os.chown(runtime/'web/tls.key',65532,65532);os.chown(runtime/'web/tls.crt',65532,65532)
    else:raise ValueError('supply TLS certificate/key/CA, or explicitly use local test certificate')
    write(runtime/'gateway/web-ca.crt',(runtime/'web/web-ca.crt').read_text())
    if args.budget_dir:
        if not args.budget_id or not args.budget_file:raise ValueError('existing shared ledger identity required')
        budget_dir=args.budget_dir.resolve();budget_file=args.budget_file
        if Path(budget_file).name!=budget_file:raise ValueError('budget filename required')
        # Preserve the existing ledger and all attempts; never copy or reset it.
        from app.providers.budget import BudgetLedger
        BudgetLedger(budget_dir/budget_file,args.budget_id,args.budget_cny)
    else:
        budget_dir=runtime/'budget';budget_file='ledger.sqlite';args.budget_id='merchant-'+secrets.token_hex(12)
        # Runtime owner initializes its own explicitly authorized ledger.
        if os.geteuid()==0:os.chown(budget_dir,0,0)
        initialize_ledger(budget_dir/budget_file,args.budget_id,args.budget_cny)
        if os.geteuid()==0:os.chown(budget_dir/budget_file,65532,65532);os.chown(budget_dir,65532,65532)
    dsn=lambda role:f'host=postgres dbname=ecr_v2 user={role} password={passwords[role]}'
    common={'APP_ENV':'selfhost','RAG_PROFILE':'v2_langchain_bailian','V2_RPC_TLS':'mtls','V2_RPC_CA_FILE':'/run/runtime/rpc-ca.crt','V2_RPC_CERT_FILE':'/run/runtime/rpc.crt','V2_RPC_KEY_FILE':'/run/runtime/rpc.key','V2_AUTHORITY_GRPC_ADDR':'gateway:50054'}
    paid={'V2_PROVIDER_CONFIG':'/run/runtime/providers.env','V2_ONLINE_BUDGET_CNY':args.budget_cny,'V2_ONLINE_BUDGET_LEDGER':'/run/budget/'+budget_file,'V2_ONLINE_BUDGET_ID':args.budget_id}
    storage={'INGEST_STORAGE_PROFILE':'filesystem','INGEST_ARTIFACT_ROOT':'/var/lib/ecr/artifacts'}
    configs={'gateway':{**common,'HTTP_ADDR':'0.0.0.0:8080','V2_GRPC_ADDR':'ai:50052','V2_INGEST_GRPC_ADDR':'ingest:50053','V2_GATEWAY_DSN':dsn('rag_v2_gateway'),'SCOPE_SIGNING_KEY_FILE':'/run/runtime/scope-key','V2_IDENTITY_PROFILE':args.identity_profile,**({'V2_OIDC_CONFIG_FILE':'/run/runtime/oidc.json'} if args.identity_profile=='oidc_browser' else {}),'V2_CUSTOMER_ORIGIN':args.origin,'V2_CUSTOMER_PROXY_HOST':'edge','SSL_CERT_FILE':'/run/runtime/web-ca.crt'},
      'ai':{**common,**paid,**storage,'V2_GRPC_ADDR':'0.0.0.0:50052','V2_INGEST_GRPC_ADDR':'0.0.0.0:50053',
             'V2_RETRIEVER_DSN':dsn('rag_v2_ai'),'V2_CHECKPOINT_DSN':dsn('rag_v2_ai'),
             'V2_INGEST_DSN':dsn('rag_v2_ingest'),'V2_INGEST_WORKER_ID':'merchant-worker'}}
    for name,values in configs.items():write(runtime/(name+'.env'),'\n'.join(k+'='+v for k,v in values.items())+'\n',0)
    write(runtime/'migration.env','V2_MIGRATION_DSN='+dsn('rag_migration')+'\n',0)
    write(runtime/'ai'/'providers.env',args.provider_config.read_text())
    if args.identity_profile == 'oidc_browser':
        write(runtime/'identity.env','\n'.join(['KC_DB=postgres','KC_DB_URL=jdbc:postgresql://postgres:5432/keycloak','KC_DB_USERNAME=keycloak','KC_DB_PASSWORD='+passwords['keycloak'],'KC_HOSTNAME='+args.origin+'/identity','KC_BOOTSTRAP_ADMIN_USERNAME=bootstrap-admin','KC_BOOTSTRAP_ADMIN_PASSWORD='+secrets.token_urlsafe(32),'KC_TELEMETRY_DISABLED=true'])+'\n',0)
    write(runtime/'redis/redis.conf','bind 0.0.0.0\nprotected-mode yes\nappendonly yes\nrequirepass '+secrets.token_hex(32)+'\n',999)
    write(runtime/'compose.env','\n'.join(['ECR_RUNTIME='+str(runtime),'ECR_BUDGET_DIR='+str(budget_dir),'ECR_ORIGIN='+args.origin,'ECR_HOSTNAME='+origin.hostname,'ECR_HTTPS_PORT='+str(port),'ECR_PROJECT='+args.project,'ECR_WEB_BIND='+args.bind])+'\n',0)
    print('PASS protected installation prepared; run bootstrap then start Compose. Account details: bootstrap/accounts.json')


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=['init']);p.add_argument('--directory',type=Path,required=True);p.add_argument('--origin',required=True)
    p.add_argument('--provider-config',type=Path,required=True);p.add_argument('--budget-cny',required=True)
    p.add_argument('--budget-dir',type=Path);p.add_argument('--budget-file');p.add_argument('--budget-id')
    p.add_argument('--tls-cert',type=Path);p.add_argument('--tls-key',type=Path);p.add_argument('--tls-ca',type=Path);p.add_argument('--local-certificate',action='store_true')
    p.add_argument('--shop-name',default='我的店铺');p.add_argument('--project',default='ecr-merchant');p.add_argument('--bind',default='127.0.0.1')
    p.add_argument('--identity-profile',choices=['local','oidc_browser'],default='local');p.add_argument('--owner-username',default='admin')
    args=p.parse_args()
    try:initialize(args)
    except Exception:raise SystemExit('FAIL installation preparation; check HTTPS, protected Provider, budget, empty directory and openssl') from None
if __name__=='__main__':main()
