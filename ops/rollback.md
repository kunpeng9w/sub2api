# 应用部署预检与回滚

先核对正在运行的程序、最近的停服备份和上一版产物。检查目标构建是否包含数据库迁移，再选择相应的恢复方式。[部署记录](deployed-build.json)

## 二进制更新与回滚

以下入口用于原生 systemd 实例的应用程序更新，要求当前实例的 Claude 定向出口和出口保护已启用。使用已核验的产物目录以及归档包、新程序、当前程序的 SHA-256。

```bash
sudo bash ops/scripts/deploy/deploy.sh --check \
  "$release_dir" "$archive_sha256" "$binary_sha256" "$previous_sha256"
```

在维护窗口以相同参数运行 `--apply`。脚本核对运行中的程序和磁盘上的程序，保存旧程序与配置副本，再原子替换二进制。启动或健康检查失败时会尝试恢复旧程序。手动恢复时，以 root 运行该次更新输出的备份目录中的 `rollback.sh`。回滚脚本校验备份程序摘要并验证启动健康。[部署入口](scripts/deploy/deploy.sh)

## 涉及数据库迁移的更新

更新前在停止应用写入后备份数据库、应用配置和运行二进制，校验备份。需要回滚时，停止应用并保全故障现场，使用同一部署检查点的数据库备份、配置及对应的二进制恢复。启动前验证恢复库的迁移记录，启动后核对本机健康端点、HTTPS、登录和所需的真实流量。部署记录列出了备份路径和对应 SHA。[部署记录](deployed-build.json)

## Nginx 与出口配置

- Nginx 回滚时恢复维护窗口前的站点配置，执行 `sudo nginx -t`，通过后执行 `sudo systemctl reload nginx`。检查 HTTPS、登录和流式路由。[当前 Nginx 配置](config/nginx/sub2api.conf)
- Mihomo 及出口保护按控制器状态恢复，随后检查 `sudo mihomo-ctl status` 和 DNS 生命周期。[出口保护手册](scripts/egress-guard/README.md) · [Mihomo DNS 手册](scripts/mihomo-dns/README.md)
- 如果变更了 networkd 回程配置，维护窗口内恢复变更前的 drop-in，重启 `systemd-networkd` 后验证路由表、出口保护及 SSH 连通性。[回程配置](config/systemd/networkd.conf.d/70-mihomo-routing.conf)
