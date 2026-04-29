# Security policy

## Reporting a vulnerability

Please report security issues privately to **security@bron.org**. We aim to
acknowledge new reports within two business days and will keep you informed
of remediation progress.

Do **not** open a public GitHub issue, post a discussion thread, or include
exploitation details in a pull request — that gives every Bron customer the
same window of exposure.

When reporting, please include:

- a clear description of the issue and its impact,
- steps to reproduce (a minimal command or short script is ideal),
- the CLI version (`bron --version`) and OS / arch,
- any relevant logs (with secrets redacted).

We support coordinated disclosure: once a fix is shipped and customers have
had a reasonable window to upgrade, we'll publish an advisory crediting the
reporter (unless you prefer to remain anonymous).

## Scope

This policy covers the `bron` CLI binary and the source tree in this
repository. Issues in the public Bron API itself, the SDKs, or the
`bron.org` web app should also go to **security@bron.org** and we'll route
them internally.

Out of scope: theoretical issues without a working PoC, vulnerabilities in
third-party dependencies that don't affect the CLI's behaviour, and
denial-of-service against your own machine (e.g. passing a 10 GB JSON to
`--file`).

## Supported versions

Only the latest minor release on `master` receives security fixes. Pinning
to an older version means you'll need to upgrade to pull a fix.
