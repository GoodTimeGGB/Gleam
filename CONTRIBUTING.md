# Contributing to Gleam

Gleam is the flagship product of [@gleam-ai](https://github.com/gleam-ai): a local-first desktop AI agent (~10MB single binary, zero third-party Go dependencies) for file organization, task planning, scheduled execution, with built-in tools and MCP extensions.

Org-wide rules live in [gleam-ai/.github CONTRIBUTING](https://github.com/gleam-ai/.github/blob/main/CONTRIBUTING.md). This file adds Gleam-specific setup.

## Prerequisites

- Go **1.22+** (see `go.mod`)
- Python 3 (for `scripts/check-*.py` gates)
- Git with **SSH commit signing** enabled for maintainers

## Local development

```powershell
# Clone
git clone git@github.com:gleam-ai/Gleam.git
cd Gleam

# Format, vet, build, test (Windows is the primary CI test OS)
gofmt -w ./internal ./pkg ./cmd
go vet ./...
go build ./...
go test ./... -count=1 -timeout 300s

# Optional full gate (bash)
bash scripts/verify.sh --quick
```

Default branch: **`master`**.

## Branch naming and commits

See the org [Contributing guide](https://github.com/gleam-ai/.github/blob/main/CONTRIBUTING.md) for Conventional Commits, scopes, signed commits, and DCO-style sign-off.

## AI-assisted review

- Prefer enabling **GitHub Copilot code review** via a repository ruleset (Settings → Rules) when available on your plan.
- Optionally install [CodeRabbit](https://github.com/apps/coderabbitai) for automated PR comments (requires a human to click Install — maintainers only).

## Code of Conduct / Security / Support

- [Code of Conduct](https://github.com/gleam-ai/.github/blob/main/CODE_OF_CONDUCT.md)
- [SECURITY.md](./SECURITY.md)
- [Support](https://github.com/gleam-ai/.github/blob/main/SUPPORT.md)
