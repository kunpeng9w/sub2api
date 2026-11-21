# 保留定制模块的上游同步流程

使用长期定制分支 `main`，每次从它创建 `codex/merge-upstream-vX.Y.Z`，合并明确的上游标签。升级 PR 必须使用 **Create a merge commit**，不能 squash 或 rebase：保留上游祖先关系后，下次 Git 只需处理新增差异。不要用 `-X ours`、整文件覆盖或自动接受 ours/theirs 保留定制，它们可能静默丢掉上游修复。

## 每次升级

1. 确认工作区干净，拉取 origin 与目标 upstream tag，记录升级前 main 和标签对应的完整提交 SHA。
2. 创建升级分支，执行 `git merge --no-ff --no-commit vX.Y.Z`。即使没有冲突，也要检查双方共同修改的文件。
3. 更新 `upstream-upgrade-ci.yml` 的迁移基线矩阵，保留最早定制基线，另一个使用升级前 main 的完整 SHA。新迁移加入升级回归的校验清单。
4. 推送升级分支并创建 PR。GitHub Actions 执行 CI、运维/嵌入构建、渠道监控、严格出口和升级检查；本机不编译或测试。生成代码由 Actions 导出补丁，审查后提交，再验证最终 SHA。
5. 检查最终提交对应的全部必需检查及日志，从 Actions 下载构建产物并核对版本、源码 SHA 与校验和。通过后使用普通 merge 合并 PR；生产部署单独进行。

在每个开发克隆中可启用冲突解决记录复用（只在本地保存，不随仓库克隆传播）：

```sh
git config --local rerere.enabled true
git config --local rerere.autoupdate false
```

Git 会复用相同冲突的历史解法，仍需人工审查后 `git add`；这不能解决接口或行为的语义变化。

## 定制模块边界

| 定制能力 | 尽量独立维护的位置 | 升级回归 |
|---|---|---|
| 渠道监控、手动权限与排序 | `channel_monitor*`、监控组件和独立迁移 | `channel-monitor-ci.yml` |
| 严格出口、代理策略、审计与连接回收 | 独立策略/审计辅助文件；HTTP/WS 入口集中调用 | `egress-policy-ci.yml`、`ops-ci.yml`、升级跨组件回归 |
| 客户端模板与模型目录 | 独立配置/模板生成辅助文件；组件仅组装 UI | `UseKeyModal.spec.ts`、完整前端测试 |
| 部署配置 | `ops/config/` 与 `ops/scripts/` | `ops-ci.yml` |

表中是维护方向，不表示所有共享文件已经拆分完毕。以后修改模块时渐进提取，避免为了升级同时大规模重构。需要介入上游行为的权限和网络策略必须保留明确调用点，不能仅把代码移到新文件而漏掉接入。可通用的修复适合单独贡献给上游；上游采纳后删除对应定制差异。

数据库迁移以完整文件名和校验和识别。已执行的迁移禁止改名、改内容；编号相同但文件名不同不必重编号，要通过空库、旧库升级和重复启动回归验证顺序与兼容性。

历史升级记录见[运维历史归档](archive.md)。
