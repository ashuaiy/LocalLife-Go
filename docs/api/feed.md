# 关注与动态 Feed

基础路径 `/api/v1`，所有接口需要 `Authorization: Bearer <token>`，响应设置 `Cache-Control: no-store`。统一返回 `code/message/data/request_id`，ID 为 JSON 字符串。

## 关注关系

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| PUT | `/users/:id/follow` | 当前用户关注目标用户 |
| DELETE | `/users/:id/follow` | 当前用户取消关注目标用户 |
| GET | `/users/:id/follow` | 查询当前用户是否关注目标用户 |
| GET | `/users/me/following` | 查询当前用户的关注列表 |

前三个接口无请求体，成功的 `data` 为 `{"following":true}` 或 `{"following":false}`。目标必须是已有用户，不能是自己。重复关注或重复取消均成功；数据库 `UNIQUE(user_id, follow_user_id)` 保证并发重复关注只保留一条关系。相反操作并发执行时，最终状态由数据库实际执行顺序决定。

关注列表接受 `page`（默认 1，范围 1–1000）与 `page_size`（默认 10，范围 1–50），按关注关系 ID 倒序排列。只返回用户 ID、昵称和头像，不返回手机号；无效、重复或显式为空的分页参数返回 400。

```json
{
  "items":[{"id":"7","nickname":"作者","avatar":""}],
  "page":1,
  "page_size":10,
  "has_more":false
}
```

## Feed 查询

`GET /feed?page_size=10&cursor=<next_cursor>`。

| 参数 | 默认值 | 约束 |
| --- | --- | --- |
| page_size | 10 | 1–50；显式为空或重复时返回 400 |
| cursor | 创建新快照 | 不传或空字符串为首页；续页原样使用上次的 `next_cursor`；重复参数返回 400 |

示例 `data`：

```json
{
  "items":[{
    "id":"9","user_id":"7","shop_id":"1",
    "title":"周末探店","content":"环境安静。",
    "like_count":0,"created_at":"2026-09-17T14:00:00Z"
  }],
  "next_cursor":"<服务端返回的不透明游标>",
  "has_more":true
}
```

发布内容先保存 MySQL，再向发布时已关注作者的用户推送 Redis ZSET 成员，member 为内容 ID，score 为发布时间的 Unix 毫秒数。按 score 倒序读取；同分成员按 Redis 的字符串逆字典序稳定排列，不承诺内容 ID 数值倒序。重复推送使用 `ZADD NX`，不会新增重复成员或改变原位置。

关注不会补发关注前的内容；取消关注后，后续读取会过滤该作者的内容；重新关注以新的关注时间为界。关注时间与发布时间使用毫秒精度，判断为 `follow.created_at <= blog.created_at`。Feed 不包含自己的发布内容。

### 分页约定

- 首次请求原子复制当前 Feed 索引，生成用户专属、固定 5 分钟有效的快照；翻页不续期。
- 后续使用同一快照的 `max score + equal-score offset`。同分跨页时累积 offset，切换分值时重新计算该分值已消费条数。
- 翻页期间新推送或重建不改变已有快照。重新请求首页才会看到新内容。
- 快照固定的是候选成员及顺序。内容字段、点赞数和当前关注关系仍从 MySQL 实时读取；不存在或已不满足关注条件的内容会过滤。
- 游标按原始候选推进，因此一页可能不足 `page_size`，甚至 `items: []` 但 `has_more: true`。客户端按 `has_more` 继续，不能仅以数组长度判断结束。
- 结束时 `has_more: false`，省略 `next_cursor`。空 Feed 返回 `items: []`。
- 游标格式错误、字段缺失或为 `null` 返回 400；快照过期、丢失或被其他用户使用返回 409。客户端收到 409 后丢弃游标，重新请求首页。

游标不作为身份凭证，读取目标只能来自登录上下文，不能通过请求指定其他用户的 Feed。

## 失败与恢复

发布时的 Feed 推送是同步步骤。MySQL 内容保存成功后，查询关注者或 Redis 推送仍可能失败，响应为 503（超时为 504）；此时内容仍然存在，部分关注者也可能已经收到推送。当前没有持久化任务、自动重试或跨 MySQL/Redis 事务。先通过作者列表核对已发布内容，避免盲目重发导致重复内容。

Redis Feed 索引丢失或推送不完整时，可从 MySQL 重建指定读者的动态。**先暂停内容发布和关注变更，避免同一读者并发重建**，然后在仓库根目录加载应用配置并运行：

```powershell
. .\scripts\env.ps1
go run ./cmd/feed-rebuild -user-id 7
```

`-user-id` 是读者 ID，必须对应已有用户。命令从当前关注关系及关注后的内容生成完整临时索引，校验条目数后原子替换 live key；失败保留原 live 索引，空集合会清除旧索引。临时 key 有 10 分钟 TTL，命令总超时 5 分钟。重建成功后恢复写入，客户端刷新首页；已有快照继续使用原候选直到过期。

这是一项受控恢复操作：MySQL 查询与 Redis 发布不是跨系统事务，暂停写入是避免覆盖并发推送的前提。当前没有 Feed 初始化标记，丢失的 live key 与无内容的 Feed 都会表现为空；缺失检测和容量治理留待有基线的优化。

## 错误响应

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 非法目标、自关注、分页或游标格式错误 |
| 401 | unauthorized | 缺失、过期或无效登录身份 |
| 404 | not_found | 关注目标不存在 |
| 408 | request_canceled | 请求取消 |
| 409 | conflict | 分页快照过期或不再可用，需要刷新 |
| 503 | dependency_failure | MySQL / Redis 故障 |
| 504 | timeout | 请求超时 |

本模块沿用已有关注表，无新增迁移。模型与分页取舍见 [ADR 005](../adr/005-push-feed-and-snapshot-cursors.md)。
