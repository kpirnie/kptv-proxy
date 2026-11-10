# Security Policy

## Supported versions

Security fixes are applied to the latest release and the `main` branch. Please reproduce any issue on the latest version before reporting it.

## Reporting a vulnerability

**Do not open a public issue, pull request, or discussion for a security problem.**

Report it privately using one of:

- GitHub's **Report a vulnerability** button under the repository's **Security** tab (private vulnerability reporting)
- Email: iam@kevinpirnie.com

Please include:

- A description of the vulnerability and its impact
- Steps to reproduce, or a proof of concept
- The affected version or commit
- Your environment (host OS, Docker or Podman version, Go restreaming or FFmpeg mode) if relevant
- Any suggested fix

Remove real credentials, API tokens, XC account passwords, and provider URLs from anything you send.

## What to expect

- You will receive an acknowledgement of your report.
- The report will be validated, and you may be asked for more detail.
- A fix is prepared and released, and you are credited in the release notes unless you prefer to stay anonymous.
- Please allow time for a fix to ship before disclosing the issue publicly.

## Scope

In scope: the admin interface and API, authentication, session handling, and API token permissions, Xtream Codes account authentication on the playlist, EPG and stream endpoints, the HDHomeRun local network restriction, handling of source and provider URLs and credentials, and the project's container image and build configuration.

Out of scope: vulnerabilities in third-party software that KPTV Proxy runs or depends on (FFmpeg and FFprobe, upstream IPTV providers and their streams, reverse proxies placed in front of it), findings that require an already-compromised host or administrator account, custom CSS or settings supplied by an authenticated administrator, and denial of service through excessive resource use by an authenticated administrator.
