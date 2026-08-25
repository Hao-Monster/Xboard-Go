# syntax=docker/dockerfile:1.7

ARG APP_REVISION=local

FROM golang:1.25.13-bookworm@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS go-build
ARG APP_REVISION
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w -buildid= -X main.buildRevision=${APP_REVISION}" -o /out/xboard ./cmd/xboard \
    && mkdir -p /out/data /out/backups \
    && chmod 0700 /out/data /out/backups

FROM node:24.18.0-trixie-slim@sha256:ae91dcc111a68c9d2d81ff2a17bda61be126426176fde6fe7d08ab13b7f50573 AS web-build
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm build

FROM scratch
ARG APP_REVISION=local
LABEL org.opencontainers.image.title="Xboard-Go" \
      org.opencontainers.image.description="Clean-room Xboard-compatible control plane" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-build /out/xboard /xboard
COPY --chown=65532:65532 --from=go-build /out/data /var/lib/xboard
COPY --chown=65532:65532 --from=go-build /out/backups /var/lib/xboard-backups
COPY --chown=65532:65532 --from=web-build /src/web/dist /srv/xboard/web
USER 65532:65532
EXPOSE 8080
ENV XBOARD_ADDRESS=0.0.0.0:8080 \
    XBOARD_DATABASE_DSN=file:/var/lib/xboard/xboard.db \
    XBOARD_PANEL_URL=http://127.0.0.1:7080 \
    XBOARD_ALLOWED_ORIGINS=http://127.0.0.1:7080 \
    XBOARD_COOKIE_SECURE=false \
    XBOARD_BACKUP_DIRECTORY=/var/lib/xboard-backups \
    XBOARD_WEB_ROOT=/srv/xboard/web
VOLUME ["/var/lib/xboard"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 CMD ["/xboard", "healthcheck"]
ENTRYPOINT ["/xboard"]
