# 异步秒杀

默认活动仍走 [同步事务下单](order.md)。异步模式通过本地受控命令启用，只接受没有任何历史订单、库存大于 0、尚未结束的活动。未开始的活动也可提前启用；启用不会提前开放购买。

## 受理与查询

路径前缀 `/api/v1`，两个接口均需 `Authorization: Bearer <token>`，返回统一 envelope 与 `Cache-Control: no-store`。用户身份仅来自 Session，URL 中的 `user_id` 不改变查询对象。

`POST /vouchers/:id/seckill-async`，不带请求体。受理成功返回 **HTTP 202**，`data` 示例：

```json
{"voucher_id":"21","ticket":"1790000000000-0","status":"pending"}
```

202 只表示 Redis 已预留库存并写入消息。此时没有订单 ID，也不代表已写入 MySQL。活动未启用异步模式返回 `409 conflict`；未开始、已结束、已受理过、售罄分别返回对应的 `409 activity_not_started / activity_ended / already_purchased / sold_out`。

`GET /vouchers/:id/seckill-result` 返回本人结果：

```json
{"voucher_id":"21","ticket":"1790000000000-0","status":"created","order_id":"42"}
```

| status | 含义 |
| --- | --- |
| pending | Redis 中有受理记录，MySQL 尚无最终结果 |
| created | 订单、库存扣减和结果均已提交，`order_id` 可用于查询本人订单 |
| failed | MySQL 已记录终态失败，`reason` 为 `user_not_found`、`already_purchased`、`sold_out` 或 `outside_activity_window` |

终态失败记录可能先于 Redis 补偿完成；消费者会继续重试补偿和消息确认。失败后仍保留该用户的购买去重标记，本活动内不允许重新受理该用户。

没有本人的受理记录时为 `404 not_found`。非法 ID/请求体为 400，未登录为 401，依赖故障为 503，超时为 504。业务查询先读 MySQL，已有最终结果无需读取 Redis 订单状态；但 HTTP 的 Session 鉴权仍依赖 Redis。待处理票据无法核实或 generation 不一致时返回 503。

受理响应丢失、请求超时或重复 POST 时，先查询结果。不要把超时视为必然失败，也不要改用同步入口重试。重复 POST 返回 409，客户端用结果接口恢复票据。

## 启用与运行

先配置并启动开发依赖、执行数据库迁移及 HTTP 服务（见 [本地运行](../quickstart.md)）。启用前应将所有 API 实例升级到支持模式检查的版本，并停止旧版本进程；旧版本不识别模式字段，不能与异步活动混跑。活动仍有受理或待处理消息时，不应回滚应用到旧版或执行迁移 4 的 down。随后在加载相同环境变量的终端执行：

```powershell
. .\scripts\env.ps1
go run ./cmd/migrate up
go run ./cmd/seckill -mode enable -voucher-id 21
go run ./cmd/seckill -mode worker
```

将 `21` 替换为实际未使用的秒杀券 ID。消费者需作为独立进程持续运行；Ctrl+C 会取消当前操作并退出，然后释放连接池，未确认消息由后续消费者接管。允许多个消费者进程，进程启动自动生成独立名称。

启用按 `sync(0) → initializing(1) → active(2)` 进行。阶段 1 已阻止同步下单，但不开放异步受理；命令中断时重复相同启用命令即可继续核验、激活。状态 2 下重复命令只检查 Redis，不重置库存。没有自动关闭异步模式或恢复同步写入的命令；启用后不得直接修改该活动的库存、时间窗或 generation。

消费者每轮按 ID 分页扫描活动，每活动最多读取 20 条新消息及接管 20 条 Pending。默认空闲轮询间隔 200ms；每条处理及每次依赖操作最多 5 秒。Pending 最低空闲 30 秒后可被 `XAUTOCLAIM` 接管，游标跨轮次推进；30 秒是接管门槛，并非恢复时延承诺。

## 故障处理与边界

- 订单、扣库存、结果属于一个 SQL 事务；提交结果不确定时保留 Pending，重试先查同一事件的持久化结果，避免重复下单。
- 成功结果提交后才 `XACK`。终态失败先提交失败记录，再原子回补 Redis 库存并标记已补偿，最后 `XACK`。数据库暂时故障不会直接回补库存。
- 解析失败的消息保留 Pending 并记录错误，同批有效消息继续处理；没有自动死信删除。检查日志中的 `voucher_id`、`event_id` 后人工核对。
- 消费者每 30 秒及退出时输出累计投递处理数、重试数、队列错误数。这些不是唯一订单数，也不是 Prometheus 指标。活动级 Stream 的 `XPENDING`、`XINFO GROUPS` 可用于检查积压。
- Redis state/Stream 丢失、类型损坏、generation 不符或消费组缺失时拒绝受理。已激活活动不根据 SQL 剩余库存自动重建 Redis；需要暂停入口、人工对账和恢复，当前没有自动修复工具。
- Redis 受理仍受其持久化与故障恢复策略约束，不能保证掉电或回滚备份后未落库的票据零丢失。开发 Compose 启用 AOF，测试实例故意不持久化；这不是跨系统原子提交。
- 订单 Stream 不自动过期、不裁剪，买家及补偿标记也保留；结束活动仍被扫描，以便处理迟到消息。大规模活动需补充归档、消费者元数据清理和告警。
- 券详情的库存/状态来自 MySQL，异步预留尚未消费时可能显示仍有库存；最终是否受理由 Lua 判断。
- 券详情暂不返回活动的同步/异步模式，客户端需按活动配置选择入口；错误入口返回 409，不自动切换。

当前受理仍读取 MySQL 的活动模式与 generation。吞吐收益需以相同负载比较“受理耗时”和“订单最终提交耗时”，不能用 Lua 执行时间代替端到端性能。
