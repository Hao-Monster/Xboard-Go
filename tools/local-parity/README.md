# Local user-parity templates

These files persist the previously temporary local Oracle, Redis, and candidate
setup. They are **transcript-reconstructed and pending revalidation**: the
original `.local` overlay and PHP scripts were deliberately cleaned after the
recorded run. Do not treat this directory as proof of a fresh parity result.

`run.sh` is intentionally split into bounded actions:

```bash
tools/local-parity/run.sh prepare
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh start
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh verify
# Assigned executor runs the exact Playwright target after verify.
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh cleanup
```

`prepare` creates only ignored `.local/local-parity-<run-id>` secrets and runs
`docker compose config --quiet`; it does not start containers. `start` first
starts only the internal Redis, migrates and initializes the Oracle in
one-shot containers, then starts the Oracle Web process and candidate. This
ordering is required: the legacy `admin_setting` implementation caches all
settings forever in Redis, and its V2/admin routes are registered at process
startup. The initializer then invalidates only that documented settings-cache
key after its database writes, because its own Laravel CLI bootstrap can load
the route files before those writes; it never flushes Redis wholesale. `verify`
and `cleanup` must be performed by the assigned local
executor. The exact
Compose project is `xboard-user-parity-<run-id>` and every public port is bound
to loopback. `cleanup` destroys only that run's Compose resources, candidate
image, and generated `.local` directory; it retains deidentified logs in
`output/local-parity-<run-id>/`.

The source tree must have no tracked modifications. `prepare` records its exact
HEAD and the user-parity test-file SHA-256; `start` rejects a different runtime
HEAD, so an arbitrary revision label cannot be attached to dirty source. The
candidate binary is built with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, and its
Go version/build metadata are retained in the run evidence.

This workstation's WSL environment may not have Go 1.26.8. Do not download a
replacement automatically. Instead either run with an already verified Go
1.26.8 toolchain, or supply a precompiled Linux binary and a strict manifest:

```bash
export LOCAL_PARITY_PREBUILT_XBOARD=/absolute/path/to/xboard-linux-amd64
export LOCAL_PARITY_PREBUILT_MANIFEST=/absolute/path/to/xboard-linux-amd64.manifest
```

The manifest is a six-line `key=value` file with no extra or duplicate keys:

```text
source_commit=<exact-lowercase-40-character-git-sha>
binary_sha256=<lowercase-64-character-sha256>
goos=linux
goarch=amd64
cgo_enabled=0
go_version=go1.26.8
```

`start` verifies the manifest against the clean current HEAD and the actual
binary, then copies the manifest and records its SHA-256. The no-secret quick
check is `tools/local-parity/run.sh validate-prebuilt` with the two variables
above; it starts neither Docker nor a build.

Run this template from a WSL-native clone, not directly from a Windows-managed
worktree whose `.git` pointer contains a drive-letter path. On Windows, create
a bundle containing the exact clean HEAD; then clone that bundle into a unique
WSL directory and detach at the recorded SHA. Do not alter global Git settings
or worktree metadata. The bundle transfers committed source only: ignored
`.local` secrets, runtime databases, logs, and other untracked files are not
inputs and must be generated afresh by `prepare`.

```powershell
# Windows-managed worktree, after confirming tracked status is clean.
$paritySha = git rev-parse HEAD
git bundle create .local/local-parity-source.bundle $paritySha
```

```bash
# WSL, using a new directory outside the Windows worktree.
git clone /mnt/c/path/to/worktree/.local/local-parity-source.bundle /tmp/xboard-local-parity
git -C /tmp/xboard-local-parity checkout --detach <recorded-sha>
cd /tmp/xboard-local-parity
```

Set a distinct `LOCAL_PARITY_RUN_ID` and loopback ports before `prepare` when
another executor is active. For example, one executor may use 18780–18782 and
another 18880–18882; candidate image tags are derived from the run ID. Do not
rebuild or retag the shared `xboard-go:local` base image while another local
run uses it.

The overlay sends the local Oracle to an internal Redis service called
`legacy-redis` and uses `redis-server --dir /tmp --save "" --appendonly no`.
This avoids the prior `/data` tmpfs-permission failure. The Oracle schema is
created only by `php /www/artisan migrate --force --no-interaction`; the PHP
initializer uses Laravel models and never creates tables manually.
It mirrors the image installer for a newly created administrator: only new
rows receive `uuid=Helper::guid(true)` and `token=Helper::guid()`, the two
non-default `v2_user` identity columns. Re-entry preserves those existing
identities while refreshing the generated administrator credential. Settings
are written through `Setting::createOrUpdate(name, value)`.

`verify` records the generated legacy administrator URL only after a strict
legacy-state check exits successfully: both database path keys, both cached
path keys, and the registered V2 administrator route must exactly match the
generated path. It then requires an HTTP success for that URL.
It separately records the legacy root (`/`) status. A root failure is not
treated as evidence about the administrator-route prefix: `/` is a distinct
theme-rendering route. For a root 5xx it retains only a deidentified exception
category and first application/vendor stack location from the latest Laravel
log; it never retains raw request data or full logs. The assigned executor
must use that small diagnostic record to decide whether another targeted
Oracle investigation is needed.

The initializer was revalidated against `xboard-legacy-parity:8065164` on
2026-09-08 in a fresh project using the image's real migrations: two consecutive
initializations left one administrator and the legacy login endpoint returned
HTTP 200. That evidence covers this Oracle initialization only; the candidate
build and the assigned Playwright case still require their own execution.

Before calling a run evidence, retain the generated overlay/config output,
image inspection, migration/init logs, HTTP checks, target Playwright reporter
output, and cleanup log. The candidate image's ID and RepoDigests are recorded
as separate fields; do not infer one from the other.
