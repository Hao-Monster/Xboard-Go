# Panel-managed Node releases

This page describes the retained legacy panel provider. For new direct GitHub
downloads, publication order and activation, see [Direct GitHub Node downloads](node-github-releases.md).

The panel generates installation commands that download a fixed Node version
from `/api/v2/node/releases`. Node servers need no `gh`, GitHub account, or
GitHub token. Enrollment remains a short-lived, machine-bound, one-use code.

## Compatibility and scope

- Existing v1.14.3 nodes retain their enrollment and communication protocol.
- v1.14.4 adds the panel release provider to installation and `xbctl upgrade`.
- This change does not change proxy kernels, traffic accounting, or node data.
- A running panel image carries its approved Node release under
  `/usr/share/xboard/node-releases`. This path is separate from uploads and
  databases; the production filesystem is read-only.
- `XBOARD_NODE_RELEASE` selects the version in generated commands;
  `XBOARD_NODE_RELEASE_ROOT` selects the immutable distribution directory.
  Source-only local runs must configure a populated directory to serve releases.
  There is no fallback to a GitHub-login installation command.

## Maintainer release process

1. Review and merge Node changes through the Node repository PR and its CI.
2. Publish a new fixed Node version using its tested release workflow. Never
   replace the old version's files. The Node source repository remains private.
3. Run `python deploy/package-node-release.py --version v1.14.4 --output PATH`
   in a new local directory. This maintainer-only operation uses `gh` to verify
   the immutable Node release and both GitHub asset digests and `SHA256SUMS`.
   It packages only the installer, checksums, four binaries and a manifest.
4. Publish that verified archive in the Go repository's `node-v1.14.4` release.
   Pin its SHA256 and URL in the Dockerfile through the sole development branch.
   No GitHub token is copied into the image or configured on nodes.
5. The image workflow runs the actual bundled installer download functions
   against the new panel and verifies all four binaries without GitHub tokens.
   It does not install a node or consume an enrollment code.
6. Merge the panel PR and select the exact tested image SHA in the protected
   production-internal workflow's `update` mode. It backs up and verifies the
   database, preserves the existing volumes/configuration/proxy, and restores
   the previous Xboard image if health or release probes fail.

## Node operator

For a new node, generate a fresh enrollment command from the updated panel and
run it on the intended node server. Previously copied commands still contain
their old version and download source; copy a new command after the panel update.

On nodes already running the new CLI, an explicit fixed-version upgrade is:

```bash
sudo xbctl upgrade --version v1.14.4 --release-api-base https://fast.hjy.ca:8443/api/v2/node/releases
```

The provider is explicit, not persisted as an implicit default. An old v1.14.3
CLI does not understand this option; use the new verified installer's `upgrade`
action with the same version/provider arguments when upgrading that CLI.
Do not run the new-install enrollment command over an existing installation.
