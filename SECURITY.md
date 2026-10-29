# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Report them privately via
[GitHub's private vulnerability reporting](https://github.com/kalibek/compose-deploy/security/advisories/new)
instead.

Include what you found, how to reproduce it, and the version (image tag or commit) affected.

This is a personal project maintained on a best-effort basis: there is no guaranteed response time, but
reports are taken seriously and fixes are released as soon as practical.

## Supported versions

Only the latest image (`kalibek/compose-deploy:latest`, built from `main`) receives fixes.

## Scope

compose-deploy has access to the Docker socket, which is root-equivalent on the host. Anyone who can log
in, or push to a repository a stack deploys from, can run arbitrary containers by design — that is not a
vulnerability. See [Security notes](README.md#security-notes) for how to deploy it safely.

The software is provided "as is", without warranty of any kind; see [LICENSE](LICENSE).
