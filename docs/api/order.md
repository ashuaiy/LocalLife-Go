# 秒杀下单与个人订单

基础路径 `/api/v1`，所有接口需要 `Authorization: Bearer <token>`，响应设置 `Cache-Control: no-store`。统一返回 `code/message/data/request_id`；ID 使用 JSON 字符串。

## Seckill V1 下单

`POST /vouchers/:id/seckill`，无请求体。`:id` 为秒杀券 ID，购买用户只来自登录身份，不能通过请求指定其他用户。

成功返回 200，表示 MySQL 事务已提交，`data` 示例：

```json
{
  "id":"100",
  "user_id":"7",
  "voucher_id":"21",
  "status":1,
  "created_at":"2026-09-17T14:00:00.123Z"
}
```

`status: 1` 表示已创建订单，不代表支付完成。每个用户对同一张券最多产生一笔订单，每笔订单扣减一份库存；当前没有支付、取消或退款接口。

事务流程：

1. `READ COMMITTED` 事务内锁定 `seckill_voucher` 活动行。
2. 获取锁后读取 MySQL `UTC_TIMESTAMP(3)`，校验 `[begin_time, end_time)`。
3. 查询当前用户是否已有该券订单。
4. 执行 `UPDATE ... SET stock = stock - 1 WHERE voucher_id = ? AND stock > 0`，影响行数必须为 1。
5. 插入订单，提交成功后才返回成功。`UNIQUE(user_id, voucher_id)` 为最终重复购买约束。

任一步骤失败都会回滚本事务。普通券、缺失的券或无秒杀活动的券返回 404。活动时间优先于重复购买和库存判断；同一活动仍有效时，已有订单返回 `already_purchased`，即使库存已售罄。

资格以获取活动锁后的数据库时间为准。等待期间活动结束会拒绝请求；已在有效区间完成校验的请求可能在结束时刻之后提交。客户端查询到的活动状态不预留库存，服务器会在每次下单时重新检查。

请求超时、连接断开或提交确认失败时，客户端可能无法确认事务是否提交。先通过 `GET /orders?voucher_id=:id` 检查自己的订单再决定是否重试。重复请求不再次扣库存，已存在订单时返回 409；当前不会自动重试数据库事务。

## 查询订单

`GET /orders/:id` 只返回当前用户的订单，结构同上。不属于当前用户和不存在的订单统一返回 404。

`GET /orders?voucher_id=21&page=1&page_size=10` 返回当前用户的订单列表，按订单 ID 倒序：

| 参数 | 默认值 | 约束 |
| --- | --- | --- |
| voucher_id | 不筛选 | 若提供，必须为 uint64 正整数 |
| page | 1 | 1–1000 |
| page_size | 10 | 1–50 |

```json
{"items":[],"page":1,"page_size":10,"has_more":false}
```

多读一条确定 `has_more`。无记录返回 `items: []`，使用 offset 分页，不提供跨页快照；并发新增订单可能改变后续页面。查询参数中的 `user_id` 不参与身份选择，所有查询都带当前用户条件。未知参数忽略，已知参数为空、重复、非法或溢出返回 400。

## 错误响应

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 非法 ID、分页参数或下单请求携带请求体 |
| 401 | unauthorized | 未登录、凭据无效或已退出 |
| 404 | not_found | 活动不存在，或订单不存在/不属于当前用户 |
| 408 | request_canceled | 请求取消 |
| 409 | activity_not_started | 活动尚未开始 |
| 409 | activity_ended | 活动已结束 |
| 409 | already_purchased | 当前用户已经购买该券 |
| 409 | sold_out | 库存不足 |
| 409 | conflict | 活动正在启用或已经启用异步模式，请按活动配置选择入口 |
| 503 | dependency_failure | MySQL、认证依赖或事务提交故障 |
| 504 | timeout | 请求超时，包括等待数据库锁超时被请求 deadline 取消 |

数据库自身的锁等待超时、死锁等未细分错误返回 503。具体时间窗与库存读取见 [优惠券接口](voucher.md)，事务设计见 [ADR 006](../adr/006-seckill-v1-transaction.md)。

按活动启用的异步入口、202 受理语义、结果查询及恢复流程见 [异步秒杀](async-order.md)。同步与异步路径不会同时受理同一活动。
