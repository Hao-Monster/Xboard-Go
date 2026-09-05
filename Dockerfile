ARG APP_REVISION=local
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct

FROM golang:1.25.13-bookworm@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS go-build
ARG APP_REVISION
ARG GO_MODULE_PROXY
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod GOPROXY="${GO_MODULE_PROXY}" go mod download
COPY cmd ./cmd
COPY internal ./internal
ADD --checksum=sha256:5555fd1aab63e06096d9ee2a4187e93b8451e650b77ce4138a26eb9cf4d81469 \
    https://raw.githubusercontent.com/zoujingli/ip2region/a031c359620c22889fac7b998409fdcdef76a69c/ip2region.xdb \
    /out/ip2region.xdb
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w -buildid= -X main.buildRevision=${APP_REVISION}" -o /out/xboard ./cmd/xboard \
    && mkdir -p /out/data/admin-exports /out/backups /out/tmp \
    && chmod 0700 /out/data /out/data/admin-exports /out/backups /out/tmp

FROM node:24.18.0-trixie-slim@sha256:ae91dcc111a68c9d2d81ff2a17bda61be126426176fde6fe7d08ab13b7f50573 AS web-build
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm build

FROM scratch AS backend
ARG APP_REVISION=local
LABEL org.opencontainers.image.title="Xboard-Go Backend" \
      org.opencontainers.image.description="Clean-room Xboard-compatible Go control plane backend" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-build /out/xboard /xboard
COPY --chown=65532:65532 --from=go-build /out/data /var/lib/xboard
COPY --chown=65532:65532 --from=go-build /out/backups /var/lib/xboard-backups
COPY --chown=65532:65532 --from=go-build /out/tmp /tmp
COPY --chown=65532:65532 --from=go-build /out/ip2region.xdb /usr/share/xboard/ip2region.xdb
COPY --chown=65532:65532 LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/xboard-go/
USER 65532:65532
EXPOSE 8080
ENV XBOARD_ADDRESS=0.0.0.0:8080 \
    XBOARD_DATABASE_DSN=file:/var/lib/xboard/xboard.db \
    XBOARD_PANEL_URL=http://127.0.0.1:7080 \
    XBOARD_ALLOWED_ORIGINS=http://127.0.0.1:7080 \
    XBOARD_COOKIE_SECURE=false \
    XBOARD_BACKUP_DIRECTORY=/var/lib/xboard-backups \
    XBOARD_ADMIN_EXPORT_ROOT=/var/lib/xboard/admin-exports \
    XBOARD_IP2REGION_XDB_FILE=/usr/share/xboard/ip2region.xdb
VOLUME ["/var/lib/xboard"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 CMD ["/xboard", "healthcheck"]
ENTRYPOINT ["/xboard"]

FROM ghcr.io/nginx/nginx-unprivileged:1.30.4-alpine-slim@sha256:b1d850535f815f18e9dd12ca7e9f4d36de36a49e246ccd8ef64289b0d06bde49 AS frontend
ARG APP_REVISION=local
LABEL org.opencontainers.image.title="Xboard-Go Frontend" \
      org.opencontainers.image.description="Static Xboard-Go frontend deployment unit" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY .docker/frontend/nginx.conf /etc/nginx/nginx.conf
COPY --chown=101:101 --from=web-build /src/web/dist /srv/xboard/web
COPY --chown=101:101 LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/xboard-go/
USER 101:101
EXPOSE 8080
STOPSIGNAL SIGQUIT
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/frontend-healthz"]
ENTRYPOINT ["nginx", "-g", "daemon off;"]

FROM ghcr.io/nginx/nginx-unprivileged:1.30.4-alpine-slim@sha256:b1d850535f815f18e9dd12ca7e9f4d36de36a49e246ccd8ef64289b0d06bde49 AS gateway
ARG APP_REVISION=local
LABEL org.opencontainers.image.title="Xboard-Go Gateway" \
      org.opencontainers.image.description="Same-origin Xboard-Go gateway deployment unit" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY .docker/gateway/nginx.conf /etc/nginx/nginx.conf
COPY --chown=101:101 LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/xboard-go/
USER 101:101
EXPOSE 8080
STOPSIGNAL SIGQUIT
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/gateway-healthz"]
ENTRYPOINT ["nginx", "-g", "daemon off;"]

# Keep the combined image as the default final target until the split topology
# has independent lifecycle and rollback evidence.
FROM backend AS combined
ARG APP_REVISION=local
LABEL org.opencontainers.image.title="Xboard-Go" \
      org.opencontainers.image.description="Clean-room Xboard-compatible control plane" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY --chown=65532:65532 --from=web-build /src/web/dist /srv/xboard/web
ENV XBOARD_WEB_ROOT=/srv/xboard/web
