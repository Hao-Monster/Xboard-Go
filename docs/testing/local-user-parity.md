# Local user parity reconstruction record (MIG-001 / CI-001)

## Status and evidence boundary

This document records an independently completed local user-parity run from
2026-09-08, using a newly initialized legacy Oracle, a Go candidate, and an
isolated Redis service. It is a reconstruction record, not a claim that the
environment is currently running or that the removed temporary scripts can be
executed unchanged.

The primary persisted evidence is
`output/user-generation-parity-evidence-2026-09-08.md` in the originating
worktree. Its temporary Compose overlay, Dockerfile, PHP initialization
scripts, image context, and generated secrets were intentionally deleted at
cleanup. Recreate them from the facts below, run `docker compose config`, and
revalidate before asserting a new result.

`tools/local-parity/` now contains a persistent, no-secret reconstruction
template. It is labelled transcript-reconstructed and remains pending
revalidation; it is not the deleted original overlay or initialization script.
The runner binds its candidate label to a clean current Git HEAD, records the
runtime and test-source identities separately, cross-compiles Linux/amd64 with
`CGO_ENABLED=0`, and supports a hash- and manifest-bound prebuilt Linux binary
when a local Go 1.26.8 toolchain is unavailable.

No production data, credentials, or application key is part of this recipe.
Every attempt generates its own synthetic `APP_KEY`, administrator passwords,
and random synthetic test user.

## Verified run identity

| Item | Recorded value |
| --- | --- |
| Compose project | `xboard-user-parity-7ed5096` |
| Candidate source revision | `7ed509693d3fdd53ad21220ded57e3f22691f55d` |
| Candidate image | `xboard-go:user-parity-7ed5096` (removed after cleanup) |
| Reported candidate image reference before cleanup | `xboard-go@sha256:db1d6a969ca42bce8b14e9c85a4b3119c48640dafcd86af21f5e7ec3bc780468` (the removed image cannot now be re-inspected, so this is not re-labelled as a verified RepoDigest) |
| Legacy image | `xboard-legacy-parity:8065164` |
| Legacy image ID | `sha256:efa8e6d487a8dbfe991f50386657eeb8943d46ad1b3f2a2725c06806f256fd41` |
| Legacy RepoDigest | `xboard-legacy-parity@sha256:efa8e6d487a8dbfe991f50386657eeb8943d46ad1b3f2a2725c06806f256fd41` (re-inspected locally with `docker image inspect .RepoDigests`) |
| Legacy `composer.lock` SHA-256 | `72b4e90e9c84cb8644b1fc70f360bd5ec3d3d6664be6eb853ad6bf9fc34ed49b` |
| Legacy install script SHA-256 | `40d5f838eac860deefd09a8cc71eb7ae7d661d47e89bb04539fce521ba4efcff` |
| Legacy migration list | 56 files; sorted-path SHA-256 `8b8a6efaa2481236e1f03a4253fe40a4cd28a20221a03a89c0206fc08072f649` |

The legacy image has no `/www/.git`; identify it with the image identity and
the recorded Composer/install/migration hashes rather than inventing a source
revision.

## Isolated topology

```text
loopback host
  127.0.0.1:18780 -> Go candidate
  127.0.0.1:18781 -> local legacy Oracle
  127.0.0.1:18782 -> Mailpit

unique Compose network
  Go candidate, local legacy Oracle, Mailpit, captcha-stub, legacy-redis
```

`captcha-stub` and `legacy-redis` are internal Compose services. Redis has no
host port. The Oracle reaches it through the service name `legacy-redis`, not
`localhost`; this avoids the legacy image's internal s6 Redis startup path.

The recorded Redis service used the pinned repository image
`redis:8.4.2-alpine@sha256:e1b6db24cb4fdd89f4bc9be09f671ea3bec92fbd7042554f76c34aa2be9b59ad`
and command:

```text
redis-server --dir /tmp --save "" --appendonly no
```

`--dir /tmp` is material: the earlier default `/data` plus tmpfs permissions
prevented Redis from starting.

## Oracle configuration and real schema initialization

The local Oracle was a new container, not the historical CI Oracle. Its
effective environment was:

```text
REDIS_HOST=legacy-redis
REDIS_PORT=6379
ENABLE_REDIS=false
ENABLE_HORIZON=false
ENABLE_SCHEDULER=false
ENABLE_WS_SERVER=false
ENABLE_WEB=true
ENABLE_CADDY=true
SKIP_XBOARD_UPDATE=true
DB_CONNECTION=sqlite
DB_DATABASE=.docker/.data/database.sqlite
INSTALLED=true
```

The schema was created with the image's real migrations, not hand-written
tables:

```text
php /www/artisan migrate --force --no-interaction
```

After that command, an ephemeral local PHP initialization script upserted one
administrator and wrote both `secure_path` and `frontend_admin_path` as
`e2e-admin-secure`. Its recorded check was
`{"admin_count":1,"secure_path":"e2e-admin-secure"}`. The initialization
script itself was removed, so its exact source must be reconstructed and
reviewed before a repeat run; do not claim it is a saved launcher.

The persistent reconstruction in `tools/local-parity/init-legacy-oracle.php`
was subsequently verified against a freshly migrated local Oracle image: it
uses the installer-equivalent UUID/token generation for a new user, preserves
those identities on re-entry, and writes settings by `name`. Two consecutive
runs produced one administrator with the expected secure path; the legacy
login API returned HTTP 200. This does not replace a full candidate or browser
parity run.

## Candidate configuration and startup order

The removed local overlay was named `.local/compose.user-parity-local.yaml`.
The candidate used the normal Compose file plus that overlay and these recorded
variables:

```text
COMPOSE_PROJECT_NAME=xboard-user-parity-7ed5096
XBOARD_GO_IMAGE=xboard-go:user-parity-7ed5096
XBOARD_GO_REVISION=7ed509693d3fdd53ad21220ded57e3f22691f55d
XBOARD_GO_PORT=18780
XBOARD_LEGACY_PORT=18781
XBOARD_MAILPIT_PORT=18782
XBOARD_GO_BOOTSTRAP_ADMIN_EMAIL=admin@legacy-parity.test
XBOARD_GO_BOOTSTRAP_ADMIN_PASSWORD_FILE=.local/user-parity-bootstrap-password.txt
XBOARD_GO_SETTINGS_ENCRYPTION_KEY_FILE=.local/user-parity-settings-key.txt
XBOARD_E2E_ADMIN_PATH=e2e-admin-secure
XBOARD_LEGACY_ADMIN_PATH=e2e-admin-secure
XBOARD_CAPTCHA_ALLOW_INSECURE=true
XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL=http://captcha-stub:4199/recaptcha
XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL=http://captcha-stub:4199/recaptcha-v3
XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL=http://captcha-stub:4199/turnstile
```

`LOCAL_LEGACY_APP_KEY` was read from a private generated file and is purposely
not recoverable from this record. Generate a replacement only for the new run.

The completed run proceeded in this order:

1. Keep a WSL Bash session alive because this host's WSL Docker context stopped
   containers once WSL became idle.
2. Build a Linux `cmd/xboard` binary with `CGO_ENABLED=0 GOOS=linux
   GOARCH=amd64`; copy existing `web/dist` into a temporary image context.
3. Build the candidate image from local `xboard-go:local`, replacing `/xboard`
   and `/srv/xboard/web`, and verify its revision label.
4. Start `compose.local.yaml` plus the generated overlay with the `e2e` profile,
   `--no-build`, `-d`, and `--wait`.
5. Wait for candidate, Oracle, `legacy-redis`, Mailpit, and captcha stub health;
   then execute the real legacy migration and ephemeral administrator setup.
6. Verify legacy login (`200`, recorded body length 231) and Go admin page (`200`,
   recorded body length 560) before running browser parity.

The historical CI workflow is reference material only. Its persistent Oracle
container name, port, and secret locations are not requirements for this
isolated local topology.

## User parity evidence and current limitation

The persisted run executed:

```text
playwright test --config playwright.parity.config.ts user-generation-persistence.spec.ts --reporter=list --trace off
```

It first failed on a nullable `speed_limit` contract mismatch and then passed
after the test-only compatibility change (`1 passed`). The persisted Playwright
state file reported `{"status":"passed","failedTests":[]}` and had SHA-256
`91D1C43004802CD49950D78EB11C8FA7D05DA8FFFFE219A8B13B2F561BC00903`.
Raw red and green reporter stdout were not persisted. A later test case is not
covered by that evidence until it is run separately.

For every repeat, create a random lowercase synthetic identifier and
`@parity.test` email. Use supported application APIs, apply only the selected
user mutation, and read persistent state back through both legacy and Go APIs.
Do not insert rows directly or tailor a schema to the test.

## Exact cleanup boundary

The recorded cleanup sequence was:

1. Run Compose `down --volumes --remove-orphans` with the exact run project and
   the same base/overlay files.
2. Remove only that run's candidate image.
3. Delete only that run's generated overlay, init scripts, image context, and
   secret files.
4. Restore the local Playwright `web/node_modules` junction arrangement, if it
   was temporarily changed for that run.
5. Close the WSL keepalive session and verify no containers for the exact
   project remain.

Never use broad Docker prune commands or remove another project's containers,
volumes, images, or networks.

## Required reconstruction artifacts

Before calling a reconstructed run reproducible, persist the newly generated
Compose overlay, temporary Dockerfile, Oracle initialization scripts, redacted
command log, image inspections, health checks, and test reporter output. The
missing previous transient files and reporter stdout are proof gaps, not
permission to fabricate them.
