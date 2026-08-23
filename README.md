# Xboard-Go

Xboard-Go is an Apache-2.0 clean-room reimplementation of the Xboard control plane with a Go backend and a maintainable React/TypeScript administration interface.

The project preserves verified business behavior while replacing inaccessible legacy frontend code and progressively rebuilding the backend. Correctness, security, data compatibility, measurable performance, and operational reliability are first-class constraints.

The implemented vertical slices cover administrator authentication, server-machine and node association management, one-time hashed machine enrollment credentials, load reporting, revision-safe daily activation schedules, and the Xboard-Node runtime control-plane contract. Node configuration and user snapshots are available over authenticated HTTP and WebSocket transports; traffic reports are transactionally persisted with durable idempotency, node-group authorization, bounded inputs, and cross-node device-state synchronization.

The administration interface uses accessible React portals with explicit overlay stacking and focus management for the server detail drawer and nested activation-schedule dialog.

The repository is under active construction. It is intended for local and isolated test environments only and is not ready for production deployment.

Licensed under the [Apache License 2.0](LICENSE).
