# 黑马点评 Go 参考实现迁移清单

本轮以本地 `xzdp-go-master` 为参考，先交付完整的基础后端。该参考仓库是 Java 黑马点评的 Go / Hertz 重构；这里只描述实际检查到的 Go 代码，不将 Thrift 声明或空 Service 当作已实现功能。

沿用当前模块化单体、`/api/v1` 路由、Bearer 鉴权、字符串 ID 与统一响应；不提供旧前端的路由和字段兼容层，也不自动导入原仓库的用户数据。

## 功能匹配

| 原仓库实现 | 当前承接位置 / 接口 | 结果 |
| --- | --- | --- |
| `user/send_code.go`、`user_login.go`、`user_me.go` | Auth；验证码登录、当前用户，另有退出 | 已有，保留 |
| `user/user_info.go` | `GET /api/v1/users/:id` | 已迁移公开资料；不存在返回 404，查询不创建用户 |
| `user/user_sign.go`、`user_sign_count.go` | `POST / GET /api/v1/users/me/sign` | 已迁移每月 Bitmap 与连续签到计数 |
| `shop/shop_list.go`、`shop_of_type.go`、`shop_info.go` | 分类、分类分页、详情 | 已有，保留 |
| `shop/shop_of_type_geo.go` | `/api/v1/shops/nearby`、`cmd/geo-rebuild` | 已有，保留 |
| `blog/post_blog.go`、`get_blog.go` | 发布与详情 | 已有，补齐图片列表与作者昵称/头像 |
| `blog/get_user_blog.go`、`blog_of_me.go` | `/api/v1/blogs?user_id=ID`；本人 ID 取 `/users/me` | 已有作者筛选，列表按发布时间排序 |
| `blog/get_hot_blog.go` | `GET /api/v1/blogs/hot` | 已迁移按实际点赞数排序 |
| `blog/like_blog.go`、`get_likes.go` | 点赞/取消/状态；`GET /blogs/:id/likes` | 保留幂等点赞，新增最早五位点赞用户 |
| `blog/delete_blog.go` | `DELETE /api/v1/blogs/:id` | 已迁移；仅作者可删，点赞关系一起事务删除 |
| `follow/follow.go`、`is_followed.go` | 关注/取消/状态/关注列表 | 已有，保留 |
| `follow/common_follow.go` | `GET /users/:id/common-following` | 已迁移；直接查询 MySQL 关系交集并分页 |
| `blog/get_follow_blog.go` | `GET /api/v1/feed` | 已有，保留快照分页；删除的笔记由 MySQL 回填过滤 |
| `voucher/voucher_list.go`、`seckill_voucher.go` | 优惠券查询、事务下单、本人订单 | 已有；保留库存和订单同事务及唯一约束 |
| `handler/image/image_service.go` | `POST /api/v1/uploads`、`GET /uploads/:name` | 已迁移；鉴权、限大小、验证实际 JPEG/PNG、随机文件名 |
| `blog/like_blog.go`、`message/sse.go` | Redis Stream 点赞通知；`GET /api/v1/messages/sse` | 已迁移，采用有游标的有限 SSE 批次重连 |
| `blog_comment/*` | 无 | 五个 Service 均为空返回，未作为可迁移功能 |
| `xzdp/hello_method.go` | `/healthz`、`/readyz` | 演示入口由健康检查承接 |

以上源路径除特别标注外均相对于 `biz/service`。图片上传实际代码位于 Handler，虽然其 Service 是空实现，仍纳入迁移。

## 基础版本边界

- MySQL 事务秒杀已经可用。原仓库没有 Lua + Stream 异步订单消费者可直接迁移；当前未接入的受理组件保留，异步下单、恢复补偿和新压测延期。
- 评论、支付、真实短信、后台管理不在本轮基础版内；不能把 SQL 表或接口声明当成完整实现。
- 本轮提供后端 API，不包含网页前端。原商户的经营展示字段没有逐字段复刻，当前商户契约仍以分类、名称、地址、经纬度和距离为准。
- 上传文件保存本地 `UPLOAD_DIR`，运行目录需要写权限并自行持久化；未提供对象存储、图片清理任务和多实例共享。
- 点赞事实由 MySQL 保存，通知尽力投递。Stream 每人最多保留 1000 条；Redis 故障不回滚点赞，不保证通知不丢失。消息不可当作订单事实。
- 签到按北京时间、当月统计，今日未签到为 0，跨月重新计数；这与原月度 Bitmap 思路一致。Redis 中签到与通知应随开发实例的 AOF 卷持久化。

## 来源许可

参考实现版权为 `Copyright (c) 2024 haopengliu`，采用 MIT License。迁移和适配保留其完整许可证：[xzdp-go-master MIT](third-party/xzdp-go-master-LICENSE.txt)。
