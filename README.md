# Xboard-Go

Xboard-Go is an Apache-2.0 clean-room reimplementation of the Xboard control plane with a Go backend and a maintainable React/TypeScript administration interface.

The project preserves verified business behavior while replacing inaccessible legacy frontend code and progressively rebuilding the backend. Correctness, security, data compatibility, measurable performance, and operational reliability are first-class constraints.

The implemented vertical slices cover administrator authentication and account-session security, a cursor-paginated user directory and access-state editor, server-machine and node association management, one-time hashed machine enrollment credentials, load reporting, revision-safe daily activation schedules, server permission groups, panel routing rules, notices, a validated cross-platform client download catalog, and the Xboard-Node runtime control-plane contract. Account holders can list and revoke their active sessions; password and other security-sensitive account changes atomically revoke every session. Node configuration, selected routing rules, and user snapshots are available over authenticated HTTP and WebSocket transports; user access changes use bounded per-user deltas while traffic reports are transactionally persisted with durable idempotency, node-group authorization, bounded inputs, and cross-node device-state synchronization. Referenced groups and routes are protected by transactional integrity checks so administrative changes cannot leave dangling node configuration. Client downloads use stable panel routes, strict HTTPS validation, repository-pinned GitHub release resolution, bounded caching, and administrator-configurable action links.

The administration interface uses accessible React portals with explicit overlay stacking and focus management for the server detail drawer and nested activation-schedule dialog.

The repository is under active construction. It is intended for local and isolated test environments only and is not ready for production deployment.

Licensed under the [Apache License 2.0](LICENSE).

## Local container

The local Compose profile builds one non-root, read-only image containing the
Go API and the immutable frontend build. Store a temporary password in the
Git-ignored `.local/bootstrap-password.txt`; Compose mounts it as a file-backed
secret rather than exposing it in the application container environment.

```bash
mkdir -p .local
printf '%s' 'replace-with-a-local-password' > .local/bootstrap-password.txt
chmod 600 .local/bootstrap-password.txt
docker compose -f compose.local.yaml up --build --wait
```

Open `http://127.0.0.1:7080`. Runtime data is stored in the
`xboard-go-data` named volume. This profile is for isolated development and
compatibility testing; it is not a production deployment definition.
