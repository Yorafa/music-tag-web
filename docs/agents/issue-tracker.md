# Issue tracker：本地 Markdown

本仓库的 issue 与 PRD 都以 markdown 文件的形式落在 `.scratch/` 下。

## Conventions（约定）

- 每个 feature 一个目录：`.scratch/<feature-slug>/`
- PRD 文件：`./scratch/<feature-slug>/PRD.md`
- 实施 issue：`./scratch/<feature-slug>/issues/<NN>-<slug>.md`，编号从 `01` 起
- triage 状态写在每个 issue 文件靠顶部的 `Status:` 行（具体 role 字符串见 `triage-labels.md`）
- 文件顶部（`Status:` 行 + 任何 frontmatter）可变；**除此之外的内容 append-only**——评论与对话历史持续累加在 `## Comments` heading 之下。skill **只能 append**，绝不能改写对话段

## 当某个 skill 说"publish 到 issue tracker"

在 `.scratch/<feature-slug>/` 下新建一个文件（必要时先建目录）。

## 当某个 skill 说"取回相关 ticket"

读所指路径的文件。用户一般会直接给路径或 issue 编号。
