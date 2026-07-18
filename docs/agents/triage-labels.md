# Triage Labels

skill 用五种 canonical triage role 来对话。本文件把这五种 role 与本仓库实际用到的 label 字符串做映射。

| mattpocock/skills 中的 Label | 本仓库 tracker 中的 Label | 含义                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | 维护人需要先评估这条 issue  |
| `needs-info`               | `needs-info`         | 等报告人补充信息 |
| `ready-for-agent`          | `ready-for-agent`    | 已充分 spec，可交给 AFK agent 处理 |
| `ready-for-human`          | `ready-for-human`    | 需要人来实施 |
| `wontfix`                  | `wontfix`            | 不会动手 |

当某个 skill 提到一个 role（例如"apply the AFK-ready triage label"），就用本表里对应的右侧字符串。

改右侧那列以匹配你实际用的词汇。
