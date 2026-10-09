# EcommerceRAG

E-commerce RAG development foundation using Go, Python, Vue, PostgreSQL and Redis.

This repository contains code, schemas, reusable deployment tools and three concise collaboration documents: `AGENTS.md`, `HANDOFF.md` and `FACTS.md`. Detailed engineering notes, per-window archives and environment configuration remain private under `.local/dev-docs/` or outside the repository.

Run `bash scripts/install-git-hooks.sh` after cloning, then `bash scripts/check.sh` in a configured development environment. The local hooks allow only the curated root documents and reject private archives, environment values and common credential patterns; each contributor must enable them. Commit one completed function or documentation unit at a time, staging only its files.

Deployment requires a protected `/etc/ecommerce-rag/infra.env`, based on `scripts/infra.env.example`. Missing or invalid settings stop deployment. Supply real credentials separately outside this repository.
