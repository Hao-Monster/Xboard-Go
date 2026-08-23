# Xboard-Go

Xboard-Go is an Apache-2.0 clean-room reimplementation of the Xboard control plane with a Go backend and a maintainable React/TypeScript administration interface.

The project preserves verified business behavior while replacing inaccessible legacy frontend code and progressively rebuilding the backend. Correctness, security, data compatibility, measurable performance, and operational reliability are first-class constraints.

The first vertical slice covers administrator authentication, server-machine and node association management, one-time hashed machine enrollment credentials, load reporting, and revision-safe daily activation schedules. The detail drawer and nested schedule dialog are implemented as accessible React portals with explicit overlay stacking and focus management.

The repository is under active construction. It is intended for local and isolated test environments only and is not ready for production deployment.

Licensed under the [Apache License 2.0](LICENSE).
