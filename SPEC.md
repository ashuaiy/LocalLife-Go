# LocalLife-Go — SPEC

**Architecture:** Modular Monolith

**Primary Goal:** 本地生活服务后端 + 可验证工程优化

**Core Stack:** Go / Hertz / GORM / MySQL / Redis

> 当前交付范围（2026-09-18）：先匹配并迁移参考 Go 仓库的已实现业务，形成可运行基础版；性能优化与异步秒杀后置。第 10、11、13 节及最终优化冻结条件属于后续目标，不阻塞本轮基础版。当前验收以业务功能、构建/vet、真实依赖回归和运行文档为准，详见 [迁移清单](docs/migration.md)。

## 1. 产品边界

LocalLife-Go 是基于黑马点评业务模型的 Go 后端重构项目。按参考 Go 仓库匹配并迁移已有功能，适配当前分层、表结构和接口契约，保留必要的数据正确性与鉴权。

开发遵循：**先跑通基础业务闭环，再完成有验证依据的高价值优化，达到验收条件后冻结功能开发。**

核心模块包含：

- User
- Shop
- GEO
- Blog
- Like / Follow
- Feed
- Voucher
- Seckill

项目重点是缓存、高并发、数据一致性、Feed、可观测性与测试，不追求业务数量。

---

## 2. 非目标

P0/P1 禁止无理由引入：

```text
Microservices
Kafka
RabbitMQ
Elasticsearch
Kubernetes
Service Mesh
Distributed Transaction Framework
```

任何新增中间件必须先说明：

```text
当前问题
现有方案为什么不够
新增技术收益
新增复杂度
验证方法
```

---

## 3. 分层约束

### Handler

只做参数解析、校验、鉴权上下文、调用 Service、错误映射与 Response。

不得直接执行 SQL 或复杂 Redis 业务。

### Service

负责业务规则、流程、事务边界、缓存协调和幂等。

### Repository

负责 MySQL 持久化和查询，不依赖 HTTP DTO。

---

## 4. Context 约束

公开业务调用链使用：

```go
context.Context
```

```text
Handler → Service → Repository / Redis
```

后台 Stream Consumer 使用独立 lifecycle context。

---

## 5. 数据库要求

至少包含：

```text
User
ShopType
Shop
Blog
Follow
Voucher
SeckillVoucher
VoucherOrder
```

订单必须有：

```text
UNIQUE(user_id, voucher_id)
```

库存扣减：

```sql
UPDATE seckill_voucher
SET stock = stock - 1
WHERE voucher_id = ? AND stock > 0;
```

affected rows 为 0 代表库存不足。

---

## 6. Cache SPEC

### V1

```text
GET Redis
↓ miss
SELECT MySQL
↓
SET Redis
```

基础阶段已完成固定 30 分钟 TTL 的 V1，并保留冻结参考作为对照。当前缓存已演进为 V2：空值缓存、随机 TTL、逻辑过期、按 ID 合并重建，以及先更新数据库再删除缓存的维护命令。分类与列表直接查询 MySQL。接口约束见 [商户接口](docs/api/shop.md)，设计取舍见 [ADR 004](docs/adr/004-shop-cache-aside-v1.md)，前后对照见 [缓存验证](docs/performance/cache.md)。

### 防穿透

不存在对象写 30–45 秒空值；数据库故障不写空值。

### 防雪崩

TTL：

```text
base + jitter
```

### 热点击穿

热点对象：

```text
logical expiration
+
singleflight
```

正缓存逻辑刷新时间为 30–35 分钟，硬过期再晚 5 分钟。逻辑过期后允许先返回旧值，同进程同 ID 仅一个重建任务访问 DB。最多 64 个不同 ID 在途；重建预算 2 秒，失败退避最多延后 5 秒且不延长硬过期。缓存单次 I/O 100ms，超时失败退避另有最多 100ms 写入预算；服务关闭会取消并等待任务。

### 更新一致性

默认：

```text
Update DB
→ DEL cache
```

当前通过 `cmd/shop-update` 更新名称和地址，数据库提交后 DEL。Lua CAS 保护重建任务观察到的缓存记录不被旧任务覆盖，但不保证读取线性一致，也不能识别空 key 的更新竞争。失效失败需重试维护命令。P0 不默认实现延迟双删或 CDC。

---

## 7. GEO SPEC

Redis GEO 保存位置索引并做距离排序；MySQL 保存权威 Shop 实体。

已实现 `GET /api/v1/shops/nearby`，按分类、坐标、半径返回距离递增的商户候选。索引通过 `cmd/geo-rebuild` 从 MySQL 重建，以临时 key 加条目数校验后原子发布；未初始化索引返回 503。具体参数、恢复方法与一致性边界见 [GEO 接口](docs/api/geo.md)。

---

## 8. Feed SPEC

P0 使用 Push Model。

发布：

```text
Create Blog
→ Query Followers
→ ZADD feed:<follower>
```

读取使用：

```text
max score + equal-score offset
```

必须有重复/丢失测试。

当前已实现：发布同步推送、5 分钟分页快照、同分 offset 累积、当前关注状态过滤与单用户索引重建。快照固定候选集合，避免同分迟到推送或重建改变翻页位置。接口及恢复边界见 [关注 Feed](docs/api/feed.md)，成本与替代方案见 [ADR 005](docs/adr/005-push-feed-and-snapshot-cursors.md)。

---

## 9. Seckill V1

优惠券查询已实现：`GET /api/v1/vouchers`、`GET /api/v1/vouchers/:id`，详见 [优惠券接口](docs/api/voucher.md)。Seckill V1 已实现活动行锁、锁后数据库时间校验、RC 事务下单、本人订单查询，并通过重复购买、并发不超卖和失败回滚测试；接口见 [秒杀订单](docs/api/order.md)，决策见 [ADR 006](docs/adr/006-seckill-v1-transaction.md)。

```text
DB Transaction
→ Lock Activity + Validate Time
→ Duplicate Check
→ Conditional Stock Update
→ Create Order
```

验收：

```text
oversell = 0
duplicate user order = 0
```

---

## 10. Seckill V2

```text
HTTP
 ↓
Redis Lua
 ↓
XADD Stream
 ↓
Consumer Group
 ↓
MySQL Transaction
```

Lua 原子执行：

```text
check stock
check duplicate purchase
deduct Redis stock
record purchase marker
enqueue order event
```

Consumer 必须支持：

- Consumer Group
- retry
- pending recovery
- idempotency
- graceful shutdown
- error metrics

不能假设 exactly-once。

当前进度：内部 Lua 受理与回执组件、create-only 初始化和真实 Redis 并发/故障测试已实现；当前 HTTP 仍为同步 V1。异步模式/generation 的 MySQL 激活隔离、消费者事务落库、状态查询与恢复尚未接入，不能将内部组件当成完整 V2。设计和接入条件见 [ADR 006](docs/adr/006-seckill-v1-transaction.md#v2-增量redis-原子受理内部组件)。

---

## 11. 最终一致性

最终事实来源：

```text
MySQL
```

Redis 用于高并发入口和暂态状态。

P1 至少包含：

- retry
- pending recovery
- failed record
- 可说明的 compensation 策略

---

## 12. Authentication

P0：

```text
random token + Redis session
```

需要 TTL、中间件、User Context。

JWT 作为替代方案讨论，不同时实现两套。

当前接口：`POST /api/v1/auth/code`、`POST /api/v1/auth/login`、`POST /api/v1/auth/logout`、`GET /api/v1/users/me`。

实现约束：

- 手机号为 11 位中国大陆手机号格式，验证码为 6 位数字。
- 开发验证码仅在 `AUTH_DEV_CODES=true` 且 HTTP 绑定回环地址时启用；默认关闭，尚未接入真实短信服务。
- 验证码默认有效期 5 分钟、重发冷却 1 分钟、最多 5 次错误尝试，校验成功后原子删除。
- Token 由 32 字节密码学随机数生成，Redis 使用 Token 的 SHA-256 作为 key 的一部分；Session 默认固定有效期 30 分钟，读取不续期。
- 首次登录基于手机号唯一索引创建用户；并发登录不创建重复账号。
- 鉴权通过 Authorization Bearer Header，拒绝从 URL 查询参数读取 Token。
- 凭据无效、过期或已退出返回 401；依赖故障返回 503，请求超时返回 504。
- 认证接口响应设置 `Cache-Control: no-store`，日志不记录手机号、验证码或 Token。

请求与响应示例见 [认证接口](docs/api/auth.md)。

---

## 13. Observability

### Logs

至少：

```text
request_id
method
path
latency
status
error
```

不得记录 token、验证码等敏感信息。

### Metrics

必须：

```text
HTTP count / latency / error
cache hit / miss
seckill accepted / rejected
stream processed / failed
```

### Tracing

P2：

```text
HTTP → Service → Redis / MySQL
```

---

## 14. Testing

### Unit

核心 Service、Cache、Feed、Lua 映射、错误映射。

### Integration

至少：

1. login/session
2. shop cache
3. GEO
4. Feed
5. Seckill V1
6. Seckill V2
7. Consumer idempotency

### Race

```bash
go test -race ./...
```

### Benchmark

至少：

```text
cache read
feed query
seckill Lua path
```

---

## 15. 秒杀压测验收

设库存为 `N`，并发压测后必须满足：

```text
unique successful orders <= N
DB stock >= 0
duplicate order = 0
```

记录：

```text
QPS
P95
P99
error rate
accepted count
final order count
```

---

## 16. 优化规则

任何优化前记录：

```text
Problem
Baseline
Change
Expected Impact
Validation
```

优化完成后补：

```text
Actual Result
Trade-off
```

没有数据支撑时，不写具体性能提升百分比。

---

## 17. ADR 规则

下列变化必须写 ADR：

- cache strategy
- seckill architecture
- feed model
- auth strategy
- new middleware
- service split

格式：

```text
Context
Decision
Alternatives
Why not alternatives
Consequences
When to revisit
```

---

## 18. AI 开发约束

允许 AI 完成大量实现，但一次只做一个 bounded task。

例如：

```text
implement shop cache V1
```

而不是：

```text
modernize entire project
```

每次修改后至少：

```bash
go test ./...
go vet ./...
```

涉及并发时：

```bash
go test -race ./...
```

AI 不得自行：

- 拆微服务
- 更换框架
- 引入大型中间件
- 全仓库重构
- 编造 Benchmark
- 声称未运行测试通过

---

## 19. 实现顺序

业务闭环优先于全面优化。每次完成一个可运行、可验证的模块，然后推进下一模块；不能因为某一模块仍可优化就长期停留。基本校验、数据正确性、必要安全措施和测试随实现完成，不能推迟到最后。

当前基础业务已跑通，包括 User、Shop、GEO、Blog/Like、Follow/Feed、Voucher 与 Seckill V1。缓存治理与 [Seckill V1 HTTP 负载基线](docs/performance/seckill.md) 保留。当前先完成参考业务迁移与基础版验收；Seckill V2、恢复验证和新性能优化后续再做。完整范围与剩余验收项见 [实现进度](docs/progress.md)。

### Phase 0 — Bootstrap

```text
Config
Hertz
MySQL
Redis
Error / Response
Migrations
```

### Phase 1 — Basic Business

目标：可以完整演示登录、查询商户、附近商户、发布探店、点赞关注、读取 Feed、查询优惠券及事务下单。

先实现简单可靠的版本：商户使用 Cache Aside；Feed 使用 Push Model 与正确的滚动分页；秒杀使用 MySQL 事务、条件扣库存和唯一索引。暂不提前实现热点逻辑过期或秒杀异步化。

```text
User
Shop
GEO
Blog
Follow
Feed
Voucher
Seckill V1
```

### Phase 2 — High-value Optimization（后续）

基础业务闭环完成后，再围绕已记录的问题和基线做优化。重点限定为缓存治理、秒杀异步化及一致性恢复；每项优化都要有测试或前后对照数据，不增加与现有问题无关的中间件。

```text
Cache resilience
Seckill V2
Consumer idempotency
Consistency recovery
```

### Phase 3 — Engineering

这是验收收尾，不是无限扩展阶段。基础日志和正确性测试随各模块交付；此阶段集中补齐关键指标、链路分析、负载与性能证据、CI。正式短信供应商、支付、运营后台等不作为当前业务闭环的前置条件。

```text
Logging
Metrics
Tracing
Tests
Benchmark
CI
```

---

## 20. Commit 规范

建议：

```text
feat(auth): add redis session authentication
feat(shop): implement cache aside
feat(feed): add zset push feed
feat(seckill): add transactional ordering
perf(cache): add logical expiration and singleflight
perf(seckill): add lua and redis stream
obs: add prometheus metrics
test: add integration tests
```

---

## 21. 验收标准

### Build

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

### Business

登录、商户、GEO、Blog、Follow、Feed、Voucher、Seckill 均可运行。

### Cache

可验证：

```text
hit
miss
null cache
logical-expiration rebuild
```

### Seckill

并发压测：

```text
oversell = 0
duplicate = 0
```

异步订单最终正确落库。

### Feed

滚动分页无明显重复或丢失。

### Engineering

至少：

```text
structured logs
key metrics
integration tests
race test
benchmark
CI
```

---

## 22. 最终优化版 Freeze 条件（不阻塞本轮基础版）

完成：

```text
基础业务
+ 缓存优化
+ 秒杀 V2
+ Feed
+ Metrics
+ Integration Test
+ Race / Benchmark
+ 3~5 篇 ADR
```

即冻结功能开发。

冻结后不继续增加业务模块、中间件或无明确问题的优化。只有影响现有功能正确性、稳定性或证据可信度的缺陷才继续修改；新的扩展方向单独记录，不自动纳入当前范围。

随后重点转向稳定性维护：

```text
缺陷修复
性能回归
故障恢复验证
架构与运行文档维护
```

---

## 23. 设计评审要求

每个核心设计需要说明：

```text
怎么设计？
为什么这样设计？
为什么用这个技术？
能不能换？
替代方案有什么代价？
失败怎么办？
流量 10 倍怎么办？
如何验证有效？
```

设计说明应包含实现依据、替代方案、故障处理与验证结果。
