# 架构与模块边界

当前包含模块化单体的启动基础和用户认证模块。商户、Feed、优惠券与秒杀等业务仍按模块逐步增加。

```text
cmd/server
  -> config.Load：环境变量解析、类型及边界校验
  -> platform.Open：有超时的 MySQL / Redis Ping、连接池
  -> app.Serve：绑定端口、运行 Hertz、监听生命周期取消
      -> middleware.HTTP：Request ID、协作式 deadline、恢复、访问日志
      -> /healthz、/readyz、统一 404 / 405
      -> Auth Handler -> Auth Service -> User Repository / Redis Auth Store
      -> response：HTTP 状态与 JSON envelope

cmd/migrate up
  -> config.Load
  -> migration.Up
      -> 嵌入 SQL、MySQL advisory lock、schema_migrations
```

## 目录职责

| 目录 | 当前职责 |
|---|---|
| `cmd/server` | 配置加载、signal context、依赖装配与关闭 |
| `cmd/migrate` | 独立迁移命令，不启动 HTTP |
| `internal/app` | HTTP 路由与生命周期 |
| `internal/config` | 有默认值的环境配置，错误只包含变量名和规则 |
| `internal/platform` | GORM、database/sql、go-redis 实例及连接池 |
| `internal/migration` | 迁移执行、锁、资源释放 |
| `internal/middleware` | Request ID、deadline、恢复、访问日志 |
| `internal/handler` | 认证请求解析、校验、响应映射 |
| `internal/service` | 验证码、登录、Session 与用户查询流程 |
| `internal/repository` | GORM 用户持久化与并发注册去重 |
| `internal/cache` | Redis 验证码脚本和固定 TTL 会话 |
| `internal/model` | 数据库实体 |
| `pkg/apperror` | 与业务实现无关的错误分类及公开消息 |
| `pkg/response` | `code/message/data/request_id` HTTP envelope |
| `migrations` | 八张表的 SQL 和编译时嵌入资源 |
| `tests/integration` | 真实 MySQL/Redis 的显式 opt-in 测试 |

Handler → Service → Repository 沿用 `context.Context`，GORM 使用 `WithContext(ctx)`，Redis 调用传入同一个 ctx。Handler 不直接执行 SQL。健康检查是基础设施入口，不承载业务。

## 请求与生命周期

`X-Request-ID` 仅接受 1–64 字符的 ASCII 字母、数字、点、短横线和下划线，其余由 `crypto/rand` 重新生成；ID 同时进入 response header、JSON 和 typed context。

HTTP deadline 是协作式取消：Handler 和底层 I/O 必须遵守 context。它不能强制终止不检查 context 的 CPU 循环。没有为每次请求额外启动超时 goroutine，避免在请求结束后访问 Hertz 池化的 RequestContext。

启动时先验证配置和依赖，再监听 HTTP；端口冲突通过返回值传播到非零退出码。监听器进入 Accept 后才执行常规关闭，避免刚启动即取消时与 Hertz 初始化竞争。关闭时先排空 HTTP，再关闭依赖；超时作为错误返回。

访问日志包含 `request_id/method/path/latency_ms/status/error`。不输出 query string、请求体、Authorization、配置密码或原始 panic 值。公开响应只显示稳定错误码和安全消息。Hertz 自身仍保留框架诊断日志；完整日志适配、Metrics、Trace、pprof 放在后续工程阶段。

## 数据库约定

- MySQL 是权威数据来源；Redis 当前保存验证码与 Session，并支持连通性检查。
- 时间使用 UTC，`DATETIME(3)` 保留毫秒；金额使用整数分。
- `voucher_order` 有 `UNIQUE(user_id, voucher_id)`；`seckill_voucher` 有非负库存和有效时间窗约束。
- 显式外键保证关系完整性。业务增长后若要移除外键，必须通过新迁移和 ADR 说明一致性责任如何转移。
- 普通应用连接关闭 `multiStatements`；迁移单独连接开启它。
- 不调用 GORM AutoMigrate。DDL 失败可能产生部分已提交表，迁移库会保留 dirty 标记；修复前检查实际 schema，不自动 force 或删除库。
- `migrate up` 是当前唯一 CLI 操作；down SQL 用于审查和受控回滚，不由启动流程执行。

设计取舍见 [ADR 001](adr/001-modular-monolith.md)、[ADR 002](adr/002-bootstrap-lifecycle-and-migrations.md) 与 [ADR 003](adr/003-redis-session-auth.md)。接口契约见 [用户认证接口](api/auth.md)。

## 官方参考

- [Hertz Engine 与服务配置](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/engine/)
- [GORM 连接数据库](https://gorm.io/docs/connecting_to_the_database.html)
- [go-redis 使用文档](https://redis.io/docs/latest/develop/clients/go/)
- [golang-migrate](https://github.com/golang-migrate/migrate)

版本固定在 `go.mod` / `go.sum`，实现同时依据对应版本源码验证。
