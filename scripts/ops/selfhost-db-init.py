"""One-shot superuser bootstrap: roles, databases and extensions only."""
from __future__ import annotations

import json
import os
from pathlib import Path

import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo

ROOT = Path(os.environ.get('ECR_BOOTSTRAP_ROOT', '/run/bootstrap'))
PG_HOST = os.environ.get('ECR_POSTGRES_HOST', 'postgres')
PG_PORT = os.environ.get('ECR_POSTGRES_PORT', '5432')


def main():
    config = json.loads((ROOT / 'config.json').read_text())
    admin = make_conninfo(host=PG_HOST, port=PG_PORT, dbname='postgres', user='postgres',
                          password=(ROOT / 'pg-password').read_text())
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
    print('PASS database roles, databases and extensions')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('FAIL database initialization; check protected configuration and postgres availability') from None
