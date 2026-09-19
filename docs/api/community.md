# 用户资料、签到与社区补齐接口

一般响应沿用 `code/message/data/request_id`，所有 ID 是十进制字符串。受保护接口使用 `Authorization: Bearer <token>`。

| 方法 | 路径 | 说明 | 登录 |
| --- | --- | --- | --- |
| GET | `/api/v1/users/:id` | 公开资料、粉丝数、关注数，不返回手机号 | 否 |
| POST | `/api/v1/users/me/sign` | 今日签到，重复请求幂等 | 是 |
| GET | `/api/v1/users/me/sign` | 日期、今日是否签到、月内连续签到天数 | 是 |
| GET | `/api/v1/users/:id/common-following` | 本人与目标的共同关注 | 是 |
| GET | `/api/v1/blogs/hot` | 按点赞数降序、ID 降序分页 | 否 |
| GET | `/api/v1/blogs/:id/likes` | 最早五名点赞用户，时间相同按用户 ID 排序 | 否 |
| DELETE | `/api/v1/blogs/:id` | 删除本人笔记与点赞关系 | 是 |
| POST | `/api/v1/uploads` | multipart/form-data，字段 `file` | 是 |
| GET | `/uploads/:name` | 读取已保存图片 | 否 |
| GET | `/api/v1/messages/sse` | 当前用户收到的点赞通知 | 是 |

共同关注、热门笔记接受 `page=1`、`page_size=10`；最大页码 1000、每页最多 50。共同关注返回 `items/page/page_size/has_more`，每项仅包含 ID、昵称、头像。资料中的城市、简介、性别、生日、积分、等级为可选展示数据，默认空值或零；关注统计直接来自关系表。本轮没有编辑资料、积分发放或等级规则。

签到示例：`{"date":"2026-09-18","signed":true,"streak":3}`。使用 Asia/Shanghai（UTC+8）日历；统计止于今天，今天未签则为 0；跨月不累计上月。

删除不存在或非本人笔记均返回 404；不会删除图片文件。删除后 Feed 可能保留旧 ID，但数据库回填会过滤已删除内容。

## 图片与笔记

上传上限 2 MiB，仅接受实际可解码的 JPEG/PNG，像素总量最多 1600 万；重新编码后存储，文件名由服务端随机生成。返回 `{"url":"/uploads/<随机名称>.png"}`。文件在 `UPLOAD_DIR`（默认仓库工作目录下的 `uploads`）中，公开可读。

发布接口仍为 `POST /api/v1/blogs`，增加可选 `images`：最多 9 个，每个 URL 最多 512 字节，接受上传地址或 HTTPS URL；服务器不会抓取外部图片。

```json
{"shop_id":"1","title":"探店记录","content":"正文","images":["https://example.com/photo.jpg"]}
```

详情、列表、热门及 Feed 的笔记响应新增 `images`、`nickname`、`avatar`，原有字段不变。没有图片时返回空数组。

## 点赞通知

从未点赞变为点赞时向笔记作者投递 Stream 通知；重复 PUT、取消点赞、自赞不投递。取消后再次点赞可产生新通知。点赞提交和 Redis 投递不跨库事务；通知使用最多 100ms 的独立预算并预留响应时间，时间不足时跳过，投递失败仅记日志，点赞仍成功。

SSE 每次最多返回 50 条后关闭，客户端按 `retry: 1000` 的间隔重连，并用 `Last-Event-ID` 传入最后处理的事件 ID。不传则从当前保留的最早消息开始。无新消息时只返回 retry 行。每人最多保留 1000 条，游标早于保留窗口可能遗漏旧消息；客户端可按 ID 去重。

```text
retry: 1000

id: 1720000000000-0
event: like
data: {"id":"1720000000000-0","blog_id":"1","user_id":"2","nickname":"用户","avatar":""}

```

接口需要 Bearer Header。浏览器原生 EventSource 不能自定义该 Header，应使用支持 Header 的 SSE 客户端或 fetch 重连；不要把 Token 放入 URL。该有限批次模式遵守现有 HTTP 超时，无独立长连接消费者或后台线程。
