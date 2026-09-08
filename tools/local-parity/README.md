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

The overlay sends the local Oracle to an internal Redis service called
`legacy-redis` and uses `redis-server --dir /tmp --save "" --appendonly no`.
This avoids the prior `/data` tmpfs-permission failure. The Oracle schema is
created only by `php /www/artisan migrate --force --no-interaction`; the PHP
initializer uses Laravel models and never creates tables manually.

Before calling a run evidence, retain the generated overlay/config output,
image inspection, migration/init logs, HTTP checks, target Playwright reporter
output, and cleanup log. The candidate image's ID and RepoDigests are recorded
as separate fields; do not infer one from the other.
