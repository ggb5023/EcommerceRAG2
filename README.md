# EcommerceRAG

E-commerce RAG development foundation using Go, Python, Vue, PostgreSQL and Redis.

This repository contains code, schemas, reusable deployment tools and three concise collaboration documents: `AGENTS.md`, `HANDOFF.md` and `FACTS.md`. Detailed engineering notes, per-window archives and environment configuration remain private under `.local/dev-docs/` or outside the repository.

Run `bash scripts/install-git-hooks.sh` after cloning, then `bash scripts/check.sh` in a configured development environment. The local hooks allow only the curated root documents and reject private archives, environment values and common credential patterns; each contributor must enable them. Commit one completed function or documentation unit at a time, staging only its files.

Deployment requires a protected `/etc/ecommerce-rag/infra.env`, based on `scripts/infra.env.example`. Missing or invalid settings stop deployment. Supply real credentials separately outside this repository.

For local V2 development, migrate an explicitly configured independent database with `python/.venv/bin/python scripts/v2-migrate.py --env /path/to/protected-migration.env`, then run `bash scripts/v2-local-services.sh --env /path/to/protected-runtime.env`. The migration file accepts only `V2_` keys; keep migration and runtime configuration separate. The owned regular runtime file must have mode `0600`; provide each service's restricted database connection, development tenant/user, and an explicit artifact-storage profile. The launcher uses separate environments for Gateway, AI RPC, ingestion RPC and worker, waits for their health/heartbeat checks, and stops the owned stack on a failed process or readiness timeout. Its default model profile is offline `v2_langchain_mock`; online integration requires explicit `v2_langchain_bailian`, a protected Provider configuration, and an authorized budget. It does not create databases or restart shared infrastructure.
