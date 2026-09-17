# 用户认证接口

基础路径：`/api/v1`。响应统一为 `code/message/data/request_id`，所有认证响应使用 `Cache-Control: no-store`。

请求体使用 `Content-Type: application/json`，不接受未知字段、多个 JSON 文档或超过 1 KiB 的请求体。手机号为 11 位中国大陆手机号格式；ID 在 JSON 中使用字符串。

## 获取开发验证码

`POST /auth/code`

```json
{"phone":"13800138000"}
```

成功的 `data`：

```json
{"expires_in":300,"dev_code":"123456"}
```

`dev_code` 只用于本地开发。默认 `AUTH_DEV_CODES=false`，短信通道未接入时返回 503；本地显式开启后才能使用此流程，禁止绑定公网或所有网卡地址。

验证码默认有效期 5 分钟、同一手机号重发冷却 1 分钟。冷却期重发返回 429。每个验证码最多允许 5 次错误尝试，超过后失效；校验成功后立即删除，只能使用一次。

## 登录

`POST /auth/login`

```json
{"phone":"13800138000","code":"123456"}
```

成功的 `data`：

```json
{
  "token":"<随机生成的 Token>",
  "token_type":"Bearer",
  "expires_in":1800,
  "user":{"id":"1","nickname":"用户_...","avatar":""}
}
```

首次登录创建用户，后续登录使用同一用户 ID 并生成新 Token；不同登录会话可同时有效。会话固定有效期 30 分钟，访问接口不会延长。验证码错误、过期、已使用或尝试耗尽均返回 401。

如果验证码已消费而后续 MySQL/Redis 写入失败，客户端需要在冷却期结束后申请新验证码；不会恢复旧验证码。

## 当前用户

`GET /users/me`

```http
Authorization: Bearer <token>
```

返回 `id/nickname/avatar`，不返回手机号。缺失、无效、过期或已注销 Token 返回 401。用户 ID 由鉴权中间件写入 typed context，再传给业务层。

## 退出登录

`POST /auth/logout`，携带同样的 Authorization Header，无请求体。

删除当前 Token 对应的 Redis Session。成功时 `data` 为 `null`；该 Token 随后不能继续查询当前用户。其他登录会话不受影响，重复使用已注销 Token 返回 401。

## 错误响应

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 请求格式或字段无效 |
| 401 | unauthorized | 凭据或会话无效 |
| 429 | rate_limited | 验证码重发过于频繁 |
| 503 | dependency_failure | MySQL/Redis 不可用，或验证码通道未启用 |
| 504 | timeout | 请求超过 deadline |

不通过 query string 或 Cookie 接受 Token。数据库错误和凭据不会包含在公开错误消息或访问日志中。

## 本地运行

在项目根目录的 PowerShell 中执行：

```powershell
if (!(Test-Path .env)) { Copy-Item .env.example .env }
. .\scripts\env.ps1
docker compose up -d --wait
go run ./cmd/migrate up
$env:AUTH_DEV_CODES='true'
go run ./cmd/server
```

默认绑定 `127.0.0.1:8080`。若当前终端找不到 docker，请重新打开终端，或将 Docker Desktop 的 `resources/bin` 目录加入本次终端 PATH。

另开终端完成流程：

```powershell
$base='http://127.0.0.1:8080/api/v1'
$phone='13800138000'
$code=Invoke-RestMethod -Method Post "$base/auth/code" -ContentType 'application/json' -Body (@{phone=$phone} | ConvertTo-Json)
$login=Invoke-RestMethod -Method Post "$base/auth/login" -ContentType 'application/json' -Body (@{phone=$phone;code=$code.data.dev_code} | ConvertTo-Json)
$headers=@{Authorization="Bearer $($login.data.token)"}
Invoke-RestMethod "$base/users/me" -Headers $headers
Invoke-RestMethod -Method Post "$base/auth/logout" -Headers $headers
```

配置项见 `.env.example`；验证码 TTL、冷却、尝试次数和会话 TTL 均可配置。认证相关时长至少为 1 秒。
