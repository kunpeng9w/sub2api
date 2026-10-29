# 安全修复与应用部署准备（2026-10-29）

状态：**安全扫描、专项回归、全量单元及隔离集成、lint、前端检查和正式构建全部通过。部署包已校验，待确认第一阶段应用维护窗口；尚未部署。** 完整记录见 [CI 证据](CI-EVIDENCE.json)。

应用候选：`1dce7da6bcd3330c9b4ce92e6e5aa6a8746e2eaf`，分支 `codex/strict-egress-policy`。

- 修复 `GO-2026-6860`：`golang.org/x/net` 从 `v0.60.0` 升级到 `v0.62.0`，关联的 `x/crypto`、`x/sys`、`x/term`、`x/text`、`x/sync`、`x/tools` 按模块依赖要求更新。
- 本轮审计发现 `GHSA-83rm-6c8r-g728`：Vue I18n v9 已无修复版本，升级运行库和消息编译器到 `11.4.14`，相关 CI 使用 Node 22。
- 新增真实运行库回归，覆盖语言切换、插值、复数、链接消息、回退、CSP 和 iframe `srcdoc` 防护；同时将既有语言包编译和键冲突检查纳入关键前端回归。
- 沿用已有前端审计豁免策略，没有新增或延长漏洞豁免；历史失败记录保留。

## 验证状态

- [安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/40792733107)：前后端通过，后端报告 `No vulnerabilities found`。
- [专项回归](https://github.com/kunpeng9w/sub2api/actions/runs/40792732897)：32 个 Go 顶层用例、11 项 Python 模拟和 8 项专项前端用例通过。
- [全量回归](https://github.com/kunpeng9w/sub2api/actions/runs/40792733044)：全量单元（59 个包）及隔离集成（53 个包）通过；lint、587 项关键前端用例、脚本检查通过。
- [正式构建](https://github.com/kunpeng9w/sub2api/actions/runs/40792733142)：前端和 Linux amd64 嵌入式构建通过。

首次仅升级 Go 依赖的 `47e249a12` 后端扫描通过，前端审计暴露此前未报告的 Vue I18n 漏洞；随后完成前端升级和兼容性回归。最终提交的验证结果见已归档的 CI 证据，既有审计豁免保持原样。

## 候选交付包

目录：`/home/pk/workspace/sub2api-egress-delivery/1dce7da6bcd3330c9b4ce92e6e5aa6a8746e2eaf/`。

| 文件 | SHA256 |
| --- | --- |
| `sub2api-linux-amd64.tar.gz` | `44eabff18981cdfee866ed187954ec5c815a70c6bfd31da8e304f433799ce09e` |
| 包内 `sub2api` | `6535d20036928c966bcd10c447d8449ae4d7c342a2baea1f2dcc72583f6706d0` |

已核对压缩包清单只有普通文件 `sub2api`，清单校验通过，`build-info.txt` 提交与候选一致。仅保留交付文件，未安装或执行候选程序。

## 第一阶段部署范围

使用更新后的 [部署教程第 0、1 节](../20261028-egress-policy/DEPLOY.md) 和 [第一阶段回滚](../20261028-egress-policy/ROLLBACK.md)。实际部署需另行确认维护窗口，Sub2API 替换程序时会短暂停止。

1. 部署前重新核对在线健康、程序摘要及出口保护状态，并核验本候选包。
2. 在 root 私有目录准备合并后的第一阶段配置，备份当前程序、配置和数据库，验证备份可用。
3. 停止应用，保存代理回退字段和账号绑定清单，规范化旧回退字段并确认账号绑定不变。
4. 原子替换程序和应用配置，保持 `gateway.claude_egress.enabled=false`，启动应用并检查健康、既有出口保护和日志。
5. 观察正常请求延迟和数据库负载；异常时按第一阶段回滚恢复保存的程序和配置。

当前在线程序只读摘要为 `08a9c7443dd6207aba5f0c1226f7ed1d8a90b6a4d271804774a3982df4d4ae25`，模块构建记录为 `cce6dcf6f5d1b42f657dcda0beb0dfe78bdb7d50`。与本候选比较，`backend/migrations` 无新增或变更；仍需按教程备份并规范化已有代理回退字段。部署时以重新采集的实际基线为准。 只读预检时共有 2 个活跃账号，均未绑定独立代理；活跃账号失效绑定数与旧回退配置数均为 0。现有保护为 `confirmed`，规则存在且一致，六个相关服务 active，健康接口返回 `ok`。这些仅是部署前快照，不替代部署时重新核验。

本阶段不迁移 DNS、TUN 或出口保护。第二阶段需单独确认受管客户端、DNS/连接缓存和回滚前提，应用稳定后再决定。

漏洞来源：[Go 官方条目](https://pkg.go.dev/vuln/GO-2026-6860)、[Vue I18n 维护者公告](https://github.com/intlify/vue-i18n/security/advisories/GHSA-83rm-6c8r-g728)。
