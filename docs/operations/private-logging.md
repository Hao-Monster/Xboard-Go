# Private internal-test logging

Work item OBS-001, issue #299. The log center runs on bingo-dev with VictoriaLogs,
Vector collectors and vmauth. Only Xboard panel/node operational diagnostics are
in scope. Remnawave and unrelated projects are not collected or reconfigured.

## Access and retention

Open a local SSH forward from the operator workstation:

```powershell
ssh -N -L 19428:127.0.0.1:19428 bingo-dev
```

Then open `http://127.0.0.1:19428/select/vmui/`. Query authentication is required.
The generated viewer credentials live in `/home/bingo/apps/xboard-logs/secrets.json`
on bingo-dev, mode 0600. An authorized operator can read them in their own private
SSH terminal; never paste this file into a chat, issue, CI log or screenshot.
Writer tokens cannot query logs; the viewer cannot insert or call admin endpoints.

Logs remain on bingo-dev under `/home/bingo/apps/xboard-logs/data` for 30 days.
30 GiB is a capacity warning, not a hard early-deletion limit. Below 5 GiB free
space the storage engine stops accepting writes; collectors buffer and retry.
Each collector has a 2 GiB disk buffer and a persistent journal cursor. A host
disk failure or source journal rotation beyond retained history can still lose
records; there is no infinite-outage or zero-loss claim.

## Collection and diagnosis

Panel application containers use only their own journald tag:
`xboard-panel-development` and `xboard-panel-production`. Journal files are mounted
read-only; no Docker socket is given to Vector. Application logs retain generated
request IDs, route templates, HTTP status/duration, safe error category/code,
source location, argument-free panic stacks and node/machine operation metadata.
Internal-test overlays enable DEBUG for node lifecycle correlation. Passwords,
headers, raw URLs, request bodies, SQL values and user/device payloads are omitted.

Useful LogsQL queries:

```text
_time:1h source:production level:ERROR
_time:1h request_id:YOUR_REQUEST_ID
_time:1h machine_id:1
_time:3m event:collector.canary
```

The `X-Request-ID` HTTP response header links a failed UI request to its server
events. Start with source/revision/request ID, then error category/code and source
location. Node durable reports use a hashed report correlation ID; they do not
expose the original identifier or traffic payload.

Raw upstream errors and suppressed kernel events cannot be reconstructed if they
were never safely emitted. This system is for operational diagnosis, not recording
user traffic. Node host enrollment is pending the user's host identities.

## Private transport and monitoring

Production internal test uses a restricted SSH reverse forward: its loopback
19428 reaches bingo-dev's authenticated ingestion gateway. The dedicated private
key stays on bingo-dev. Its production authorized-key entry disables commands,
PTY and agent/X11 forwarding and limits listen/forward targets to loopback 19428.
No public log port or Remnawave route is added.

On bingo-dev:

```bash
systemctl --user status xboard-logs-tunnel.service
systemctl --user status xboard-logs-health.timer
cat /home/bingo/apps/xboard-logs/health.json
journalctl --user -u xboard-logs-health.service --since '1 hour ago'
docker compose -f /home/bingo/apps/xboard-logs/compose.yaml ps
```

Health checks run every minute and report stale transport canaries, missing panel
HTTP events, query failure, disk budget and free space. A separate scoped runtime
probe records Xboard's restart count, OOM flag, exit status and collector buffer/
error counters through the same journal pipeline. This is an on-host alarm/status report, not an external
notification channel. Canary health alone does not prove every application event
was emitted. Inspect collector metrics on its host at loopback 19429 for dropped
events, journal/source errors and buffer pressure as well.

## Release and rollback

All changes are released through `.github/workflows/observability.yml` on protected
main. The workflow first executes isolated Docker auth, persistence and outage
checks, then deploys exact-SHA scripts. Application image releases remain in the
existing development/production workflows. They preserve the separate logging
overlay without altering the installed base Compose digest.

The logging deployment changes only the Xboard app logging driver and its log
environment. Existing images and data volumes are retained. If its app restart
fails, a newly introduced overlay is removed and the existing base Compose is
started again. Production deployment compares Remnawave container IDs/start times
before and after. Failure diagnostics are private host files, never raw CI output.

The collector image includes journalctl 259 from a signed, fixed Ubuntu snapshot.
This avoids journalctl 250–257's cross-boot `--follow` limitation without changing
the host OS or weakening replay requirements.

For a broader rollback, use CI with the previous reviewed logging source and
retained credentials/data. Do not delete the data directory or journal. An explicit
logging disable/removal operation requires review; stopping collection does not
require rolling back business data.

## Validation evidence

Current exact source, CI and host verification evidence is recorded in
`docs/project/observability-2026-10-02.json`. Pending entries must not be interpreted
as successful deployments. No node delivery is accepted until an actual node host
is provided, enrolled and observed in the center.
