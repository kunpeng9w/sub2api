# Sub2API 运维

当前部署版本、运行模式、二进制 SHA-256、构建来源和备份位置以部署记录为准。[部署记录](deployed-build.json)

公网流量经 Nginx 转发到本机 Sub2API。应用使用 PostgreSQL 和 Redis，官方 Claude 域名使用定向出口，前端随 Linux amd64 程序一同构建。

## 配置入口

| 组件 | 仓库配置 | 服务器位置 |
|---|---|---|
| Sub2API | [应用模板](config/application.yaml.example) | `data/config.yaml` |
| Nginx | [HTTPS、流式转发和登录限流](config/nginx/sub2api.conf) | `/etc/nginx/sites-available/sub2api` |
| systemd | [应用、备份、出口保护及回程单元](config/systemd/) | `/etc/systemd/system/` |
| Mihomo | [Claude 定向代理模板](config/mihomo/config.yaml.example) | `/etc/mihomo/config.yaml` |
| PostgreSQL | [本机实例参数](config/postgresql/99-sub2api.conf) | `/etc/postgresql/16/main/conf.d/99-sub2api.conf` |
| Redis | [本机实例模板](config/redis/redis.conf.example) | `/etc/redis/redis.conf` |
| Certbot | [ACME 续期模板](config/certbot/idontknow.site.conf.example) | `/etc/letsencrypt/renewal/` |

示例配置在私有目录填写真实密码、密钥和代理凭据后再安装。应用配置使用权限 `0600`。出口保护、DNS 生命周期和独立网络状态服务各有运维手册。[出口保护](scripts/egress-guard/README.md) · [Mihomo DNS](scripts/mihomo-dns/README.md) · [网络状态](network-status/README.md)

## 常用检查

```bash
systemctl is-active sub2api nginx postgresql@16-main redis-server
curl -fsS http://127.0.0.1:8080/health
sudo nginx -t
sudo mihomo-ctl status
ip rule show
journalctl -u sub2api -n 100 --no-pager
sha256sum /home/pk/workspace/sub2api/bin/sub2api
```

Mihomo 可以按运维安排停止，DNS 和出口保护仍应运行。

备份 timer 每天新加坡时间 03:30 运行，备份期间短暂停止应用。检查 timer、备份目录和最新备份日志。[备份 timer](config/systemd/sub2api-backup.timer) · [备份脚本](scripts/backup.sh)

使用 GitHub Actions 的 Source and operations verification 获取 Linux amd64 产物。下载与目标提交 SHA 对应的包，核对其中的 `SHA256SUMS`、`build-info.txt` 和 `sub2api.sha256`。[构建工作流](../.github/workflows/ops-ci.yml)

## 维护手册

- [应用部署预检与回滚](rollback.md)
- [上游同步流程](upstream-sync.md)
- [历史计划、验收报告与 CI 证据](archive.md)
