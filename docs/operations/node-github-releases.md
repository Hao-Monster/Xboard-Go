# Direct GitHub Node downloads

Work item: NDL-001. Requirement: MACH-002. Issue: #121. Milestone: M3.

The panel generates one command for the operator to copy to a Linux node. With
`XBOARD_NODE_RELEASE_SOURCE=github`, it downloads the fixed installer and
SHA256SUMS from the public Go repository's `node-vX.Y.Z` release. The installer
downloads only the node's architecture. Nodes need no gh, GitHub login or token.
Source code stays private. Enrollment happens against the panel after both
binaries have been downloaded and verified. Proxy kernels, accounting and
machine credential semantics are unchanged.

## Development tasks

1. Prepare public release files through the existing verified packaging process.
2. Add public downloads, HTTP/1.1 and bounded retries to installer and xbctl.
3. Add the panel source setting, compatibility tests and staged activation.

No R2, CDN, mirror failover, signing system or replacement installer engine is
introduced. GitHub connectivity is required for public mode.

## Publish before activating

1. Merge and release the changed Node installer/CLI through Node CI using a new
   version and exact source SHA. Never replace v1.14.4: its public release only
   has a bundle, and cannot support the new direct-download command.
2. Configure `NODE_RELEASE_READ_TOKEN` as a CI-only secret with read access to
   the private Node repository. Public publication uses `github.token`.
3. Run `node-public-release.yml` from the sole Go development branch with the
   version and exact private source SHA. It verifies the private release and
   GitHub asset digests, packages original bytes, uploads all public assets as a
   draft, publishes, then downloads every file without credentials and compares
   bytes/checksums. It refuses to replace any existing release. After a failure,
   inspect any partially completed release; do not overwrite or delete it.
4. Only after successful verification, set `XBOARD_NODE_RELEASE=<new-version>`
   and `XBOARD_NODE_RELEASE_SOURCE=github` through authorized CI/CD deployment.
   `deploy/compose.yaml` passes both settings. Other Compose files must explicitly
   pass these settings into the app container when activating this mode.
5. Copy a fresh panel command and perform actual node install/upgrade acceptance.
   Successful publication does not prove systemd startup or machine registration.

The default source remains `panel` during migration, preventing a code deployment
from generating commands for unpublished assets. Invalid source names fail
configuration loading. Existing installations are not automatically upgraded.
The public repository's immutable-release setting is independent; this workflow
does not change repository settings or claim platform-enforced immutability for
ordinary releases. All remote publication/deployment needs explicit authorization.

## Public layout and trust

`https://github.com/Hao-Monster/Xboard-Go/releases/download/node-vX.Y.Z/` contains
install.sh, SHA256SUMS, four existing architecture binaries, release.json and
node-release.tar.gz. Static release.json is for CLI metadata without GitHub API
auth/rate limits; asset URLs must match that exact public version directory.
The bundle is retained for existing panel image consumers.

SHA256 verification is mandatory before replacement. Bootstrap verifies the
installer using checksums fetched over HTTPS. Its trust is GitHub/TLS and the
trusted panel command, not independent cryptographic publisher authentication.

## Operator behavior

With the new CLI: `sudo xbctl upgrade --version <published-version>`.
For an old v1.14.3/v1.14.4 CLI, use the new verified installer's `upgrade` action;
do not run a fresh enrollment command over an existing installation.

Downloads use HTTP/1.1. Installer GETs retry up to three times with 1s/2s waits;
binary attempts are capped at 30 minutes each. CLI binary retries share a
30-minute deadline, and metadata retries share 30 seconds. Small bootstrap
downloads use curl's bounded retry policy. Failures remain visible. There is no
resumable download, automatic mirror, silent version downgrade or enrollment
retry added by this change.

## Compatibility and rollback

Keep `--release-api-base https://PANEL/api/v2/node/releases` for legacy panel
downloads. Private maintainer downloads require the explicit private GitHub API
base and temporary XBOARD_NODE_RELEASE_TOKEN. Public mode never sends that token.

Existing panel endpoints and bundled release root remain unchanged. Reverting
the source to panel must also restore a version actually present in that image's
release root. Changing source alone with an unbundled new version would fail.
This setting rollback does not downgrade running nodes. Binary rollback keeps
the existing installer/CLI behavior and still needs isolated-node acceptance.
