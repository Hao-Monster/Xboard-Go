# Xboard-Go

Xboard-Go is an Apache-2.0 clean-room reimplementation of the Xboard control plane with a Go backend and a maintainable React/TypeScript administration interface.

Third-party components and compatibility assets are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

The project preserves verified business behavior while replacing inaccessible legacy frontend code and progressively rebuilding the backend. Correctness, security, data compatibility, measurable performance, and operational reliability are first-class constraints.

The implemented vertical slices cover administrator authentication and account-session security, atomic public email/password registration with administrator-controlled registration policies and optional email verification, invitation-code registration and referral relationship tracking, email-code password recovery, a cursor-paginated user directory and access-state editor, revision-safe plan catalog management and user-visible offers, normal user order creation and completion plus administrator order assignment, administrator-managed referral commission rules, exactly-once settlement, tiered distribution, commission history and balance transfer, server-machine and node association management, one-time hashed machine enrollment credentials, load reporting, revision-safe daily activation schedules, server permission groups, panel routing rules, notices, multilingual knowledge management and public sharing, a validated cross-platform client download catalog, user and administrator ticket lifecycles, centralized SMTP settings with subscription-expiry and traffic reminders, credential-safe Telegram bot and Webhook settings with replay-resistant join-request handling, system runtime status and administrator audit, and the Xboard-Node runtime control-plane contract. Password authentication has administrator-configurable persistent error thresholds and expiry windows, normalized identity keys that cannot be bypassed with case or whitespace changes, account-enumeration-resistant failures, and an independent bounded source-IP safeguard. Account holders can list and revoke their active sessions; password and other security-sensitive account changes atomically revoke every session. Registration applies bounded Argon2id concurrency, per-address resource limits, strict origin checks for browsers, normalized email-domain whitelists, scoped Gmail-alias protection, a persistent successful-registration IP quota, Google reCAPTCHA v2/v3 or Cloudflare Turnstile server verification, purpose-isolated six-digit verification codes with durable cooldown, lockout, encrypted outbox delivery and transactional one-time consumption, and CSPRNG invitation codes protected by keyed digests and authenticated encryption at rest. CAPTCHA server credentials are purpose-isolated under authenticated encryption, never returned by the administration API, and enforced on direct registration plus registration and recovery email-code requests; v3 score and action are verified server-side and provider failures fail closed. Invitation relationships and single-use code consumption commit atomically with the user and initial session; reusable codes preserve the verified legacy behavior. Password recovery uses cryptographically generated six-digit codes, persistent cooldown and lockout state, account-enumeration-resistant responses, authenticated encryption at rest, a durable mail outbox, one-time transactional consumption, and bounded password hashing; a successful reset revokes every existing session. The verified Xboard `/api/v1` and `/api/v2` Passport surface remains available for password login and registration, email verification and recovery, mail and quick login links, one-time token exchange, invitation-view tracking, and normal order workflows with their legacy validation and response contracts. Internally these compatibility routes preserve purpose isolation, enumeration resistance, bounded abuse controls, transactional credential handling, trusted browser origins, and a redirect allowlist instead of reproducing the legacy open-redirect behavior. Policy checks are repeated transactionally so account, initial session, verification-code consumption, invitation consumption and relationship creation, and IP counter changes cannot bypass concurrent configuration changes or commit partially; expired IP, login-failure, email-challenge, and stale pending-order state is removed by bounded background maintenance. Plan state changes use optimistic revisions, capacity is computed by one aggregate query, unlimited capacity remains explicit, referenced plans cannot be deleted, forced entitlement synchronization is transactional, and due traffic resets use bounded exactly-once state transitions with an audit record. Order amounts use integer cents; server-side plan pricing, balance and surplus deductions, cancellation refunds, entitlement activation, status transitions, and referral attribution commit atomically. A partial unique index prevents concurrent duplicate active orders, completion is idempotent, and the compatibility administrator route accepts a validated configurable legacy path segment without exposing it in audit rows. Node configuration, selected routing rules, and user snapshots are available over authenticated HTTP and WebSocket transports; user access changes use bounded per-user deltas while traffic reports are transactionally persisted with durable idempotency, node-group authorization, bounded inputs, and cross-node device-state synchronization. Referenced groups and routes are protected by transactional integrity checks so administrative changes cannot leave dangling node configuration. Knowledge articles support categories, languages, optimistic editing, visibility and ordering, subscriber-only regions, user-specific placeholders, and server-rendered public pages with strict HTML sanitization and content-security headers. Client downloads use stable panel routes, strict HTTPS validation, repository-pinned GitHub release resolution, bounded caching, and administrator-configurable action links. Mail settings use optimistic revisions and authenticated encryption for SMTP credentials; the daily UTC+8 scheduler creates idempotent, bounded expiry and traffic jobs in a durable retry outbox. Tickets enforce ownership, one-open-ticket-per-user, bounded threads and inputs, role-specific reply rules, automatic stale closure, durable rate-limited reply notifications, and bounded delivery retries. The operations page reports scheduler and mail-worker heartbeats, database schema and combined mail-queue health, failed-delivery metadata, and an append-only mutation audit that deliberately excludes request bodies, verification codes, and credentials.

The administration interface uses accessible React portals with explicit overlay stacking and focus management for the server detail drawer and nested activation-schedule dialog. Theme management preserves the legacy upload, preview, configure, activate, and delete workflow using bounded declarative ZIP packages: raster assets and contrast-checked palettes are stored transactionally, while executable templates, scripts, custom HTML, remote backgrounds, path traversal, symlinks, and archive bombs are rejected. The active theme is shared by public, user, distributor, and administrator interfaces through immutable digest-addressed assets. The packaged Go binary can create online-consistent SQLite backup archives, verify their manifest, SHA-256, integrity, and foreign keys, and restore a verified archive to a new database path without overwriting the active database. A separate host-side lifecycle command composes those primitives into conservative local install, upgrade, failed-health recovery, and explicit rollback flows without exposing the Docker socket to the application container. Offline, source-fingerprinted migration commands import independently verified legacy slices from a standalone Xboard SQLite snapshot into a pristine Go database with a verified pre-import backup, atomic commit, per-domain checksums, and idempotent replay.

The repository is under active construction. It is intended for local and isolated test environments only and is not ready for production deployment.

Licensed under the [Apache License 2.0](LICENSE).

## Local container

The local Compose profile builds one non-root, read-only image containing the
Go API and the immutable frontend build. Store a temporary password and an
independent 256-bit settings-encryption key in the Git-ignored `.local`
directory; Compose mounts both as file-backed secrets rather than exposing
them in the application container environment.

```bash
mkdir -p .local
printf '%s' 'replace-with-a-local-password' > .local/bootstrap-password.txt
openssl rand -base64 32 > .local/settings-encryption-key.txt
chmod 600 .local/bootstrap-password.txt
chmod 600 .local/settings-encryption-key.txt
docker compose -f compose.local.yaml up --build --wait
```

Open `http://127.0.0.1:7080`. Captured local test mail is available only on
the loopback interface at `http://127.0.0.1:7082`; its SMTP port is not
published to the host. Runtime application data is stored in the
`xboard-go-data` named volume, while captured mail is intentionally ephemeral.
This profile explicitly permits cleartext SMTP only inside its isolated Docker
network and is not a production deployment definition.

The default `XBOARD_NODE_COORDINATION_MODE=local` is deliberately limited to
one API/WebSocket replica. Multi-replica tests must use `redis` mode: the
application then claims a 180-second machine-and-node lease, renews it every 60
seconds, closes replaced owners, and routes node synchronization through Redis
Pub/Sub. Redis claim, verification, or renewal failures fail closed before a
node report is written; Pub/Sub remains an acceleration path, while reconnect
full snapshots and the node's bounded HTTP pull provide eventual recovery.
The application refuses to silently fall back to local ownership.

`XBOARD_WEBSOCKET_ENABLED`, `XBOARD_WEBSOCKET_URL`,
`XBOARD_NODE_PUSH_INTERVAL`, and `XBOARD_NODE_PULL_INTERVAL` are deployment
capability and first-run defaults only. After the database has been
initialized, administrators own the pull/push intervals, WebSocket switch,
and optional public WebSocket URL from the **Node settings** page; process
restarts do not overwrite them. When the stored URL is empty, the advertised
endpoint is derived from the trusted `XBOARD_PANEL_URL`, never from an
untrusted request `Host` header. The stored switch cannot enable WebSocket
when the deployment capability is disabled.

The same page can generate, replace, or clear the legacy global server token
used by fixed Xboard-Node single-node mode. The plaintext is returned once;
the database stores only its SHA-256 digest and a short display prefix.
Machine credentials remain the recommended least-privilege mode. Rotating or
clearing the global token revokes legacy HTTP authentication immediately and
fences legacy WebSocket connections without revoking machine credentials.

The optional `coordination` Compose profile provides a pinned, private-network
development Redis with a file-backed password, no published port, and no persistent data. Generate
a temporary password, start Redis, and provide the matching URL only to the
application process. For shared environments, use `XBOARD_REDIS_URL_FILE`
instead of exposing credentials in an environment value; this local command is
only a reproducible test example.

```bash
openssl rand -hex 32 > .local/redis-password.txt
chmod 600 .local/redis-password.txt
XBOARD_GO_REDIS_PASSWORD_FILE=.local/redis-password.txt \
  docker compose -f compose.local.yaml --profile coordination up -d --wait redis
redis_password="$(cat .local/redis-password.txt)"
XBOARD_NODE_COORDINATION_MODE=redis \
XBOARD_REDIS_URL="redis://:${redis_password}@redis:6379/15" \
XBOARD_GO_REDIS_PASSWORD_FILE=.local/redis-password.txt \
  docker compose -f compose.local.yaml --profile coordination up --build --wait xboard-go
unset redis_password
```

`XBOARD_LEGACY_ADMIN_PATH` changes only the compatibility route segment under
`/api/v2`; it defaults to `admin` and accepts 1 to 64 ASCII letters, digits,
underscores, or hyphens. It is not an authorization factor: every route still
requires an authenticated administrator, and mutation audit records normalize
the configured segment instead of persisting it.

Keep the settings-encryption key for as long as the database contains an SMTP
or CAPTCHA credential, an invitation code, or a pending password-recovery or
registration-verification mail. The application intentionally refuses to start
if the key is missing or cannot authenticate a stored SMTP/CAPTCHA credential or
invitation code; protected email and invitation operations remain unavailable
without the key.

## Local image lifecycle

The host-side lifecycle tool only accepts an Xboard-Go image that already
exists in the local Docker image store and carries an immutable 40-character
revision label. It never pulls an image, selects `latest`, reads application
secrets, mounts the Docker socket into the application, or deletes an unknown
container or data volume.

For a fresh Compose project, create the file-backed secrets shown above, build
an exact-revision image, and install it:

```bash
revision="$(git rev-parse HEAD)"
docker build --build-arg "APP_REVISION=${revision}" -t "xboard-go:${revision}" .
go run ./cmd/xboard-lifecycle install \
  --project xboard-go-local \
  --compose-file compose.local.yaml \
  --image "xboard-go:${revision}"
```

An upgrade requires the active container to be healthy. Before stopping it,
the tool creates and verifies an online `.xbbackup`, records the exact current
and target image IDs plus the verified backup manifest, then starts the target
and verifies its health, revision, database DSN, and image ID. A failed target
health check automatically starts the previous image against a newly restored
database file; the failed upgraded database is retained for diagnosis rather
than overwritten. `status` remains usable when the container is stopped or
unhealthy so that the recorded state can be inspected.

```bash
go run ./cmd/xboard-lifecycle upgrade \
  --project xboard-go-local \
  --compose-file compose.local.yaml \
  --image xboard-go:next-revision

go run ./cmd/xboard-lifecycle status \
  --project xboard-go-local \
  --compose-file compose.local.yaml

go run ./cmd/xboard-lifecycle rollback \
  --project xboard-go-local \
  --compose-file compose.local.yaml
```

Explicit rollback restores the snapshot taken immediately before the last
successful upgrade. Writes accepted after that snapshot are not part of the
restored active database; they remain in the non-overwritten upgraded database
for diagnosis or deliberate reconciliation. A failed fresh install likewise
leaves its project container, volume, and audit state in place for inspection,
and a repeated install is refused until the operator explicitly removes those
project-scoped resources.

Lifecycle state is an append-only, Git-ignored journal under
`.local/lifecycle`; it contains image IDs, revisions, database paths, backup
paths and manifests, timestamps, and outcomes, but no credentials or business
rows. On POSIX hosts the tool restricts its state directories to `0700` and
files to `0600`; on Windows it retains the inherited ACL of the operator-owned
state directory. Use an operator-owned `--env-file` for non-default local ports
or Compose settings.

Each Compose project receives its own runtime image tag, so isolated local
lifecycle operations cannot retag another project's pending container image.
The tool is currently a local test workflow, not a production updater or a
remote image trust/signing system.

## Local database backup and recovery

The Compose profile mounts a separate `xboard-go-backups` volume. Creating a
backup is online: SQLite produces a transactionally consistent snapshot while
the local application remains available. Every `.xbbackup` archive contains a
versioned manifest and a compact SQLite snapshot; both create and verify run a
full SQLite integrity and foreign-key check without loading the database into
memory.

```bash
docker compose -f compose.local.yaml run --rm --no-deps maintenance backup create
docker compose -f compose.local.yaml run --rm --no-deps maintenance backup verify \
  --input /var/lib/xboard-backups/xboard-YYYYMMDDTHHMMSSZ.xbbackup
```

Recovery deliberately writes a new database file and refuses to overwrite an
existing path. Stop the application, restore into a new file, then explicitly
select that file. Returning to the original DSN is the rollback path.

```bash
docker compose -f compose.local.yaml stop xboard-go
docker compose -f compose.local.yaml run --rm --no-deps maintenance backup restore \
  --input /var/lib/xboard-backups/xboard-YYYYMMDDTHHMMSSZ.xbbackup \
  --output /var/lib/xboard/restored.db \
  --attachment-output /var/lib/xboard/restored-attachments
XBOARD_DATABASE_DSN=file:/var/lib/xboard/restored.db \
XBOARD_ATTACHMENT_ROOT=/var/lib/xboard/restored-attachments \
  docker compose -f compose.local.yaml up -d --wait xboard-go
```

The database archive does not contain `XBOARD_SETTINGS_ENCRYPTION_KEY`; retain
that secret independently for as long as encrypted settings or pending tokens
exist. Copy verified archives to independently protected storage when testing
a real disaster-recovery plan. These commands are currently intended only for
local and isolated test environments.

## Local bounded maintenance

The running scheduler removes expired authentication challenges and abuse
limits and closes administrator-answered tickets after the verified 24-hour
window. The same state-machine cleanup can be run explicitly against the local
database; each category is independently bounded to at most 1,000 rows and the
command returns machine-readable counts. It validates the current Xboard schema
before the first write and can be repeated until every count is zero.

```bash
docker compose -f compose.local.yaml run --rm --no-deps maintenance \
  maintenance cleanup-expired --limit 1000
```

This operation deliberately does not invent retention rules for user records,
attachments, audit history, traffic statistics, or other business data.

## Local legacy migration

The first legacy migration slice covers only five public site-identity settings
(`app_name`, `app_description`, `app_url`, `tos_url`, and `logo`), notices, and
the `client_catalog_links` JSON setting. It deliberately does not read or copy
passwords, tokens, SMTP/CAPTCHA credentials, users, plans, orders, payments,
nodes, plugins, or other settings.

The second independently recorded slice covers server permission groups and
routing rules while preserving their IDs and timestamps. It validates each
match array and action against the current bounded runtime contract. User,
plan, and node relationships are handled by later independently recorded
slices; the preserved IDs make those migrations composable.

The third slice covers knowledge articles while preserving IDs, content,
visibility, ordering values, and timestamps. A legacy nullable sort value maps
to the current explicit unsorted value `0`, and imported articles start at
optimistic revision `1`. Because knowledge attachments are not implemented yet,
this slice fails closed if the source has attachment rows, upload sessions, or
attachment URI references instead of creating broken articles.

The plan slice preserves plan IDs, group relationships, entitlements, exact
integer-cent prices, visibility, sales and renewal state, capacity, tags,
ordering and timestamps. The human-account slice then replaces the single
unused bootstrap administrator and preserves IDs, normalized email identities,
PHP bcrypt password hashes, administrator and banned state, UUIDs, subscription
tokens, group, plan and invitation relationships, traffic and limits, reset
schedule state, timestamps, expiry state, account balance, account discount,
and invitation-commission type, rate, and balance. A successful first password
login upgrades bcrypt to Argon2id in the same transaction that records the
login time and creates the session or bearer token. Unsupported staff,
distributor, reminder, or audit state is rejected instead of being silently
discarded.

The node slice imports the operational server domain after groups and routes:
machines, hashed credentials and enrollments, recent load history, nodes,
protocol definitions, group and route memberships, activation schedules, and
aggregated node traffic statistics. A legacy plaintext machine-token fallback
is converted to a SHA-256 credential while the source snapshot is read and is
never emitted in the result. Protocol definitions remain separate from the
compiled runtime JSON so node pulls stay on the existing hot path. Time-window
traffic rates retain Xboard's inclusive `Asia/Shanghai` `HH:mm` behavior. The
reader fails closed if in-flight report receipts have not been drained.

Node-agent settings are a separate slice because the legacy global server
token is configuration, not a machine credential. The reader accepts only the
six fixed `v2_settings` keys, hashes the plaintext token in memory, validates
bounded intervals and WebSocket values, and never emits the token or digest in
its JSON result or logs.

Never bind-mount a running Xboard database file directly. First use SQLite's
online backup API to create a standalone snapshot; a filesystem copy of a WAL
database is not a valid migration input. The migration reader refuses
symlinks, adjacent `-wal`/`-shm` files, views that masquerade as required
tables, changing inputs, unsafe client URLs, and unbounded content.

Stop the Go application, bind the standalone snapshot read-only into the
maintenance container, and select a new rollback archive path:

```bash
docker compose -f compose.local.yaml stop xboard-go
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-content \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-content.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

Before the first write, the command creates and verifies the rollback archive.
Settings, notices, client links, and the source fingerprint ledger then commit
in one SQLite transaction. Re-running the same snapshot returns the recorded
result without duplicating data and verifies that the original rollback
archive still matches its recorded digest. A different snapshot or a target
whose site identity, notices, or client catalog has already been edited is
rejected instead of being merged implicitly.

With the application still stopped, the same standalone source can then be
used for groups and routes. This creates a second rollback point, so restoring
it removes only this slice while retaining an earlier successful content
migration:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-groups-routes \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-groups-routes.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

This slice requires empty target group and route tables. It rejects lossy
normalization, malformed JSON, unsupported actions, invalid timestamps,
oversized data, a different snapshot after completion, or any pre-existing
group/route data instead of attempting an ambiguous merge.

Still offline, knowledge can be imported with its own rollback point:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-knowledge \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-knowledge.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The knowledge target must be empty. Unsupported languages, attachment data,
lossy text normalization, invalid visibility/sort/timestamps, oversized data,
and conflicting snapshots are rejected. Restoring this third backup removes
only the knowledge slice while retaining the first two completed slices.

Plans can then be imported into an empty plan target. Referenced groups must
already exist. The reader converts legacy major-unit JSON prices to integer
cents without floating-point rounding and rejects unknown periods, fractional
cents, malformed JSON, invalid state, lossy rows, or a conflicting prior import:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-plans \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-plans.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

Human accounts can then be imported with an explicit acknowledgement that the
only pristine bootstrap administrator will be replaced. Referenced groups and
plans must already have been imported, and any foreign-key dependency on the
bootstrap administrator causes the operation to fail before deletion:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-human-users \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-human-users.xbbackup \
  --confirm-offline \
  --replace-bootstrap-admin
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The source must contain at least one active administrator. Account identifiers,
emails, UUIDs and subscription tokens must be unique, invitation references
must resolve within the snapshot, and password hashes must be bounded legacy
bcrypt values. Repeating the same source verifies the recorded rollback archive
and returns the existing result without rewriting users.

Existing legacy Bearer sessions can then be imported from the same standalone
snapshot. The command preserves token IDs, user ownership, SHA-256 token
digests, device names, last-use and expiry times, and therefore does not need
or reveal any plaintext credential:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-access-tokens \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-access-tokens.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The human-account slice from the identical source must already be recorded and
the target access-token table must be empty. Only Xboard user tokens with the
existing wildcard ability contract are accepted; unknown tokenable types,
restricted abilities, malformed hashes, missing users, oversized data, or a
different prior snapshot fail before commit. A canonical checksum verifies the
atomic import and repeating the same source verifies the recorded rollback
archive without rewriting credentials.

Ticket history can then be imported after the human-account slice from the
same standalone snapshot. The target tickets, messages, reply-mail outbox, and
reply throttle must all be empty. Ticket and message identifiers, ownership,
subjects, levels, open/closed and waiting/answered state, final authors,
message bodies, and timestamps are preserved exactly. Historical notification
mail is deliberately not replayed:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-tickets \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-tickets.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The reader accepts at most one million tickets, ten million messages, and two
GiB of ticket text. Messages are streamed instead of retained in memory. The
source is re-verified before the target transaction commits, canonical
checksums independently verify both tables, and SQLite identifier sequences are
advanced to the imported maxima. Repeating the same source is idempotent and
also detects later target drift.

Coupons can be imported after plans and before orders. The slice preserves
coupon IDs, exact codes and names, fixed-cent or percentage values, visibility,
remaining and per-user limits, plan and period restrictions, timestamps, and
the legacy global coupon-system setting. Duplicate codes, invalid or lossy
values, malformed restrictions, missing plans, and ambiguous settings fail
before any target write:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-coupons \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-coupons.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The target coupon table must be empty and its global setting must still have
the default value. A canonical checksum independently verifies both coupons
and the setting after the atomic import. Referenced coupon IDs must exist before
the order slice is imported.

Gift cards can be imported after human accounts and plans. The slice preserves
template, code, and usage IDs; normalized reward and eligibility rules; sharing
limits; batch identity; code state; exact audit timestamps; applied rewards;
legacy user-level and plan context; metadata; and exact multiplier basis points.
Legacy code states are translated so a shared code marked "used" by Xboard
remains redeemable while it still has remaining uses:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-gift-cards \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-gift-cards.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

All three target gift-card tables must be empty. Missing users, administrators,
plans, templates, or codes; malformed or unknown JSON; lossy decimal rates;
duplicate identifiers; oversized data; and conflicting snapshots are rejected
before commit. Three independent canonical checksums verify the atomic import.

Payment methods can be imported before orders. The command preserves provider
IDs, callback UUIDs, display metadata, effective ordering, enabled state,
integer-cent fixed fees, percentage fees to an exact hundredth of one percent,
and timestamps. Provider configuration is validated against the six supported
gateways and encrypted before it reaches the target database; the settings key
is required and is never included in the rollback archive or command result.
Legacy non-HTTPS remote gateway or notification endpoints, unknown fields,
lossy percentage values, and malformed configurations are rejected before the
backup or any target write:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-payments \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-payments.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The target payment table must be empty. The import verifies an encrypted
source/target checksum, records a separate checksum of the normalized plaintext
source without exposing its values, and is atomic and idempotent. Existing
orders with payment references must all resolve after the import.

Orders can then be imported after their referenced users, plans, coupons, and payments. This slice
preserves order IDs, 25-digit trades, 32-character callback references, periods,
types, statuses, integer-cent financial fields, balance and surplus deductions,
coupon and payment references, referral attribution and commission state,
entitlement-boundary timestamps, and distributor metadata. Coupon, payment,
commission-settlement, and distributor domain tables are independently migrated;
commission-settlement and distributor workflows remain future slices.
Historical completed orders remain historical records; the
import deliberately does not fabricate entitlement events or replay account
changes that were already applied by Xboard.

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-orders \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-orders.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The target order and entitlement-event tables must be empty. The reader bounds
row count and encoded data, validates financial reconstruction, references,
status combinations, timestamps and identifiers, and compares an independent
canonical checksum after the atomic import. Repeating the same source is
idempotent and re-verifies the exact recorded rollback archive.

With the legacy application and its report workers still stopped, the node
domain can then be imported. Groups and routes referenced by a node must already
exist, while the target machine and node tables must be empty:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-nodes \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-nodes.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The import preserves machine and node IDs and verifies four independent target
checksums (machines/security state, nodes/definitions, schedules, and traffic).
Existing machine credentials remain valid without copying a plaintext token.
Transient report receipts are deliberately not approximated: drain them before
creating the standalone source snapshot or the command refuses to run.

The legacy node-agent settings can then be imported into a pristine settings
row using an independent rollback point. This operation is idempotent for the
same source snapshot and refuses a different source or administrator-edited
target:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-node-agent-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-node-agent-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

Telegram settings use a separate, credential-safe offline import. The command
requires `XBOARD_SETTINGS_ENCRYPTION_KEY`, encrypts the legacy bot token before
writing it, excludes credential material from JSON evidence, and creates and
verifies an independent rollback archive:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-telegram-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-telegram-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The imported bot remains enabled but its old query-string webhook credential
is deliberately not retained. An administrator must use **Telegram 设置 → 一键设置
Webhook** once after startup; Xboard-Go then provisions Telegram's official
`secret_token` header authentication and caches the verified bot username.
Schema migration also establishes one-to-one Telegram user identities. If a
legacy target contains duplicate non-null `users.telegram_id` values, migration
stops transactionally and reports the conflicting ID so the duplicate can be
resolved before retrying.

Legacy registration, login-limit, invitation, CAPTCHA, and ticket-reply policy
settings use another independent offline slice. The importer reads only the 24
fixed `v2_settings` keys, requires pristine target policy fields, and validates
all bounds before writing. Legacy CAPTCHA secrets are encrypted with the
existing purpose-isolated settings cipher and are excluded from JSON evidence;
`XBOARD_SETTINGS_ENCRYPTION_KEY` is therefore required when any CAPTCHA secret
is configured:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-site-policy-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-site-policy-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

An enabled CAPTCHA provider must have both its site key and secret; incomplete
or unsafe legacy policy values stop the import instead of being silently
disabled or normalized to weaker security.

Legacy SMTP and reminder settings use the independent `mail-settings-v1`
slice. The importer reads only the seven fixed mail keys, derives enablement
from the old `email_host` behavior, maps old `tls` to STARTTLS and `ssl` to
implicit TLS, and encrypts the SMTP password with the existing SMTP-specific
settings cipher. Incomplete credentials, orphaned fields, or cleartext SMTP
stop the import instead of weakening or silently changing mail behavior:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-mail-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-mail-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

`XBOARD_SETTINGS_ENCRYPTION_KEY` is required when the legacy snapshot contains
an SMTP password. Password material is excluded from command output and the
migration ledger.

Legacy plan-change, surplus-credit, order-event, and new-user reminder defaults
use the independent `subscription-policy-settings-v1` slice. It reads only the
seven fixed policy keys and deliberately leaves `reset_traffic_method` to the
existing subscription-config migration, so the two slices compose without
overwriting each other. Unknown events and invalid booleans fail closed:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-subscription-policy-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-subscription-policy-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

Legacy invitation and commission behavior uses the independent
`commission-policy-settings-v1` slice. It reads only the eight global
commission policy keys and composes with the existing site-policy,
configuration-compatibility, and commission-ledger migrations. The old
administration UI treated missing distribution levels as `0/0/0`, while a
pristine Xboard-Go database stores `100/0/0`; the migration recognizes the
pristine Go state and then preserves the old effective source defaults:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-commission-policy-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-commission-policy-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

After plans have been imported, migrate the legacy registration-trial plan and
duration while the target remains offline:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-registration-trial-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-registration-trial-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The importer rejects non-integer, non-positive, or unbounded enabled trial
durations. A legacy setting that references a missing plan is explicitly
reported and normalized to a disabled trial instead of creating a dangling
reference. Repeating the same source verifies and reuses the recorded rollback
archive.

The five legacy system mail templates can be imported independently from
`v2_mail_templates`. The importer accepts only the fixed template catalog,
validates placeholders and size limits before writing, requires a pristine
target, and never includes template bodies in its JSON report:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-mail-templates \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-mail-templates.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The six legacy client application version and download settings can be
imported independently from the fixed `v2_settings` keys. The importer trims
legacy whitespace, accepts blank values, requires download links to be
absolute HTTPS URLs, refuses an administrator-edited target, and verifies a
dedicated rollback archive before writing:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-client-app-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-client-app-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The legacy Xboard theme choice and safe built-in-theme configuration use a
separate offline slice. In addition to `theme_xboard`, it reads the historical
top-level `frontend_theme_color` and `frontend_background_url` compatibility
values. Empty or missing top-level values do not replace `theme_xboard`;
explicit values must agree. The slice accepts the fixed `Xboard` theme and the
verified `default`, `blue`, `black`, or `darkblue` palette values. It
deliberately stops instead of silently importing an executable custom theme,
non-empty `custom_html`, a remote background URL, conflicting theme/color
keys, or unknown configuration fields:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-theme-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-theme-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The four legacy configuration-compatibility values for commission withdrawal
limits/methods and sidebar/header light/dark styles use a further independent
slice. It reads only those fixed keys, stores withdrawal limits as exact
hundredths (accepting at most two decimal places), validates bounded string
array values, requires pristine target fields, and records no secret values:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-configuration-compat-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-configuration-compat-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The legacy `currency` and `currency_symbol` site settings use a separate
migration slice so previously completed content migrations keep their original
checksum identity. The importer accepts only those two fixed keys, normalizes
the currency code to three uppercase ASCII letters, validates the UTF-8 symbol,
requires pristine currency fields, and records a verified rollback archive:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-currency-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-currency-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The legacy `force_https` and `subscribe_url` values also use an independent
slice, leaving the frozen `content-settings-v1` checksum and ledger identity
unchanged. External subscription origins must use HTTPS; loopback HTTP remains
available for isolated local testing. The importer rejects credentials, query
strings, fragments, duplicate source rows, and non-pristine target fields:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-public-origin-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-public-origin-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The legacy `safe_mode_enable`, `secure_path`, and compatibility fallback
`frontend_admin_path` values use another independent slice. The importer reads
only those fixed keys, follows the old `secure_path` then `frontend_admin_path`
precedence, requires an explicit secure path of 8–64 ASCII letters, numbers,
underscores, or hyphens, rejects reserved
API namespaces, and only replaces a pristine empty or deployment-default
target path. Enabling safe mode also requires the site URL to have been
migrated first. If the old database has no `secure_path` row because Xboard was
also missing `frontend_admin_path` and was using the APP_KEY-derived default,
pass that observed effective value with
`--source-effective-secure-path`; the importer never guesses it or reads the
old APP_KEY:

```bash
docker compose -f compose.local.yaml run --rm --no-deps \
  -v /absolute/path/legacy-snapshot.db:/var/lib/xboard-import/legacy.db:ro \
  maintenance migration import-legacy-safe-access-settings \
  --source /var/lib/xboard-import/legacy.db \
  --backup-output /var/lib/xboard-backups/pre-legacy-safe-access-settings.xbbackup \
  --confirm-offline
docker compose -f compose.local.yaml up -d --wait xboard-go
```

The JSON result contains paths, sizes, schema versions, row counts, and SHA-256
checksums but no setting values, URLs, notice or knowledge bodies, article
titles, email addresses, password hashes, subscription tokens, or credentials.
This remains a local/isolated-test workflow; commission withdrawal settlement
and other remaining legacy domains still require separate mappings and
migration evidence.
