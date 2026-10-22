ARG GO_VERSION=1.26
ARG NODE_VERSION=24
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go config.yaml.example .env.example ./
COPY cmd ./cmd
COPY internal ./internal
COPY frontend ./frontend
COPY docs/openapi.yaml ./docs/openapi.yaml
RUN CGO_ENABLED=0 go test ./...
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/aigoni ./cmd/Aigoni

FROM node:${NODE_VERSION}-bookworm-slim AS runtime
ARG PNPM_VERSION=11.22.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global pnpm@${PNPM_VERSION}
ENV HOME=/home/node \
    ASTRO_TELEMETRY_DISABLED=1 \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
RUN mkdir -p /data && chown node:node /data
COPY --from=backend /out/aigoni /usr/local/bin/aigoni
COPY deploy/docker/healthcheck.cjs /usr/local/lib/aigoni/healthcheck.cjs
ENV PORT=8080 \
    FRONTEND_MODE=firefly \
    FIREFLY_SOURCE_DIR=/opt/firefly \
    FIREFLY_BUILD_DIR=/data/.firefly \
    FIREFLY_NODE=/usr/local/bin/node \
    FIREFLY_REBUILD_ON_START=false
WORKDIR /data
USER node
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD ["node", "/usr/local/lib/aigoni/healthcheck.cjs"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/aigoni"]
