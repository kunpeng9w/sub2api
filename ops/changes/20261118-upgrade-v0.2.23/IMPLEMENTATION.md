# v0.2.23 升级

- 起点：生产定制源码 `9339f5c5d7fcfd8e1ee33ba9567b6537e98b506c`。
- 上游标签：`v0.2.23`，指向 `5af9afb588c4b014b3df9db7d4073af1f8ae01b6`。
- 合并目标：`2af3f1e15c98c893a89255a037f551046883b8c0`，包含标签及其后仅修正 VERSION 的提交。
- 普通 merge 保留双方历史；唯一文本冲突 VERSION 设为 0.2.23。
- 原工作区未提交的 Nginx、部署记录等改动保留；升级在独立工作区实施。

## 共同修改路径复核

双方共同修改 18 个文件。协议转换、Anthropic 缓存 TTL 明细、OpenAI 计费桶采用上游修复；原绑定账号出口、断连 drain、权限及窗口采样接入保留。检查 gateway_service 仅增加内部 usage 字段；openai_gateway_usage 计费转换采用上游统一 CostInput，定制窗口逻辑保留。原生 Chat Completions flush 优化使用上游 reader；原出口和 drain 逻辑保留。Codex 目录增加 6.2 描述，客户端模板增加模型配置，原定制模板 helper 与默认配置保留。前端渠道翻译同时保留定制字段和 Ultra Fast 倍率。

## 数据库与验证

唯一新迁移 `252_channel_pricing_ultrafast_multiplier.sql` 保留上游原文。已部署迁移内容不变。Actions 迁移矩阵保留最早定制基线，并改为生产 9339f5c5d 基线；现有真实迁移 runner 回归增加 252 的校验和、列类型、可空性和约束验证，每种基线含空库与重复启动。

编译、测试、Lint、生成、扫描与嵌入构建均由 GitHub Actions 执行，结果以最终源码 SHA 为准。部署使用该 SHA 的 Actions artifact，停服保存数据库及配置恢复点，核对来源与 SHA 后安装，并记录迁移、健康与日志状态。部署结果另记 POST-DEPLOY.md。
