# syntax=docker/dockerfile:1
# Build context: repository root, e.g.
#   docker build -f api/Dockerfile -t compose-deploy .

# ---------------------------------------------------------------- UI build
FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

# ---------------------------------------------------------------- API build
FROM golang:1.25-alpine AS api
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
# Replace the placeholder UI with the real build (go:embed all:dist).
RUN rm -rf web/dist
COPY --from=ui /src/ui/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/compose-deploy .

# ---------------------------------------------------------------- runtime
FROM alpine:3.22
# git: repository sync; docker-cli + docker-cli-compose: `docker compose`
# against the host daemon via the mounted /var/run/docker.sock.
RUN apk add --no-cache ca-certificates git docker-cli docker-cli-compose tzdata

COPY --from=api /out/compose-deploy /usr/local/bin/compose-deploy

# Runs as root on purpose: it must talk to the host's /var/run/docker.sock
# (root-owned, GID differs per host) and manage files under DATA_DIR that
# deployed containers may bind-mount. Access to the docker socket is
# root-equivalent on the host anyway, so a non-root user would add little.
ENV DATA_DIR=/opt/compose-deploy \
    LISTEN_ADDR=:3000
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health || exit 1

ENTRYPOINT ["/usr/local/bin/compose-deploy"]
