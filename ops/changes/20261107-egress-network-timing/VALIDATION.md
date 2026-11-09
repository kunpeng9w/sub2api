# 验证记录（2026-11-07，Asia/Singapore）

全部编译、测试、race 检查、Lint 与构建由 GitHub Actions 执行。本机仅编辑、格式整理、阅读远程日志和执行 `git diff --check`。尚未部署、替换程序或重启服务；用户原有 Nginx 修改和速度分析文件保持原样。

## 首轮失败原因

首轮源码提交：`9eb20857ffb561d2f66ed39572802d039da97b2b`。

- [源码/运维检查](https://github.com/kunpeng9w/sub2api/actions/runs/42249519416)：组件回归的 `TestEgressAuditNetworkRequestReplay` 使用 `require.Same` 比较非指针的请求体包装值，断言报 `Both arguments must be pointers`。修正为值比较，仍验证原请求的 Body 没有被替换。该轮其他新增组件用例通过，但整个组件任务失败，完整服务回归被跳过。
- [CI](https://github.com/kunpeng9w/sub2api/actions/runs/42249519409)：Lint 检出测试中 2 处关闭返回值未处理、2 处类型断言未检查；均已修正。后端任务在 `go mod download` 下载 `github.com/go-playground/locales@v0.14.1` 时遇到 `proxy.golang.org` HTTP/2 `INTERNAL_ERROR`，没有进入单元或集成测试。该项属于远程依赖下载失败，不能表述为后端测试失败或通过。
- 首轮前端检查、嵌入前端构建及安全扫描通过，但不作为修正源码的验收替代。

## 修正提交复验

源码提交：`a6d879615df07d6a7704134528b0bee7f76a300c`；分支 `codex/egress-network-timing`。除修正测试与 Lint 外，补充 Claude 指纹 TLS 握手、指纹 SOCKS 隧道阶段的观测及回归测试。上述新增字段也在此次复验源码范围内。

- [源码/运维检查](https://github.com/kunpeng9w/sub2api/actions/runs/42251416827)：**全部通过**，含出口审计组件 race 检查、完整服务/仓库传输回归、代理路由回归、DNS 生命周期、运维配置及嵌入前端的 Linux amd64 构建。
- 专项日志确认以下用例通过：真实回环 HTTP/TLS 请求、已有 trace 组合、请求体 GetBody 重放、并发连接及超时、缺失 trace、响应体部分读取错误、透传 WS 每轮计数、生产 WS 编码器和同连接两轮租约、JSON 编码字节保持、指纹 TLS 握手失败观测。mock/回环验证不等于真实 Claude/Codex 上游联调。
- [安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/42251416711)：**通过**。
- [完整 CI](https://github.com/kunpeng9w/sub2api/actions/runs/42251416693)：**全部通过**：后端全量单元测试、数据库集成测试、golangci-lint、前端类型/关键组件检查、部署脚本与发布辅助检查。先前的依赖下载错误本轮未复现。

## 构建与日志产物

以下 artifacts 均来自修正源码 `a6d879615` 的源码/运维工作流：

- [Linux amd64 构建包](https://github.com/kunpeng9w/sub2api/actions/runs/42251416827/artifacts/13520657322)：`sub2api-linux-amd64-a6d879615df07d6a7704134528b0bee7f76a300c`，包括归档、SHA256、version 和源码来源信息。工作流保留 7 天；未将产物部署到服务器。
- [出口审计回归日志](https://github.com/kunpeng9w/sub2api/actions/runs/42251416827/artifacts/13520617820)：`egress-audit-tests-a6d879615df07d6a7704134528b0bee7f76a300c`。

本记录是复验说明；如后续只修改本文，受测可执行代码及构建来源仍为上述源码提交，不能将文档提交号冒充产物源码提交号。

## 审核修复复验

修复源码：`86c70522ee0a590350e442cf5475fc9c9c1fecaa`。请求已成功写完或已收到响应后，最终错误按等待响应/读取响应阶段归类，不再因错误分类相同而沿用已恢复的建连或写入失败阶段。尚未取得这些进度时仍保留具体回调阶段。

- [源码/运维检查](https://github.com/kunpeng9w/sub2api/actions/runs/42254780186)：全部通过，包含组件 race、服务/仓库传输回归和嵌入前端的 Linux amd64 构建。日志确认新增“写入恢复后等待超时”“建连恢复后读取超时”两项用例通过，既有未恢复 TLS 超时阶段断言也通过。
- [完整 CI](https://github.com/kunpeng9w/sub2api/actions/runs/42254780109)：全部通过，包括后端全量单元及数据库集成测试、Lint、前端和部署脚本检查。
- [安全扫描](https://github.com/kunpeng9w/sub2api/actions/runs/42254780118)：通过。

本机仅编辑、gofmt 和 `git diff --check`，未运行编译或测试；未部署。原有 Nginx 和速度审计文件改动保留。
