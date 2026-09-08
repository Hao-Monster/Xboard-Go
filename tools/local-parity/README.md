# Local user-parity templates

These files persist the previously temporary local Oracle, Redis, and candidate
setup. They are **transcript-reconstructed and pending revalidation**: the
original `.local` overlay and PHP scripts were deliberately cleaned after the
recorded run. Do not treat this directory as proof of a fresh parity result.

`run.sh` is intentionally split into bounded actions:

```bash
tools/local-parity/run.sh prepare
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh start
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh init
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh verify
# Assigned executor runs the exact Playwright target after verify.
LOCAL_PARITY_RUN_ID=<printed-run-id> tools/local-parity/run.sh cleanup
```

`prepare` creates only ignored `.local/local-parity-<run-id>` secrets and runs
`docker compose config --quiet`; it does not start containers. `start`, `init`,
and `verify` must be performed by the assigned local executor. The exact
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
1.26.8 toolchain, or supply a precompiled Linux binary together with its hash
and immutable source-manifest reference:

```bash
export LOCAL_PARITY_PREBUILT_XBOARD=/absolute/path/to/xboard-linux-amd64
export LOCAL_PARITY_PREBUILT_XBOARD_SHA256=<lowercase-sha256>
export LOCAL_PARITY_PREBUILT_XBOARD_SOURCE=<manifest-or-artifact-reference>
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

Before calling a run evidence, retain the generated overlay/config output,
image inspection, migration/init logs, HTTP checks, target Playwright reporter
output, and cleanup log. The candidate image's ID and RepoDigests are recorded
as separate fields; do not infer one from the other.
