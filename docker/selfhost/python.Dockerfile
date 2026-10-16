FROM python:3.12.14-slim
WORKDIR /app
RUN pip install --no-cache-dir uv==0.12.16
COPY python/pyproject.toml python/uv.lock ./
RUN uv sync --frozen --no-dev
COPY python/app ./app
COPY python/rag ./rag
COPY python/fixtures ./fixtures
COPY sql ./sql
COPY scripts/ops/v2-migrate.py scripts/ops/selfhost-bootstrap.py scripts/ops/selfhost-health.py ./scripts/
USER 65532:65532
ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 V2_MIGRATION_DIR=/app/sql/migrations SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/app/.venv/bin/python"]
CMD ["-m", "app.rpc.server"]
