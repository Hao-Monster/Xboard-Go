# REALITY draft credential generation

Scope: NODE-001 / VER-003, issue #121, M3. Full NODE-001 business acceptance remains pending.

The VLESS and Trojan REALITY forms expose two independent actions:

- Generate key pair fills private_key and public_key together. The authenticated administrator POST /api/v1/admin/nodes/reality-key endpoint uses Go crypto/ecdh X25519 and crypto/rand. Both keys are 32-byte, unpadded URL-safe Base64 strings, matching the legacy PHP project's admin bundle (public/assets/admin/assets/index-hwid-335565df7b.js, O4t/R4t helpers).
- Generate Short ID fills short_id using browser crypto.getRandomValues. It produces 8 random bytes encoded as 16 lowercase hexadecimal characters. The legacy M4t helper varies the length from 1 to 8 bytes; using the maximum supported length preserves the wire format.

Generation only changes the current draft. It does not create or update a node until the administrator submits the form. Manual input remains available. Failed generation leaves existing fields intact. A key request completed after a REALITY edit, protocol/security switch, or dialog close is discarded to avoid replacing newer work.

The key endpoint inherits administrator session and CSRF protection and sends Cache-Control: no-store. Private material is not persisted or logged by generation. Short ID generation requires browser secure-context cryptography, with failures displayed in the form.

Targeted checks:

```text
go test ./internal/httpapi -run '^TestAdminNode' -count=1
go vet ./internal/httpapi
pnpm --dir web exec vitest run src/features/nodes/NodeManagementPage.test.tsx src/features/servers/ServerManagementPage.test.tsx
pnpm --dir web run typecheck
pnpm --dir web run lint
pnpm --dir web run build
go run ./cmd/projectctl check
```

Deployment uses the existing internal-test image and deployment workflows. No database migration or Node binary change is required. Existing node key material is not rotated by deploying this UI.
