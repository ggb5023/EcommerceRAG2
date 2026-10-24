FROM node:24.21.0-alpine AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --include=dev --no-audit --no-fund
COPY frontend ./
COPY scripts/ops/frontend-notices.mjs /tmp/frontend-notices.mjs
RUN npm run build && node /tmp/frontend-notices.mjs
FROM golang:1.27.1-bookworm AS proxy
WORKDIR /src
COPY go/go.mod go/go.sum ./
COPY go/cmd/web ./cmd/web
RUN CGO_ENABLED=0 go build -o /web ./cmd/web
FROM scratch
COPY --from=proxy /web /web
COPY --from=proxy /usr/local/go/LICENSE /licenses/Go-LICENSE.txt
COPY --from=frontend /frontend/dist /srv/frontend
USER 65532:65532
ENTRYPOINT ["/web"]
