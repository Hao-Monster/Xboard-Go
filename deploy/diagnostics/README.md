# Customer Connection Diagnostics Storage & Collector Infrastructure (D6.1 / D6.2 / D6.3)

This directory provides configuration generators, storage guards, precheck utilities,
and automated verification for the Customer Connection Diagnostics system (CC01–CC18).

## Architecture & Data Flow

```text
[Node Observer] -> Journald -> [Vector Diagnostics Collector]
                                     | (Bearer Auth, 2 GiB Disk Buffer)
                                     v
                       [vmauth Dedicated Reverse Proxy] (127.0.0.1:19429)
                                     | (Fixed extra_fields: panel_scope, node_id, collector_id)
                                     v
                         [Loopback Storage Guard] (8429)
                                     | (Checks: usage, root free space, mount validity, latching)
                                     v
                           [VictoriaLogs Ingestion] (9428)
```

Direct admin queries for diagnostics bypass the write guard and connect through `vmauth`
directly to `VictoriaLogs` (`/select/.*`), while health checks are routed to `Guard` (`/diagnostics/health`).

## Configuration Generation (`configure.py`)

### 1. Central Storage & Ingestion Plane

```sh
python3 configure.py central \
  --directory /home/bingo/apps/xboard-customer-diagnostics-pilot \
  --scope xboard-main \
  --source node-701 \
  --source node-702 \
  --port 19429
```

Generated outputs:
- `secrets.json`: `viewer` credentials and per-source `writers` bearer tokens (mode 0600).
- `auth.json`: `vmauth` configuration enforcing role separation and server-side `extra_fields`.
- `compose.yaml`: Project `xboard-customer-diagnostics-pilot` containing `victoria-logs`, `guard`, and `auth`.
- Directories: `data/` (30 GiB dedicated volume) and `state/` (isolated health and latch state).

### 2. Node Diagnostic Collector

```sh
python3 configure.py collector \
  --directory /opt/xboard-diagnostics/collector-node-701 \
  --source node-701 \
  --scope xboard-main \
  --node-id 701 \
  --token-file /secure/node-701-token \
  --tag xboard-node-701 \
  --endpoint http://127.0.0.1:19429 \
  --unit xboard-node.service
```

Collector properties:
- **Dedicated Data Directory**: `/var/lib/vector-diagnostics` (completely isolated from legacy operational logging).
- **Buffer**: 2 GiB disk buffer (`when_full: block`).
- **Family Whitelist**: Only approved events within `connection`, `resolver_operation`, and `node` families are forwarded; all unapproved logs abort.
- **Timestamp**: Enforces valid RFC 3339 `event_time`. Missing or invalid timestamps are dropped and counted; ingress fallback is prohibited.
- **Data Sanitization**: Strips forbidden fields (`user_id`, `uuid`, `token`, `email`, `client_ip`, `raw_url`, `headers`, `stack_raw`, etc.) and suppresses raw `message`.

## Storage Guard & Capacity Management (`guard.py`)

- **Thresholds**:
  - `Warning (16 GiB)`: Logs `capacity_warning`.
  - `Critical (20 GiB)`: Logs `capacity_critical`.
  - `Stop Ingestion (24 GiB used, <= 6 GiB free, or host root < max(50 GiB, 20%))`: Transitions state to `blocked` and sets persistent latch (`state/latched`).
- **Persistent Latching**:
  - Once stopped, the latch persists across container and guard restarts.
  - Ingestion returns `503 Service Unavailable: diagnostics_storage_not_ready`.
  - Recovery requires explicit operator authorization via `POST /diagnostics/resume`.
- **Health Check (`GET /diagnostics/health`)**:
  - Returns `schema_version`, `panel_scope`, `storage_id`, `state`, `reason_codes`, `check_age_seconds`, `latched`, `data_used_bytes`, `data_available_bytes`, `root_available_bytes`.
  - Evaluated using monotonic clock; checks older than 60 seconds automatically report `blocked` (`check_stale`).

## Mount & Capacity Precheck (`precheck.py`)

Verifies host prerequisites prior to starting container services:
```sh
python3 precheck.py --data-dir ./data --state-dir ./state --root-dir /
```

Ensures dedicated mount exists, rejects fallback to root filesystem, verifies write permissions, and enforces host root disk headroom (`>= max(50 GiB, 20%)`).

## Verification Commands

```sh
python3 -m unittest discover -s deploy/diagnostics -p '*_test.py' -v
go test -v -count=1 ./internal/diagnostics/...
git diff --check
```
