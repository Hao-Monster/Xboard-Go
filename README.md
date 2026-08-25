# Xboard-Go

Xboard-Go is an Apache-2.0 clean-room reimplementation of the Xboard control plane with a Go backend and a maintainable React/TypeScript administration interface.

The project preserves verified business behavior while replacing inaccessible legacy frontend code and progressively rebuilding the backend. Correctness, security, data compatibility, measurable performance, and operational reliability are first-class constraints.

The implemented vertical slices cover administrator authentication and account-session security, atomic public email/password registration with administrator-controlled registration policies and optional email verification, invitation-code registration and referral relationship tracking, email-code password recovery, a cursor-paginated user directory and access-state editor, server-machine and node association management, one-time hashed machine enrollment credentials, load reporting, revision-safe daily activation schedules, server permission groups, panel routing rules, notices, multilingual knowledge management and public sharing, a validated cross-platform client download catalog, user and administrator ticket lifecycles, system runtime status and administrator audit, and the Xboard-Node runtime control-plane contract. Password authentication has administrator-configurable persistent error thresholds and expiry windows, normalized identity keys that cannot be bypassed with case or whitespace changes, account-enumeration-resistant failures, and an independent bounded source-IP safeguard. Account holders can list and revoke their active sessions; password and other security-sensitive account changes atomically revoke every session. Registration applies bounded Argon2id concurrency, per-address resource limits, strict origin checks for browsers, normalized email-domain whitelists, scoped Gmail-alias protection, a persistent successful-registration IP quota, purpose-isolated six-digit verification codes with durable cooldown, lockout, encrypted outbox delivery and transactional one-time consumption, and CSPRNG invitation codes protected by keyed digests and authenticated encryption at rest. Invitation relationships and single-use code consumption commit atomically with the user and initial session; reusable codes preserve the verified legacy behavior. Password recovery uses cryptographically generated six-digit codes, persistent cooldown and lockout state, account-enumeration-resistant responses, authenticated encryption at rest, a durable mail outbox, one-time transactional consumption, and bounded password hashing; a successful reset revokes every existing session. Policy checks are repeated transactionally so account, initial session, verification-code consumption, invitation consumption and relationship creation, and IP counter changes cannot bypass concurrent configuration changes or commit partially; expired IP, login-failure, and email-challenge state is removed by bounded background maintenance. Node configuration, selected routing rules, and user snapshots are available over authenticated HTTP and WebSocket transports; user access changes use bounded per-user deltas while traffic reports are transactionally persisted with durable idempotency, node-group authorization, bounded inputs, and cross-node device-state synchronization. Referenced groups and routes are protected by transactional integrity checks so administrative changes cannot leave dangling node configuration. Knowledge articles support categories, languages, optimistic editing, visibility and ordering, subscriber-only regions, user-specific placeholders, and server-rendered public pages with strict HTML sanitization and content-security headers. Client downloads use stable panel routes, strict HTTPS validation, repository-pinned GitHub release resolution, bounded caching, and administrator-configurable action links. Tickets enforce ownership, one-open-ticket-per-user, bounded threads and inputs, role-specific reply rules, automatic stale closure, durable rate-limited reply notifications, encrypted SMTP credentials, and bounded delivery retries. The operations page reports scheduler and mail-worker heartbeats, database schema and combined mail-queue health, failed-delivery metadata, and an append-only mutation audit that deliberately excludes request bodies, verification codes, and credentials.

The administration interface uses accessible React portals with explicit overlay stacking and focus management for the server detail drawer and nested activation-schedule dialog.

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

Keep the settings-encryption key for as long as the database contains an SMTP
credential, an invitation code, or a pending password-recovery or
registration-verification mail. The application intentionally refuses to start
if the key is missing or cannot authenticate a stored SMTP credential or
invitation code; protected email and invitation operations remain unavailable
without the key.
