# 账号绑定代理能否保证故障即停

检查对象：本机实际版本 Sub2API v0.2.18（ac1b8f1271be0d674fa6f074403c1410f30a3a81）。检查日期：2026-10-24，Asia/Singapore。

用户目标：关闭全局 TUN 后，某个账号的所有上游请求只经过指定代理；代理不可用时停止请求，不改走服务器公网出口。

结论：正常模型传输路径符合“代理不通则报错”，但当前版本单靠账号绑定不能保证全部异常路径严格故障即停。发现授权刷新读取代理记录失败后仍继续请求的分支。未关闭现有代理，未修改账号、服务或配置，未进行真实上游故障注入。

## 已确认的正常路径

- HTTP 普通请求与 SSE：从账号取代理 URL，传入上游 HTTP 客户端；代理连接失败返回错误，没有发现失败后清空代理 URL 再直连的逻辑。
- WebSocket：代理 URL 非空时使用绑定代理的专用 HTTP Transport，客户端初始化或握手失败返回错误；没有发现连接失败后直连的逻辑。
- OAuth 刷新：成功读取账号代理记录时，代理 URL 传入刷新客户端；该代理网络不可用时请求报错。
- 连接池按代理参数区分；WebSocket 的连接请求匹配也包含 ProxyURL，不是任意复用直连连接。

因此“代理记录正常存在，但代理服务崩溃或端口不可达”这个具体场景，已检查的路径预期会失败，不会因为连接错误自动改成直连。网络超时或重试可能使失败返回有延迟；已建立的连接也不等于立即被取消。

源码：

- `backend/internal/service/openai_gateway_forward.go:1057`
- `backend/internal/service/openai_gateway_passthrough.go:349`
- `backend/internal/repository/http_upstream.go:203`
- `backend/internal/service/openai_ws_client.go:135`
- `backend/internal/service/openai_ws_pool.go:2335`
- `backend/internal/repository/openai_oauth_service.go:100`

## 不能保证“所有请求”的具体缺口

自动刷新调用 `OpenAITokenRefresher.Refresh`，随后进入 `OpenAIOAuthService.RefreshAccountToken`。其中代理解析逻辑相当于：

```go
proxyURL := ""
if account.ProxyID != nil && proxyRepo != nil {
    proxy, err := proxyRepo.GetByID(ctx, *account.ProxyID)
    if err == nil && proxy != nil {
        proxyURL = proxy.URL()
    }
}
return RefreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID)
```

已经绑定 ProxyID，但是查询出错或结果为空时，不会返回错误，而是继续传入空代理 URL。下层 `getSharedReqClient` 对空 URL 不调用 SetProxyURL，使用默认客户端出站；在用户假设的“没有全局 TUN、没有其他环境代理、服务器直连可达”的条件下，就存在直接联系授权服务器的路径。这是源码确认的可达风险分支，不代表当前部署已经发生泄漏。

后台刷新后的隐私设置辅助请求也存在类似的代理查询错误忽略逻辑。模型转发的一些取代理分支在 `ProxyID != nil` 但关联 `Proxy == nil` 时同样保留空 URL，不过调度与数据完整性检查可能提前拦截这种状态；本次不将其描述为已复现泄漏。

证据位置：

- `backend/internal/service/token_refresher.go:120`
- `backend/internal/service/openai_oauth_service.go:347`
- `backend/internal/repository/req_client_pool.go:63`
- `backend/internal/service/token_refresh_service.go:1459`

## 另一个明确的配置例外

代理到期回退支持 `none`、`proxy`、`direct`。当设置 `direct` 且到期扫描命中时，程序会把绑定账号的 `proxy_id` 改为 NULL；关闭 TUN 后，后续请求就可能直连。

用户要求只使用同一代理时，应设置“不回退 / none”，不要选择直连或其他备用代理。这个选项主要针对到期处理，不能单独修复上述授权刷新缺口。

源码：`backend/internal/service/proxy_fallback.go:12`，`backend/internal/repository/proxy_repo.go:765`。

`security.proxy_fallback.allow_direct_on_error=false` 只作用于更新检查、定价数据等辅助服务，不能作为授权刷新查询失败的总开关。

## 达到用户目标所需的条件

1. 账号绑定指定代理，并设置到期不回退。
2. 对要求代理的账号，统一强制代理解析：查询出错、记录不存在、配置无效或关联缺失时直接报错；不把错误转换为空代理 URL。覆盖普通请求、SSE、WebSocket、OAuth 刷新及携带账号凭据的辅助请求。
3. 在操作系统或独立网络空间限制 Sub2API 出站，只允许访问指定代理及必要的本机服务。这样即使应用遗漏代理，也不能直接联系公网。不同账号需要各自严格隔离时，可使用独立实例或网络空间建立各自的出口边界。
4. 通过受控故障验收：正常时确认代理出口；模拟端口拒绝、代理超时、代理查询失败及到期后，确认请求失败且无直接上游连接。停代理时的测试需覆盖新连接，不能仅观察已有长连接。

使用同一代理与供应商保证固定公网出口 IP 是两件事；后者仍取决于代理服务本身。
