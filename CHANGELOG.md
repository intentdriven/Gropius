# Changelog

All notable changes to Gropius are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A release is a dated heading rolled out of the `[Unreleased]` section in a
reviewed pull request; on merge, the release workflow tags that commit and
publishes the build. Releases before this file existed are listed with their
GitHub release notes.

## [Unreleased]

### Changed

- The server's request log records the method, path, status and duration of a
  request, and never the client's network address. A handler panic is reported
  the same way, so no line the server writes identifies a caller.

### Removed

- **Breaking:** support for every macOS below 26. Gropius and the GropiusChat
  client both require macOS 26; the one-line installer refuses an older Mac
  before it downloads anything, and the bundles declare the same minimum, so
  macOS refuses to launch them there.

## [0.1.2] - 2026-09-06

### Security

Shared-cache mode (`make install-shared`) treats every other local account as
untrusted. Every state file in the shared root is now read only when it is a
regular file, without following symlinks, and with a size cap, so a planted
FIFO or link can no longer hang startup or a model launch. The process-group
ledger is trusted only when this account owns it. Removing a model deletes the
directory derived from its id, never a path read from the registry, and never
through a symlinked parent. The layout directories, download destinations and
per-model log files are created without following planted symlinks. The
shared root is adopted only when an administrator created it. The Python
runtime is refused unless it is a regular file, not writable by other
accounts, and owned by the account running it or by the administrator. With an
API key set, a loopback client must address `localhost` or `127.0.0.1` or send
the key, closing a DNS-rebinding read of the model list.

### Fixed

- A model id that differs from a known one only by letter case reuses that
  model instead of creating a second entry over the same directory, so
  removing either no longer deletes the other's weights.
- A repository whose files differ only by letter case is refused up front,
  instead of two downloads racing over one file.
- A download whose weight index names a shard the repository does not contain
  is marked failed instead of advertised as ready and failing on every load.
- A registry entry with an invalid id is skipped on load rather than served as
  a model that cannot be removed.

### Changed

- The installer verifies a download against the checksums file published on
  the same release, and every release asset carries a GitHub build-provenance
  attestation; minisign is no longer required.
- A symlinked `config.json` is refused and the server starts locked to this
  Mac; a hand-made shared cache directory is ignored. Both are explained in
  the getting-started guide.
- Releases are cut from this file: the newest dated heading is tagged and
  published on merge.

## [0.1.1] - 2026-07-29

See the [v0.1.1 release](https://github.com/intentdriven/Gropius/releases/tag/v0.1.1).

## [0.1.0] - 2026-07-29

See the [v0.1.0 release](https://github.com/intentdriven/Gropius/releases/tag/v0.1.0).

[Unreleased]: https://github.com/intentdriven/Gropius/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/intentdriven/Gropius/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/intentdriven/Gropius/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/intentdriven/Gropius/releases/tag/v0.1.0
