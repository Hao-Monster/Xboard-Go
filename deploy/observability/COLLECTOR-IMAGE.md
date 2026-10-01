# Collector runtime compatibility

Build from the repository root in CI:

```sh
docker build -f deploy/observability/Dockerfile.collector -t xboard-log-collector:COMMIT_SHA .
docker run --rm --entrypoint journalctl xboard-log-collector:COMMIT_SHA --version
```

Use that built image for all Vector validation, smoke fixtures, and deployed
collectors. Do not keep the original upstream Debian image in one of those paths.
The standard image entrypoint remains `/usr/bin/vector`; the configuration and
volume interface is unchanged. Runtime does not need network access to APT.

Vector 0.58.0 only supplies Debian, Alpine and distroless Docker variants. Its
Debian image uses trixie and systemd 257. Vector rejects
`current_boot_only=false` with journalctl versions 250 through 257 because those
versions implicitly restrict follow mode to the current boot. Reverting to
current-boot-only would discard the required cross-reboot recovery behavior.

This image copies the immutable official Vector binary and license assets onto
the immutable Ubuntu 26.04 image, with journalctl 259.5. The dated Ubuntu snapshot
fixes both explicit package versions and transitive dependency resolution.
APT still checks Ubuntu signatures and package hashes; only archive metadata's
expiration check is disabled for the fixed historical snapshot. Build-time
checks reject old journalctl or a Vector version mismatch and detect missing
binary runtime libraries.

Versions checked against the official snapshot's `main/binary-amd64/Packages.xz`
on 2026-10-02:

| Package | Version |
| --- | --- |
| systemd, libsystemd0 | 259.5-0ubuntu3.4 |
| ca-certificates | 20260601~26.04.1 |
| libsasl2-2 | 2.1.28+dfsg1-9ubuntu3 |
| tzdata | 2026c-0ubuntu0.26.04.1 |

Sources:

- [Official Vector 0.58.0 Docker variants](https://github.com/vectordotdev/vector/tree/v0.58.0/distribution/docker)
- [Official Vector Debian Dockerfile](https://github.com/vectordotdev/vector/blob/v0.58.0/distribution/docker/debian/Dockerfile)
- [Dated Ubuntu snapshot](https://snapshot.ubuntu.com/ubuntu/20261001T000000Z/dists/resolute-updates/main/binary-amd64/Packages.xz)
- [Ubuntu systemd package](https://packages.ubuntu.com/resolute/systemd)

This file describes the selected build inputs; the CI build and integration test
result must be reported separately. No local Docker build was possible while the
Windows Docker engine was unavailable.
