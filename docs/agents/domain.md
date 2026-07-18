# Domain Docs

当 skill 探索本仓库代码时，应如何消费本仓库的领域文档。

## 探索开始前，先读这些

- 仓库根目录的 **`CONTEXT.md`**，或
- 仓库根目录的 **`CONTEXT-MAP.md`**（如果存在）——它会指向每个 context 自带的 `CONTEXT.md`。把与当前话题相关的都读一遍
- **`docs/adr/`** ——读与你即将动手领域相关的 ADR。多 context 仓库里，还要看 `src/<context>/docs/adr/` 是否有 context-scoped 的决策

如果这些文件**不存在**，**静默推进**。不要抱怨它们缺失，也不要主动建议补——`/domain-modeling` skill（由 `/grill-with-docs` 或 `/improve-codebase-architecture` 触发）会在术语或决策真到了 need-to-resolve 的节点时，按需懒生成。

## 文件结构

单 context 仓库（多数仓库）：

```
/
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

多 context 仓库（根目录有 `CONTEXT-MAP.md`）：

```
/
├── CONTEXT-MAP.md
├── docs/adr/                          ← system-wide 决策
└── src/
    ├── ordering/
    │   ├── CONTEXT.md
    │   └── docs/adr/                  ← context 内的决策
    └── billing/
        ├── CONTEXT.md
        └── docs/adr/
```

## 使用 glossary 的词汇

当你的产出提到一个领域概念（无论是在 issue 标题、重构提案、假设、还是测试名里），用 `CONTEXT.md` 中定义的那个 term。不要漂移到 glossary 显式排除的同义词。

如果你需要的概念不在 glossary 里，这本身是个信号——要么是项目根本不用这个语言（重新考虑），要么真的存在 gap（记到 `/domain-modeling` 那边）。

## 标出 ADR 冲突

如果你的产出与某条既有 ADR 冲突，**显式地**提出来，而不是悄悄覆盖：

> _与 ADR-0007（event-sourced orders）冲突——但值得重启，因为…_
