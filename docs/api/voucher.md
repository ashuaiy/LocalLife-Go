# 优惠券与活动查询

基础路径 `/api/v1`，公开读取，无需登录。响应采用统一 `code/message/data/request_id`，设置 `Cache-Control: no-store`；优惠券与活动通过一次 MySQL LEFT JOIN 读取，不经过 Redis 缓存。

## 详情

`GET /vouchers/:id`。ID 必须为 uint64 正整数，不存在返回 404。

普通券 `data` 示例：

```json
{
  "id":"20",
  "shop_id":"3",
  "title":"示例·面馆代金券",
  "pay_value":"1900",
  "actual_value":"3000",
  "seckill":null
}
```

`pay_value` 表示购买金额，`actual_value` 表示券面值，均以**整数分**保存，并以 JSON 字符串传输，避免 uint64 数值在客户端解析时丢失精度。上例为 19 元购买、30 元券面值；接口不使用浮点数计算金额。所有 ID 也使用字符串。

有秒杀活动的券包含 `seckill`：

```json
{
  "id":"21",
  "shop_id":"3",
  "title":"示例·双人套餐秒杀",
  "pay_value":"9900",
  "actual_value":"15000",
  "seckill":{
    "stock":20,
    "begin_time":"2026-09-16T14:00:00Z",
    "end_time":"2026-09-24T14:00:00Z",
    "status":"active"
  }
}
```

`stock` 是数据库当前剩余库存，使用 JSON 整数；时间为 UTC，保留数据库毫秒精度。状态按以下顺序判断，列表同一页只取一次服务端当前时间：

| status | 条件 |
| --- | --- |
| upcoming | 当前时间早于 `begin_time` |
| ended | 当前时间大于等于 `end_time` |
| sold_out | 在活动时间内，库存为 0 |
| active | 在活动时间内，库存大于 0 |

活动区间为 `[begin_time, end_time)`。未开始或已结束时，即使库存为 0，也优先显示对应的时间状态。状态与库存仅反映读取时的情况，不预留库存或保证可下单；后续下单流程仍须重新校验。

## 列表

`GET /vouchers?shop_id=3&page=1&page_size=10`。

| 参数 | 默认值 | 约束 |
| --- | --- | --- |
| shop_id | 查询所有商户 | 若提供，必须为 uint64 正整数 |
| page | 1 | 1–1000 |
| page_size | 10 | 1–50 |

返回按优惠券 ID 递增排列的列表，包含普通券及所有时间状态的秒杀券，多读一条判断 `has_more`。筛选商户不存在、商户无券或页码超出结果范围时，返回空数组。

```json
{"items":[],"page":1,"page_size":10,"has_more":false}
```

已知参数无效、显式为空、重复或溢出时返回 400；未知参数忽略。使用 offset 分页，不承诺并发增删数据时的跨页快照一致性。

## 数据与当前范围

沿用已有 `voucher` / `seckill_voucher` 表，无新增迁移。外键关联活动与券，活动库存由非负约束保护，结束时间必须晚于开始时间。

可选示例脚本 [seed-vouchers.sql](../../scripts/seed-vouchers.sql) 包含一张普通券和一张库存 20 的秒杀券。需先执行迁移和商户示例导入，详见 [本地运行](../quickstart.md#可选导入优惠券示例)。活动首次导入时设为前一天开始、七天后结束；顺序重复执行不会新增同名示例券、重置库存或延长活动时间。过期后再次执行也不会重新开放活动。

这两个接口只提供查询与活动状态展示。MySQL 事务秒杀与个人订单查询见 [秒杀订单接口](order.md)，支付不在当前项目范围内。

## 错误响应

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 非法 ID 或分页参数 |
| 404 | not_found | 优惠券详情不存在 |
| 408 | request_canceled | 请求取消 |
| 503 | dependency_failure | 数据库不可用或活动数据不完整 |
| 504 | timeout | 请求超时 |
