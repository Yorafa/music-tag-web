## Agent skills

### Commit 约定

commit message 格式为 `type(scope): summary`，**禁止**任何 agent / bot 署名尾注
（`🤖 Generated with ...` / `Co-Authored-By: <bot>`）。完整标准与 hook 启用方式见
[`docs/agents/commit-convention.md`](docs/agents/commit-convention.md) — 写 commit 前先读。

### Issue tracker

Issue 通过 markdown 文件落在 `.scratch/<feature-slug>/` 下。PR 不作为本仓库的请求面，详见 [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)。

### Triage labels

五种 canonical 角色（`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`）以 `Status:` 行出现在每个 issue 文件中。详见 [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md)。

### Domain docs

本仓库跨多个 context，根目录的 `CONTEXT-MAP.md` 指向 per-context `CONTEXT.md`（一旦术语对齐，由 `/domain-modeling` 决定如何拆分）。详见 [`docs/agents/domain.md`](docs/agents/domain.md)。
