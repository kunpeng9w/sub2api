# 验证配置与历史日志

本目录集中保存配置验证材料；线上服务仍使用 `data/config.yaml`。

| 文件 | 用途与差异 |
|---|---|
| `config.initial.yaml` | 初期验证配置归档，原 `.validation/config.yaml`；未显式设置 `gateway.text_max_body_size`。保留原始内容，便于解释早期验证结果。 |
| `config.validation.yaml` | 验证模板，原 `scripts/config.validation.yaml`；在初期配置基础上增加 `gateway.text_max_body_size: 16777216`（16 MiB），与 `gateway.max_body_size` 相同。 |
| `logs/sub2api.log` | 原 `.validation/logs/sub2api.log` 的历史启动日志；记录了数据库占位密码认证失败，不能作为完整启动或调用成功的证明。 |

两份配置均监听 `127.0.0.1:18081`，数据库和 Redis 使用无效的验证占位密码，JWT/TOTP 使用固定测试值。它们用于验证材料归档或作为验证起点，不应替换线上配置；需要完整启动测试时，应另行准备隔离的数据库、Redis 和密钥。

配置和日志仅迁移路径，内容及文件权限保持不变。此处日志是历史归档，线上日志继续进入 systemd journal。

资源观察脚本仍将采样、结果及相关输出保存在 `.operator/`；这是现有脚本的输出位置，参见 [运维说明](../README-ops.md)。
