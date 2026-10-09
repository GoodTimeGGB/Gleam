# AI-assisted code review

Gleam CI does **not** require paid third-party secrets for merge.

## GitHub Copilot automatic code review

1. Open https://github.com/gleam-ai/Gleam/settings
2. If Copilot is available on the org/plan: enable **Copilot code review** / add a repository ruleset rule for automatic review.
3. Confirm via API (maintainers):

   ```bash
   gh api repos/gleam-ai/Gleam/rulesets
   ```

## CodeRabbit (optional)

Install URL (org admin must click Install — do not automate installs):

https://github.com/apps/coderabbitai

Recommended: install for **Only select repositories** → `gleam-ai/Gleam` (and optionally `gleam-ai/.github`).

## Why no secret-backed review workflow by default

Workflows that call OpenAI/Anthropic need repository secrets. Until those are provisioned, a guidance comment workflow documents options without failing CI.
