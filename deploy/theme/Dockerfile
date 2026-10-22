# syntax=docker/dockerfile:1
FROM node:24-bookworm-slim AS builder
RUN npm install --global pnpm@11.22.0
ENV ASTRO_TELEMETRY_DISABLED=1 PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
WORKDIR /bundle/source
COPY firefly/package.json firefly/pnpm-lock.yaml firefly/pnpm-workspace.yaml firefly/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY firefly/ ./
COPY internal/server/admin_schema.json ./aigoni-settings-schema.json
COPY docs/firefly-upstream.json /bundle/firefly-upstream.json
COPY deploy/theme/package.mjs /bundle/package.mjs
RUN pnpm exec astro check && pnpm run test:aigoni:build
ARG THEME_VERSION
ARG SOURCE_COMMIT
RUN THEME_VERSION="$THEME_VERSION" SOURCE_COMMIT="$SOURCE_COMMIT" node /bundle/package.mjs
FROM scratch AS artifact
COPY --from=builder /out/ /
