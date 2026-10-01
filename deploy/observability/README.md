# Private Xboard logs (OBS-001 / Issue 299)

CI generates configurations with `configure.py`. All files containing credentials
are atomically written with mode 0600; existing writer and viewer credentials are
preserved. Keep the generated directory out of Git and backups without encryption.
Deployment/restarts are exclusively performed by the project workflow.

```sh
python3 configure.py central --directory /home/bingo/apps/xboard-logs --source development --source production
python3 configure.py collector --directory /opt/xboard-observability/collector-production --source production --tag xboard-panel-production --token-file /secure/writer-token --endpoint http://127.0.0.1:19428
```

The central Compose file is `compose.yaml`, the collector Compose file is
`collector-compose.yaml`. Central services use immutable upstream image digests;
the collector uses an exact source-SHA image built from pinned upstream images
and a signed Ubuntu package snapshot (see `COLLECTOR-IMAGE.md`). VictoriaLogs
stores data under `data/`, retains 30 days, and stops ingestion below 5 GiB free
space. It does not silently shorten retention to keep storage under 30 GiB.
The 30 GiB planning budget is a warning, not a deletion policy.

The only published central port is `127.0.0.1:19428`. Use an SSH local forward to
that port and open `http://127.0.0.1:19428/select/vmui/`. The username/password
are in the server's owner-readable `secrets.json` under `viewer`; retrieve them
privately, never paste them into a chat or workflow log. Writers have separate
random bearer credentials and can only call `/insert/jsonline`; the viewer can
only call `/select/*`. Administrative and debugging endpoints are not proxied.
This is one trusted operational workspace, not multi-tenant isolation: a writer
credential can submit arbitrary event fields, including another source label.

The collector reads only the exact Xboard Docker journal tag. Optional
`--unit xboard-node.service` adds that specific Node unit. It never mounts the
Docker socket or reads other containers' logs. App journald configuration and
the restricted SSH transport are managed separately by the deployment workflow.
The production host sends to its loopback SSH tunnel; there is no new public
ingestion port. Journald cursor and Vector's 2 GiB disk buffer survive restarts.
Buffer-full behavior blocks rather than discards. Host journals must remain
persistent and retain enough data for the outage window.

Raw lines, arbitrary messages, request bodies, credentials and SQL errors are
not forwarded. Structured diagnostic fields and namespaced event names are
retained. The checked-in `message-catalog.json` maps literal logger messages from
the panel and Node source to stable classifications, preserving their exact safe
description. Update this catalog when adding a non-namespaced static log message.
Node's approved nested `attributes` are flattened; report/config correlation,
retry state and process identity survive ingestion. Panel callsites are stored as
`code_source` so they cannot overwrite the collector's `source` identity.
Other lines become `unstructured_log_suppressed` events. The application
logger must still redact its allowed structured fields before writing locally.
The collector is defense in depth, not a sanitizer for arbitrary hostile values
placed inside those trusted fields. Journal cursor hashes provide stable event
IDs for identifying potential replay duplicates.

`health.py --directory /home/bingo/apps/xboard-logs` checks authenticated queries,
per-source canaries and panel HTTP events within three minutes, disk free space,
and the storage budget.
It emits sanitized JSON, persists `health.json`, and exits nonzero on warning.
Run it from the workflow-managed user-systemd timer every minute. Collector
Prometheus metrics are accessible only at `127.0.0.1:19429`; inspect buffer event
counts/bytes, discarded-event counters and component errors during diagnosis.
The source canary covers collector-to-store delivery; it does not prove that an
application is producing all expected business events. Check the service's own
request/lifecycle logs and container/unit status too.

Limits: delivery is at least once, so replay duplicates are possible. There is no
promise against journal vacuum/rate limiting, exhausted disks, filesystem loss,
collector outages longer than source retention, or the buffer fsync window.
No system can guarantee every business bug is diagnosable from logs alone.
This setup does not collect user traffic contents, device IP payloads or secrets.

Targeted checks:

```sh
python3 -m unittest discover -s deploy/observability -p '*_test.py' -v
python3 deploy/observability/smoke.py
```

The second command requires Linux Docker and creates/removes only an isolated,
randomly named Compose project. It exercises auth boundaries, persisted query
results, Vector redaction and restart recovery after an ingestion outage.
It also validates the actual journald configuration, asserts the image includes
`journalctl`, and creates an isolated fixture container with the exact journal tag
to check journal ingestion and collector restart recovery. The Linux CI host must
have systemd journald; this verification fails rather than silently skipping it.
Production source presence still requires verification after deployment.

Upstream references: [VictoriaLogs](https://github.com/VictoriaMetrics/VictoriaLogs),
[Vector](https://github.com/vectordotdev/vector),
[Vector journal source](https://vector.dev/docs/reference/configuration/sources/journald/),
[Vector buffering](https://vector.dev/docs/architecture/buffering-model/),
[vmauth](https://docs.victoriametrics.com/vmauth/).
