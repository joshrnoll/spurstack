# Spurstack

<p align="center">
  <img src="assets/spurstack.png" alt="Spurstack logo" width="220" />
</p>

Spurstack is a self-hosted stack of autonomous coding agents. Each issue runner is a **spur**: a small agent process that nudges a repository from a GitHub issue toward a pull request.

Spurstack polls GitHub every 60 seconds and starts one spur for each open issue labeled `agent-ready`.

For each matching issue, Spurstack:

1. Claims the issue by adding `agent-running` and removing `agent-ready`.
2. Skips issues that also have `agent-running` or `agent-pr-opened`.
3. Clones or updates the repository cache.
4. Creates a dedicated git worktree and branch.
5. Asks an OpenAI-compatible model for an implementation plan.
6. Lets the spur read/edit/write files in the worktree.
7. Commits changes with a Conventional Commit message.
8. Pushes the branch and opens a GitHub pull request.
9. Adds the unique spur run ID to the PR body so logs can be found later.
10. Replaces `agent-running` with `agent-pr-opened` on success, or `agent-failed` on failure.

The default LLM target is OpenRouter, but any OpenAI-compatible chat completions endpoint can be used. No inbound internet access is required; Spurstack only makes outbound HTTPS calls to GitHub and the model provider.

For OpenRouter, you can force provider routing. For example, to use DeepInfra only:

```bash
OPENROUTER_PROVIDER_ORDER=DeepInfra
OPENROUTER_ALLOW_FALLBACKS=false
```

## Configuration

Environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `GITHUB_TOKEN` | yes | | Token with repo contents, issues, labels, and pull request permissions. |
| `GITHUB_REPOSITORIES` | yes | | Comma-separated repos to poll, e.g. `owner/repo,owner/other-repo`. |
| `OPENAI_API_KEY` | yes | | OpenRouter/OpenAI-compatible API key. |
| `OPENAI_BASE_URL` | no | `https://openrouter.ai/api/v1` | OpenAI-compatible API base URL. |
| `OPENAI_MODEL` | no | `anthropic/claude-3.5-sonnet` | Model name. |
| `OPENROUTER_PROVIDER_ORDER` | no | | Comma-separated OpenRouter provider preference, e.g. `DeepInfra`. |
| `OPENROUTER_ALLOW_FALLBACKS` | no | `true` | Whether OpenRouter may fall back to other providers when `OPENROUTER_PROVIDER_ORDER` is set. |
| `WORKSPACE_DIR` | no | `/var/lib/spurstack/workspace` | Persistent clone/worktree storage. |
| `POLL_INTERVAL` | no | `60s` | GitHub polling interval. |
| `SPUR_LABEL` | no | `agent-ready` | Issue label that triggers spurs. |
| `MAX_SPUR_STEPS` | no | `30` | Max model/tool iterations per spur run. |
| `GIT_AUTHOR_NAME` | no | `Spurstack` | Commit author name. |
| `GIT_AUTHOR_EMAIL` | no | `spurstack@example.local` | Commit author email. |

Backward-compatible aliases are still accepted for now: `AGENT_LABEL` and `MAX_AGENT_STEPS`.

## GitHub setup

No webhook is needed.

Create these labels in each watched repo, or let GitHub create them when the token first applies them:

```text
agent-ready
agent-running
agent-pr-opened
agent-failed
```

To trigger Spurstack, add `agent-ready` to an open issue in a repo listed in `GITHUB_REPOSITORIES`.

Agent labels act as a simple state machine:

```text
agent-ready -> agent-running -> agent-failed
                            \-> agent-pr-opened
```

Issues with `agent-running` or `agent-pr-opened` are skipped even if `agent-ready` is also present. To retry a failed issue, add `agent-ready`; Spurstack will remove `agent-failed` when it claims the retry.

## Run locally

```bash
export GITHUB_TOKEN=...
export GITHUB_REPOSITORIES=owner/repo
export OPENAI_API_KEY=...
go run ./cmd/spurstack
```

## Docker Compose

```bash
cp .env.example .env
# edit .env with real secrets
docker compose up --build
```

## Container

```bash
docker build -t spurstack .
docker run --rm \
  -e GITHUB_TOKEN=... \
  -e GITHUB_REPOSITORIES=owner/repo \
  -e OPENAI_API_KEY=... \
  -v spurstack-workspace:/var/lib/spurstack/workspace \
  spurstack
```

## Logs and run IDs

Each spur run gets an ID like `run-1a2b3c4d5e6f7890`. The ID is logged as `run_id=...` and appended to the PR body:

```text
Spur run ID: `run-1a2b3c4d5e6f7890`
```

Find logs for a run with:

```bash
docker compose logs spurstack | grep run-1a2b3c4d5e6f7890
```

## Notes

- Runs are serialized per GitHub issue inside one Spurstack instance.
- GitHub labels are the durable run state across restarts.
- Successful runs clean up their worktree after opening a PR.
- Failed runs keep their worktree for inspection until the same issue is retried.
