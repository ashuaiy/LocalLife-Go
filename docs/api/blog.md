# 探店内容与点赞

基础路径 `/api/v1`，统一返回 `code/message/data/request_id`。ID 使用 JSON 字符串。标题与正文是公开纯文本，客户端展示时应进行文本转义；笔记另支持可选图片列表。

## 发布探店

`POST /blogs`，需要 `Authorization: Bearer <token>` 与 `Content-Type: application/json`。

```json
{"shop_id":"1","title":"周末探店","content":"环境安静，服务热情。"}
```

- `shop_id` 必须为字符串形式的 uint64 正整数，并对应已有商户。
- 标题去除首尾空白后为 1–255 个 Unicode 字符，正文为 1–10000 个字符；正文内部换行保留。
- 请求体最多 64 KiB，拒绝未知字段、多个 JSON 文档与格式错误。
- 作者只能来自登录身份，请求体中的 `user_id` 等额外字段会被拒绝。
- 可选 `images` 最多包含 9 个上传地址或 HTTPS URL；详情、列表及 Feed 同时返回图片、作者昵称和头像。图片上传、热门、点赞用户列表与作者删除见 [社区接口](community.md)。

成功返回 200，`data` 示例：

```json
{
  "id":"9",
  "user_id":"7",
  "shop_id":"1",
  "title":"周末探店",
  "content":"环境安静，服务热情。",
  "like_count":0,
  "created_at":"2026-09-17T14:00:00Z"
}
```

发布是创建操作，不提供重复请求去重。保存内容后会向当时已关注作者的用户推送 Feed；推送失败返回 503，但 MySQL 内容仍然保留，部分关注者也可能已收到推送。网络断开或超时同样可能发生在写入之后，客户端应先查看自己的内容列表再决定是否重试。当前没有自动重试任务，受控恢复见 [Feed 重建](feed.md#失败与恢复)。

## 详情与列表

`GET /blogs/:id` 公开读取，返回上述结构；不存在返回 404。

`GET /blogs?shop_id=1&user_id=7&page=1&page_size=10` 公开查询。

| 参数 | 默认值 | 约束 |
| --- | --- | --- |
| shop_id | 不筛选 | 若提供，必须为 uint64 正整数 |
| user_id | 不筛选 | 若提供，必须为 uint64 正整数 |
| page | 1 | 1–1000 |
| page_size | 10 | 1–50 |

两个筛选同时提供时取交集。按 `created_at DESC, id DESC` 排序，多读一条判断 `has_more`。空结果返回 `items: []`，不存在的筛选对象同样返回空列表。

```json
{"items":[],"page":1,"page_size":10,"has_more":false}
```

无效、显式空值或重复的已知参数返回 400；未知查询参数被忽略。普通列表使用 offset 分页，并发新增时不承诺跨页快照一致性；[关注 Feed](feed.md) 使用独立的快照滚动游标。

## 点赞与取消

三个接口都需要登录、无请求体，响应设置 `Cache-Control: no-store`。

| 方法 | 路径 | 含义 |
| --- | --- | --- |
| PUT | `/blogs/:id/like` | 当前用户点赞；重复请求不重复计数 |
| DELETE | `/blogs/:id/like` | 当前用户取消点赞；未点赞时同样成功 |
| GET | `/blogs/:id/like` | 查询当前用户是否已点赞 |

成功的 `data` 为 `{"liked":true}` 或 `{"liked":false}`。点赞对象不存在时返回 404，不能指定其他用户执行操作。同一用户同时发送相反操作时，最终状态由数据库实际执行顺序决定，响应不阻止后续请求再次改变状态。

`blog_like` 使用 `(blog_id, user_id)` 复合主键，数据库裁定并发重复写入；取消只删除当前用户的一条关系。详情和列表中的 `like_count` 从关系表计数，无独立缓存计数器。当前没有内容编辑、删除、图片上传或热点榜单接口。

## 错误响应

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 参数或 JSON 无效 |
| 401 | unauthorized | 缺失、过期或无效登录身份 |
| 404 | not_found | 商户或探店内容不存在 |
| 408 | request_canceled | 请求取消 |
| 503 | dependency_failure | 数据库、认证或 Feed 推送依赖故障 |
| 504 | timeout | 请求超时 |

部署此模块前运行 `go run ./cmd/migrate up`，应用增量迁移 000002；它新增点赞表与列表索引，不覆盖已有内容。
