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
- Your environment (host OS, Podman version, site type) if relevant
- Any suggested fix

Remove real credentials, tokens, and private hostnames from anything you send.

## What to expect

- You will receive an acknowledgement of your report.
- The report will be validated, and you may be asked for more detail.
- A fix is prepared and released, and you are credited in the release notes unless you prefer to stay anonymous.
- Please allow time for a fix to ship before disclosing the issue publicly.

## Scope

In scope: the PodNest panel and API, authentication and session handling, the built-in reverse proxy, backup and restore, site and container management, and the project's container images and build configuration.

Out of scope: vulnerabilities in third-party software that PodNest manages or runs (WordPress core, plugins, themes, or site code that users deploy), findings that require an already-compromised host or administrator account, and denial of service through excessive resource use by an authenticated administrator.
