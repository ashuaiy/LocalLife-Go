# LocalLife-Go

以**黑马点评的本地生活业务模型**为基础，使用 **Go、CloudWeGo Hertz、GORM、MySQL 和 Redis** 重构的服务端项目。采用模块化单体架构，围绕用户、商户、探店内容、关注动态和优惠券构建业务模块。

> 当前为可运行的后端 API 基础版本，覆盖认证、商户、附近商户、图文笔记、点赞关注、Feed、签到、优惠券与订单。秒杀采用 MySQL 同步事务；Redis Stream 用于点赞通知，异步订单链路尚未接入。

## 技术框架

| 技术 | 用途 |
| --- | --- |
| Go | 服务端开发语言 |
| CloudWeGo Hertz | HTTP 服务、路由和中间件，显式使用标准网络 transport |
| GORM | 数据库访问 |
| MySQL | 业务数据持久化、事务与数据约束 |
| Redis | 验证码、会话、商户缓存、GEO、Feed ZSET、签到 Bitmap 与通知 Stream |
| golang-migrate | 版本化 SQL 数据库迁移 |
| slog | 结构化访问日志 |
| Docker Compose | 本地开发与集成测试依赖编排 |

## 架构设计

采用模块化单体设计，业务模块按以下职责分层：

- **Handler**：请求解析、参数校验和响应映射。
- **Service**：业务规则、事务边界与缓存协调。
- **Repository**：数据持久化与查询。
- **Middleware**：请求标识、超时控制、异常恢复与访问日志。

MySQL 保存用户、商户、内容、关注和订单等实体与关系。Redis 承载会话、缓存和业务索引，并独立保存签到与通知数据。请求通过 `context.Context` 传递上下文和超时，连接池由应用统一管理；图片保存至可配置的本地目录。

## 已实现功能

- **用户认证**：手机号验证码登录、首次登录创建用户、随机 Bearer Token 与 Redis Session、当前用户查询和退出登录。开发验证码需显式开启，短信通道尚未接入。
- **资料与签到**：公开资料、粉丝与关注数量、北京时间月度 Bitmap 签到及连续天数。
- **会话与验证码策略**：固定会话 TTL、验证码有效期与重发冷却、错误尝试上限、验证码原子消费、并发注册去重。
- **商户查询**：分类列表、按分类筛选的商户分页、商户详情、参数边界校验和字符串 ID 响应。
- **商户缓存**：短期空值缓存、随机 TTL、逻辑过期和按 ID 合并回源；旧值在硬过期前可读，后台刷新有并发上限、超时和失败退避。
- **商户维护**：通过命令更新名称与地址，数据库提交后删除详情缓存；Lua CAS 防止已观察到的缓存被删除或替换后，旧刷新任务直接覆盖它。
- **附近商户**：按分类进行 Redis GEO 半径查询与距离排序，MySQL 批量读取商户信息；支持位置索引重建与完整索引原子替换。
- **探店内容**：图文发布、详情与分页、商户及作者筛选、热门排序和删除本人笔记。
- **图片上传**：鉴权上传 JPEG/PNG，本地存储，格式、大小与像素数量校验。
- **点赞通知**：Redis Stream 保存通知，SSE 批次读取与游标重连；重复点赞不重复投递。通知使用独立短超时，尽力投递，失败不回滚已提交的点赞。
- **点赞**：当前用户点赞、取消、状态查询与最早五名点赞用户列表；MySQL 唯一约束保证并发重复请求不重复计数。
- **关注**：关注、取消、状态查询、个人关注列表与共同关注；唯一约束保证并发重复关注幂等。
- **动态 Feed**：发布后向关注者推送 Redis ZSET，使用 5 分钟快照与同分游标分页，支持从 MySQL 受控重建。
- **优惠券查询**：券详情、按商户筛选与分页，区分普通券和秒杀券；展示活动时间、库存及未开始、进行中、售罄、已结束状态。
- **事务秒杀**：活动锁与数据库时间校验、条件扣库存、订单唯一约束；扣库存和创建订单在同一 MySQL 事务内完成。
- **个人订单**：登录用户查询本人订单详情和分页列表，支持按优惠券筛选；拒绝读取其他用户的订单。
- **配置管理**：环境变量加载、类型解析与参数校验。
- **HTTP 服务**：Hertz 路由、统一 JSON 响应与错误分类。
- **请求处理**：Request ID、上下文超时、异常恢复和结构化访问日志。
- **依赖连接**：MySQL / Redis 连接池、启动连通性检查与资源释放。
- **健康检查**：`GET /healthz` 检查服务存活，`GET /readyz` 检查依赖可用性。
- **生命周期管理**：启动错误处理、信号响应与优雅退出。
- **数据库迁移**：用户、商户类型、商户、探店内容、点赞、关注关系、优惠券、秒杀库存和订单表结构，包含关系唯一约束、订单唯一约束与库存非负约束。
- **测试支持**：配置、错误映射、HTTP、生命周期与各业务模块测试；真实 MySQL / Redis 集成测试，以及容器化 Race 检查。

## 当前接口

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| POST | `/api/v1/auth/code` | 获取开发验证码，受开关与重发冷却限制 |
| POST | `/api/v1/auth/login` | 验证码登录，创建用户或复用已有账号 |
| GET | `/api/v1/users/me` | 查询当前登录用户 |
| GET | `/api/v1/users/:id` | 公开资料与关注统计 |
| POST / GET | `/api/v1/users/me/sign` | 今日签到与月内连续天数 |
| GET | `/api/v1/users/:id/common-following` | 分页查询共同关注 |
| POST | `/api/v1/auth/logout` | 注销当前会话 |
| GET | `/api/v1/shop-types` | 按展示顺序查询商户分类 |
| GET | `/api/v1/shops` | 按分类筛选并分页查询商户 |
| GET | `/api/v1/shops/:id` | 查询商户详情，使用 Cache Aside |
| GET | `/api/v1/shops/nearby` | 根据坐标、分类和半径查询附近商户 |
| POST | `/api/v1/blogs` | 登录用户发布探店内容 |
| GET | `/api/v1/blogs/hot` | 热门笔记分页 |
| GET | `/api/v1/blogs/:id/likes` | 最早五名点赞用户 |
| DELETE | `/api/v1/blogs/:id` | 删除本人笔记 |
| POST | `/api/v1/uploads` | 上传笔记图片 |
| GET | `/uploads/:name` | 读取图片 |
| GET | `/api/v1/messages/sse` | 本人点赞通知 |
| GET | `/api/v1/blogs` | 按商户、作者筛选并分页查询内容 |
| GET | `/api/v1/blogs/:id` | 查询探店详情和点赞数 |
| PUT / DELETE | `/api/v1/blogs/:id/like` | 当前用户点赞或取消点赞 |
| GET | `/api/v1/blogs/:id/like` | 查询当前用户的点赞状态 |
| PUT / DELETE | `/api/v1/users/:id/follow` | 关注或取消关注用户 |
| GET | `/api/v1/users/:id/follow` | 查询当前用户的关注状态 |
| GET | `/api/v1/users/me/following` | 分页查询个人关注列表 |
| GET | `/api/v1/feed` | 分页读取关注动态 |
| GET | `/api/v1/vouchers` | 按商户筛选并分页查询优惠券 |
| GET | `/api/v1/vouchers/:id` | 查询优惠券详情、库存与活动状态 |
| POST | `/api/v1/vouchers/:id/seckill` | 当前登录用户参加秒杀并事务下单 |
| GET | `/api/v1/orders` | 分页查询本人订单，可按券筛选 |
| GET | `/api/v1/orders/:id` | 查询本人订单详情 |
| GET | `/healthz` | 服务存活检查 |
| GET | `/readyz` | MySQL / Redis 可用性检查 |

本人资料、退出、签到、内容发布与删除、图片上传、点赞操作、关注操作、共同关注、Feed、通知、下单与订单查询通过 `Authorization: Bearer <token>` 鉴权；公开资料、商户、公开内容、点赞用户列表、已上传图片与优惠券查询无需登录。接口契约见 [用户认证](docs/api/auth.md)、[商户查询](docs/api/shop.md)、[附近商户](docs/api/geo.md)、[探店点赞](docs/api/blog.md)、[关注动态](docs/api/feed.md)、[优惠券](docs/api/voucher.md) 与 [秒杀订单](docs/api/order.md)，模块职责见 [架构说明](docs/architecture.md)，示例数据与启动方法见 [本地运行](docs/quickstart.md)。

接口补充见 [资料、签到与社区接口](docs/api/community.md)，来源与匹配范围见 [迁移清单](docs/migration.md)。参考实现的 MIT 许可保留在 [第三方许可证](docs/third-party/xzdp-go-master-LICENSE.txt)。

## 后续功能

以下完整能力尚未接入当前业务：

| 模块 | 功能 |
| --- | --- |
| 用户 | 短信通道接入 |
| 异步订单 | 已有内部 Lua 受理组件；HTTP 异步入口、订单消费者、幂等恢复与失败补偿尚未接入 |
| 可观测性 | Prometheus 指标、OpenTelemetry 链路追踪与 pprof |
