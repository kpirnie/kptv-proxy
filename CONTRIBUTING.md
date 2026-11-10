# Contributing to KPTV Proxy

Thanks for taking the time to contribute. This guide covers how to report problems, set up a development environment, and submit changes.

## Reporting bugs

Open an [issue](https://github.com/kpirnie/kptv-proxy/issues) and include:

- What you expected to happen and what actually happened
- Steps to reproduce
- Your KPTV Proxy version or commit, and how you run it (Docker or Podman, host OS)
- Relevant log output with debug mode enabled
- Whether FFmpeg mode or Go restreaming mode was in use

Remove provider URLs, usernames, passwords, and tokens from anything you post.

**Security problems must not be reported in public issues.** Follow [SECURITY.md](SECURITY.md) instead.

## Suggesting features

Open an issue describing the problem you want solved before writing code. For larger changes this avoids work on something that doesn't fit the project. You can also talk it through on [Discord](https://discord.gg/bd4Qan3PaN).

## Development setup

Requirements:

- Go (the version declared in `go.mod`)
- Node.js and npm, for the admin interface assets
- FFmpeg and FFprobe, only if you are working on FFmpeg mode

```bash
git clone https://github.com/kpirnie/kptv-proxy.git
cd kptv-proxy
git checkout develop
npm install --include=dev
npm run build
go build -o kptv-proxy .
```

## Project layout

| Path | Purpose |
| ---- | ------- |
| `main.go` | Application entry point |
| `work/<package>/` | Application code, one package per domain |
| `testing/<package>/` | Tests for the matching `work/` package |
| `static/src/` | Admin interface JavaScript sources |
| `static/admin-source.css` | Admin interface Tailwind source |
| `static/admin.js`, `static/admin.css` | Generated admin assets |

Tests live outside the application packages as external test packages, so they can only use exported identifiers. Do not add `_test.go` files under `work/`.

## Making changes

1. Fork the repository and create a branch from `develop`: `git checkout -b feature/short-description`
2. Keep the change focused. Don't mix unrelated fixes, refactors, or reformatting into the same pull request.
3. Match the surrounding code: naming, comment style, error handling, and the `{package/file - FuncName}` log tag format.
4. Add godoc comments to exported types, functions, and methods.
5. Run `gofmt` on every Go file you touch.
6. If you change anything in `static/src/` or `static/admin-source.css`, run `npm run build` and include the regenerated `static/admin.js` and `static/admin.css`.
7. Run the checks below.

```bash
go vet ./...
go test ./testing/...
```

Do not commit credentials, `.env` files, provider URLs, or generated artifacts such as coverage output.

## Pull requests

- Target the `develop` branch.
- Describe what changed and why, and link any related issue.
- Note how you tested the change, including whether you tried it with FFmpeg mode, Go restreaming mode, or both when it touches streaming.
- Keep the first line of each commit message short and in the imperative mood, for example `Fix stall detection on FFmpeg restart`.

Pull requests are reviewed as time allows. You may be asked for changes before a merge.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
