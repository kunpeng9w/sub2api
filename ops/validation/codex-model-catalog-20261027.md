# Codex 模型目录配置构建交付（2026-10-27，Asia/Singapore）

普通 OpenAI Codex 配置在远程目录模式下，于 `base_url` 紧接的下一行生成 `model_catalog_url = "<base_url>/models"`。本地目录模式仍生成 `model_catalog_json`。用户确认使用已有 `/v1/models` 接口。

- 构建源码：`cce6dcf6f5d1b42f657dcda0beb0dfe78bdb7d50`。
- 源码分支：`build/codex-model-catalog`；未更新默认 `main` 分支。
- GitHub Actions：<https://github.com/kunpeng9w/sub2api/actions/runs/40447785261>。
- 模板测试：`UseKeyModal.spec.ts`，30 项全部通过；前端生产构建和嵌入前端的 Go 编译成功。
- 交付目标：Linux amd64，静态链接程序，版本 `0.2.18`，真实来源提交已写入程序。
- 程序 SHA256：`08a9c7443dd6207aba5f0c1226f7ed1d8a90b6a4d271804774a3982df4d4ae25`。
- 本机交付目录：`/home/pk/workspace/sub2api/release/codex-model-catalog-cce6dcf6f/`，包括压缩包、校验文件、来源信息、测试日志、解包程序及 `deploy.sh`。

已核对现运行程序和官方备份与 `ops/deployed-build.json` 中登记的校验值一致；未替换程序、未执行部署脚本、未重启服务、未额外备份当前定制版本。官方备份 `bin/sub2api.backup-20261025T013912Z` 保留。由用户手动执行交付目录的 `deploy.sh`。

临时产物分支仅用于本次私有仓库的 SSH 下载。成功取回并核对产物后，清理临时产物分支和临时写入任务。最终工作流保留标准 artifact 上传，仓库权限为只读。
