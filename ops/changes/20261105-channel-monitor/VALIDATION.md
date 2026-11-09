# 渠道监控验证记录与口径

验证在 GitHub Actions 进行。源码提交 SHA、运行地址和专项前后端检查结果由 `channel-monitor-ci.yml` 的 `verification` job 写入 `verification.json`，作为 `monitor-verification-<SHA>` artifact 保存；以对应 SHA 的记录为准，不将旧提交的通过结果代替新提交。

## 必须核对的检查

| 工作流 | 检查范围 |
| --- | --- |
| [渠道监控专项验证](https://github.com/kunpeng9w/sub2api/actions/workflows/channel-monitor-ci.yml) | Ent/Wire 生成、代理与刷新回归、窗口估算、用户隔离、数据库关联/费用统计集成、前端组件/类型/Lint、前后端构建 |
| [CI](https://github.com/kunpeng9w/sub2api/actions/workflows/backend-ci.yml) | 完整后端单元及集成测试、后端 Lint、前端关键回归、部署脚本检查 |
| [源码与运维验证](https://github.com/kunpeng9w/sub2api/actions/workflows/ops-ci.yml) | 出口控制既有回归、DNS 隔离夹具、前端构建及内嵌 Linux amd64 交付物 |
| [安全检查](https://github.com/kunpeng9w/sub2api/actions/workflows/security-scan.yml) | 采用仓库现有安全扫描 |

专项覆盖 HTTP/HTTPS/SOCKS 假代理、代理解绑/失效/版本变更、禁止重定向、必要的 OAuth 刷新、禁止附加出口 IP 查询、手动与定时互斥、冷却/锁失败、授权撤销、额度开关和错误脱敏。PostgreSQL 夹具验证代理软删除/物理删除、用户授权及按上游模型和采样时间截断的费用统计。

编译依赖在测试前下载；测试阶段阻断公网新连接，只允许回环与 Docker 测试网络。账号、密钥、上游和代理均使用夹具，不加载生产配置。构建只生成产物，不运行应用或启动生产调度器。

## 交付物

- `monitor-backend-<SHA>`：专项构建的后端程序。
- `monitor-frontend-<SHA>`：前端构建文件。
- `monitor-generated-<SHA>`：远程生成及格式化差异，用于核对提交是否包含生成代码。
- `monitor-verification-<SHA>`：专项检查结果及提交、运行链接。
- `sub2api-linux-amd64-<SHA>`：现有 ops 工作流构建的内嵌前端程序，含来源和校验信息。

未执行真实上游/真实代理联调，未部署或重启生产服务。模拟测试证明应用路由约束，不能单独证明代理服务后的公网 NAT IP；真实联调严格使用 [单独确认清单](LIVE-TEST-CHECKLIST.md)。

## 2026-11-05 本次执行结果

业务代码及测试提交：`5e1d8402b3c47d197a28dd561cf9ab6925514ca7`；随后 `a31c84f743ab368877d62669cf57affc0e5458ec` 仅修复 CI 隔离进程缺失 Docker 附加组的问题，并增加快速预检。此修复尚未得到远程执行验证。

- [专项检查（5e1d8402b）](https://github.com/kunpeng9w/sub2api/actions/runs/42041633909)：前端组件回归、类型检查、Lint 和构建通过；后端专项单元回归通过；Ent/Wire 生成差异为空。数据库专项因隔离进程无法访问 Docker 而未执行，不能视为集成测试通过。
- [完整 CI（5e1d8402b）](https://github.com/kunpeng9w/sub2api/actions/runs/42041633752)：后端完整单元测试、golangci-lint、前端关键回归及脚本检查通过；完整数据库集成未通过最终验收。
- [源码/运维检查（5e1d8402b）](https://github.com/kunpeng9w/sub2api/actions/runs/42041633699)：全部通过，包括出口回归、DNS 隔离夹具、内嵌 Linux amd64 构建。
- 已取回 `5e1d8402b` 的前端及内嵌后端产物，校验内嵌包 SHA256 成功；产物来源信息包含提交 SHA 和 Actions URL。未执行产物、未部署、未触发真实检测。
- [最终修复复验（a31c84f74）](https://github.com/kunpeng9w/sub2api/actions/runs/42043394901)：GitHub 在分配 runner 前拒绝启动所有 job，没有执行任何测试。check annotation 原因：`The job was not started because recent account payments have failed or your spending limit needs to be increased. Please check the 'Billing & plans' section in your settings`。

**待完成项**：GitHub 账号付款/Actions spending limit 恢复后，对最终提交重新执行专项、完整 CI 和源码/运维工作流，确认 Docker 预检、监控关联集成及窗口费用 SQL 集成通过，再获取该最终提交的 artifacts。不得用旧提交的产物或结果冒充最终提交验收。

真实上游与真实代理联调继续保持未执行，须另经用户确认。
