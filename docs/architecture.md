# 架构与模块边界

当前已跑通模块化单体的启动基础、认证、商户、GEO、图文笔记、点赞关注、Feed、优惠券与事务下单，并完成商户缓存治理。已补齐公开资料、签到、共同关注、热门笔记、图片上传与点赞通知；异步秒杀及故障恢复后置。

```text
cmd/server
  -> config.Load：环境变量解析、类型及边界校验
  -> platform.Open：有超时的 MySQL / Redis Ping、连接池
  -> app.Serve：绑定端口、运行 Hertz、监听生命周期取消
      -> middleware.HTTP：Request ID、协作式 deadline、恢复、访问日志
      -> /healthz、/readyz、统一 404 / 405
      -> Auth Handler -> Auth Service -> User Repository / Redis Auth Store
      -> Shop Handler -> Shop Service -> Shop Repository / Redis Shop Cache
      -> GEO Handler -> GEO Service -> Redis GEO / Shop Repository 批量查询
      -> Blog Handler -> Blog Service -> Blog Repository / MySQL 点赞关系
                         -> Feed Service -> Follow Repository / Redis Feed 推送
      -> Follow Handler -> Follow Service -> Follow / User Repository
      -> Feed Handler -> Feed Service -> Redis 快照分页 / Blog Repository 批量回填
      -> Voucher Handler -> Voucher Service -> Voucher Repository / MySQL 活动关联查询
      -> Order Handler -> Order Service -> Order Repository / MySQL 事务与本人订单查询
      -> Community Handler -> Community Service -> 公开资料/热门/共同关注 Repository
                           -> Redis 签到 Bitmap / 点赞通知 Stream；Media Service -> 本地图片
      -> response：HTTP 状态与 JSON envelope

cmd/migrate up
  -> config.Load
  -> migration.Up
      -> 嵌入 SQL、MySQL advisory lock、schema_migrations

cmd/geo-rebuild
  -> MySQL 按分类读取商户 -> Redis 临时位置索引 -> 校验并原子发布

cmd/feed-rebuild -user-id ID
  -> MySQL 当前关注及内容 -> Redis 临时 Feed 索引 -> 校验并原子发布

cmd/shop-update -id ID -name NAME
  -> MySQL 事务更新商户名称/地址 -> 提交后删除 Redis 详情缓存
```

## 目录职责

| 目录 | 当前职责 |
|---|---|
| `cmd/server` | 配置加载、signal context、依赖装配与关闭 |
| `cmd/migrate` | 独立迁移命令，不启动 HTTP |
| `cmd/geo-rebuild` | 从 MySQL 重建 GEO 索引，不启动 HTTP |
| `cmd/feed-rebuild` | 受控重建指定读者的 Feed 索引，不启动 HTTP |
| `cmd/shop-update` | 更新商户名称与地址，提交后删除缓存，不启动 HTTP |
| `internal/app` | HTTP 路由与生命周期 |
| `internal/config` | 有默认值的环境配置，错误只包含变量名和规则 |
| `internal/platform` | GORM、database/sql、go-redis 实例及连接池 |
| `internal/migration` | 迁移执行、锁、资源释放 |
| `internal/middleware` | Request ID、deadline、恢复、访问日志 |
| `internal/handler` | 认证、商户、内容、关注、Feed 与优惠券请求解析、校验、响应映射 |
| `internal/service` | 认证、商户缓存与 GEO、内容发布、点赞关注、Feed 推送、优惠券状态、下单事务编排与分页 |
| `internal/repository` | GORM 持久化与查询、点赞关注关系、Service 事务端口的 SQL 实现 |
| `internal/cache` | Redis 验证码脚本、会话、商户详情缓存、GEO 索引、Feed 快照、异步秒杀受理与订单队列 |
| `internal/model` | 数据库实体 |
| `pkg/apperror` | 与业务实现无关的错误分类及公开消息 |
| `pkg/response` | `code/message/data/request_id` HTTP envelope |
| `migrations` | 版本化 SQL 与编译时嵌入资源，当前十张业务表 |
| `tests/integration` | 真实 MySQL/Redis 的显式 opt-in 测试 |

Handler → Service → Repository 传递 `context.Context`，GORM 使用 `WithContext(ctx)`，Redis 调用带取消和超时的 context。商户共享重建任务由服务生命周期管理；请求取消只结束该请求的等待。Handler 不直接执行 SQL。健康检查是基础设施入口，不承载业务。

## 请求与生命周期

`X-Request-ID` 仅接受 1–64 字符的 ASCII 字母、数字、点、短横线和下划线，其余由 `crypto/rand` 重新生成；ID 同时进入 response header、JSON 和 typed context。

HTTP deadline 是协作式取消：Handler 和底层 I/O 必须遵守 context。它不能强制终止不检查 context 的 CPU 循环。没有为每次请求额外启动超时 goroutine，避免在请求结束后访问 Hertz 池化的 RequestContext。

启动时先验证配置和依赖，再监听 HTTP；端口冲突通过返回值传播到非零退出码。监听器进入 Accept 后才执行常规关闭，避免刚启动即取消时与 Hertz 初始化竞争。关闭时先排空 HTTP，再取消并等待商户后台重建，最后关闭依赖。HTTP 排空超时返回错误；后台关闭或连接池关闭错误记录日志。

商户详情使用带空值标记、逻辑刷新时间和硬过期时间的缓存记录。Service 按 ID 合并冷查询和后台刷新，最多同时处理 64 个不同 ID；单次缓存 I/O 有 100ms context 预算，重建共用 2 秒预算，失败退避可另用最多 100ms 写入。Cache 层用 Lua 比较重建任务读到的原始 JSON 后再发布；更新采用先提交数据库、再删除缓存。该设计提供有界旧值与尽力失效，不提供跨进程合并或线性一致性，详见 [缓存契约](api/shop.md#缓存一致性边界)。

访问日志包含 `request_id/method/path/latency_ms/status/error`。不输出 query string、请求体、Authorization、配置密码或原始 panic 值。公开响应只显示稳定错误码和安全消息。Hertz 自身仍保留框架诊断日志；完整日志适配、Metrics、Trace、pprof 放在后续工程阶段。

## 数据库约定

- MySQL 保存业务实体与关系；Redis 保存验证码、Session、商户缓存、GEO 与 Feed 索引。GEO/Feed 索引可从 MySQL 重建；签到 Bitmap 和点赞通知独立保存在 Redis，依赖其持久化。
- 时间使用 UTC，`DATETIME(3)` 保留毫秒；金额使用整数分。
- `voucher_order` 有 `UNIQUE(user_id, voucher_id)`；`seckill_voucher` 有非负库存和有效时间窗约束。
- 秒杀 Service 在 RC 事务内编排活动锁、锁后数据库时间校验、重复检查、条件扣库存和订单创建；Repository 实现事务端口，失败回滚，提交成功才返回订单。
- 异步活动通过同一活动锁切换模式；Redis Lua 受理与 Stream 排队，独立消费者将库存、订单、结果写入同一个 MySQL 事务。最终结果持久化在 SQL，HTTP 鉴权仍依赖 Redis；尚未落库的受理依赖 Redis 持久化，详见 [ADR 007](adr/007-async-seckill.md)。
- 显式外键保证关系完整性。业务增长后若要移除外键，必须通过新迁移和 ADR 说明一致性责任如何转移。
- 普通应用连接关闭 `multiStatements`；迁移单独连接开启它。
- 不调用 GORM AutoMigrate。DDL 失败可能产生部分已提交表，迁移库会保留 dirty 标记；修复前检查实际 schema，不自动 force 或删除库。
- 迁移 CLI 当前只开放 `migrate up`；down SQL 用于审查和受控回滚，不由启动流程执行。

设计取舍见 [模块化单体](adr/001-modular-monolith.md)、[启动与迁移](adr/002-bootstrap-lifecycle-and-migrations.md)、[Redis Session](adr/003-redis-session-auth.md)、[商户缓存](adr/004-shop-cache-aside-v1.md)、[Feed 快照](adr/005-push-feed-and-snapshot-cursors.md) 与 [事务秒杀](adr/006-seckill-v1-transaction.md)。各业务接口入口见 [README](../README.md#当前接口)。

## 官方参考

- [Hertz Engine 与服务配置](https://www.cloudwego.io/docs/hertz/tutorials/basic-feature/engine/)
- [GORM 连接数据库](https://gorm.io/docs/connecting_to_the_database.html)
- [go-redis 使用文档](https://redis.io/docs/latest/develop/clients/go/)
- [golang-migrate](https://github.com/golang-migrate/migrate)

版本固定在 `go.mod` / `go.sum`，实现同时依据对应版本源码验证。
