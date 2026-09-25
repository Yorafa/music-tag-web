# Commit 约定

本文件是 commit message 的唯一标准。`.githooks/commit-msg` 会强制执行其中
可机器检查的部分。

## 格式

```
type(scope): summary

正文：为什么改，而不是改了什么。空行分隔，可用 - 列点。
```

- **subject** — `type(scope): summary`，全小写 type，`scope` 可选，summary 用
  祈使句、不以句号结尾、≤ 72 字符
- **body** — 解释**动机和权衡**。文件清单和 diff 已经说清了"改了什么"，
  正文只写"为什么"。多段落之间空行分隔
- **footer** — 仅在有 issue 引用或 breaking change 时写

示例：

```
fix(gateway): fail closed when the config write is denied

config.Load() ran ensureBootstrap() on every anonymous Login, so a
non-writable ./data turned one unauthenticated request into log.Fatalf
and killed the process. Split into LoadAtBoot() (main only) and
Current() (request path, no disk IO).
```

## 禁止内容

**任何 agent / bot 的署名尾注**：

```
🤖 Generated with <tool>
Co-Authored-By: <bot> <...>
```

本仓库的 commit 只描述变更本身及其理由。工具署名由平台负责归属，不写进
git 历史。hook 对 `Codebuff` / `Claude` / `ChatGPT` / `Copilot` 做大小写不敏感
匹配。

## type 取值

分两类，都可用：

**变更性质**（沿用 conventional commits）

`feat` `fix` `refactor` `perf` `test` `docs` `chore` `build` `ci` `style` `revert`

**代码层**（本仓库近 30 个 commit 的既有用法）

`frontend` `backend` `infra` `polish`

新提交沿用上下文里已有的 type，不要在同一批改动里混用两套词汇。

> 约定式写法之前的历史（自由格式中文 subject）不受此约束 —— hook 只在
> **新提交**时运行，不追溯校验既有 commit。

## 启用 hook

仓库把 hook 放在 `.githooks/`，但**不自动写入 `.git/config`**，所以 clone
之后需要手动启用一次：

```bash
git config core.hooksPath .githooks
```

启用后不合规的 commit 会被拒绝。确实需要绕过时（例如 rebase 中间步骤）：

```bash
git commit --no-verify
```

## 绕过场景

hook 只检查 **subject 首行 + bot 署名**，以下情况自动放行：

- `git commit --amend` 已有 commit（不应因历史 commit 不满足新规则而卡住）
- rebase 生成的 merge commit（无作者撰写的 subject）

其余规则一律强制。
