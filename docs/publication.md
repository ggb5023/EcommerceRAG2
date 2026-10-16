# GitHub publication and private OSS archive

[Project README](../README.md)

The GitHub publication keeps the complete ancestry of the selected release
branch. It does not include other worktrees, raw `.git/`, runtime secrets or
private detailed documents. Public installation, knowledge, support, backup and
development guides remain in both GitHub and the private archive.

Private archive bucket: `ecommercerag-dev` (Beijing). Each batch uses a unique
`ecommercerag2/publication/20261015/<batch-id>/` prefix. Objects remain private;
uploads forbid overwriting. The bucket's versioning and policy are unchanged.

The public [archive index](../project-records/archive.json) binds the private
manifest digest. `private-manifest.json` retains each source alias, relative
path, size, source/export hash, destination, license status and redaction
method. `directory-index.json` maps original directories to content-addressed
objects; duplicate contents share storage without losing source mappings.
Generated materials have explicit provenance, with no fabricated source hash.
Original files remain on the local machine.

API keys, passwords/hashes, tokens, cookies, CSRF values, private keys, client
secrets and signed download URLs never belong in either destination. Only safe
copies are exported. Unknown binary contents and unverified third-party rights
remain **PENDING**; absence of a matching string is insufficient for approval.
Any pending nonsecret files leave the overall archive **INCOMPLETE**.

The database export is a single consistent read-only snapshot, with schema,
rows and per-table counts. Customer credentials are replaced and all sessions
expired/revoked only in the export. The export is for isolated recovery and
inspection, not reuse of original login sessions. Identity backups and opaque
serialized state require separate review; raw backups are not automatically
uploaded.

Use an isolated tools environment, following `python/uv.lock`:

```bash
uv export --frozen --project python --only-group cloud --no-emit-project --output-file /tmp/oss-requirements.txt
uv venv /path/outside/repository/publication-tools
uv pip sync --python /path/outside/repository/publication-tools/bin/python --require-hashes /tmp/oss-requirements.txt
```

Archive tooling accepts an output directory outside all input roots. Supply
credentials only through a protected mode-0600 configuration file:

```bash
python scripts/ops/publication_archive.py inventory --root v2=/path/to/source --root runtime=/path/to/runtime --output /path/to/protected/export
/path/to/publication-tools/bin/python scripts/ops/publication_archive.py upload --output /path/to/protected/export --prefix ecommercerag2/publication/20261015/BATCH_ID/ --config /path/to/protected/oss.env
```

A failed or interrupted upload can be rerun with the same output and prefix.
Existing objects are downloaded and hashed; differing content is a conflict.
Every successful object is downloaded in full and SHA-256 verified. The final
receipt binds the Git commit, manifest digest and verification totals. It does
not turn an incomplete archive into a complete one.

For private retrieval, use authenticated OSS SDK access without generating a
public or signed URL:

```bash
/path/to/publication-tools/bin/python scripts/ops/publication_download.py --config /path/to/protected/oss.env --prefix ecommercerag2/publication/20261015/BATCH_ID/ --destination /path/to/empty/recovery
```

The downloader verifies the private manifest digest from the public index,
checks each downloaded object hash and reconstructs only safe relative paths.
It never installs credentials or restores database/login state automatically.

Publication does not rerun paid models or change quality conclusions. The
original answer-point gate remains NOT_PASSED; current semantic failures remain
recorded and `real_service_acceptance=false`.
