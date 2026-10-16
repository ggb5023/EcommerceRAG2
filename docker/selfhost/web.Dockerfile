FROM node:24.21.0-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web ./
COPY scripts/ops/frontend-notices.mjs /tmp/frontend-notices.mjs
RUN npm run build && node /tmp/frontend-notices.mjs
FROM node:24.21.0-alpine AS admin
WORKDIR /admin
COPY admin-web/package.json admin-web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY admin-web ./
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
COPY --from=web /web/dist /srv/web
COPY --from=admin /admin/dist /srv/admin
USER 65532:65532
ENTRYPOINT ["/web"]
