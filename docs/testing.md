# 基础框架与业务模块验证

## 参考业务迁移验收（2026-09-18）

- `TestCommunityMigration` 先在缺少接口时失败，再验证公开资料无手机号、共同关注、重复签到、图片上传读取与图文发布、热门/点赞列表、作者删除、点赞通知去重与游标续读。
- 审查复现通知耗尽请求期限后已提交点赞仍返回 504 的问题；改为独立最多 100ms 通知预算并保留响应时间。真实 HTTP 回归验证通知超时仍返回 200；溢出 Stream 游标返回 400。
- 媒体单测覆盖 JPEG/PNG、重新编码去除尾部内容、非法格式、超限文件、路径穿越与取消；真实 Redis 签到测试覆盖连续签到、缺口及跨月。
- Windows `scripts/check.ps1`（格式、全量单测、vet、build）通过。
- Docker `go test -race ./... -count=1` 通过，包含真实 MySQL/Redis；最终集成包耗时 5.601s。未重跑性能压测，不将该耗时作为性能指标。
- 实际 `cmd/server` 已在回环地址启动，开发实例应用迁移版本 3、虚构演示数据及 GEO 索引。通过真实 TCP 完成双用户登录、商户/GEO、关注、发布笔记、Feed、点赞、签到、资料、SSE 通知、优惠券下单及本人订单查询。
- 独立只读审查复核通过。原参考仓库评论 Service 为空，异步订单消费者不存在；这些不作为本轮已交付功能。范围见 [迁移清单](migration.md)。

复跑新功能：使用下方隔离依赖环境，执行 `go test ./tests/integration -run TestCommunityMigration -count=1`；跨月签到测试通过同一套环境运行 `go test ./internal/cache -run TestSignMonthBoundaryAndGap -count=1`。

## 不依赖 MySQL / Redis 的检查

```powershell
.\scripts\check.ps1
```

等价于格式检查、`go test ./... -count=1`、`go vet ./...` 和 `go build ./...`。HTTP 测试既有 Hertz 内存请求，也有真实 TCP 监听/客户端/取消退出；不是只测 mock 的连通性。

默认 `tests/integration` 会明确 skip。`go test` 显示该 package 为 ok **不代表真实数据库集成通过**，使用 `-v` 查看原因。

## 真实依赖集成测试

测试实例使用独立 Compose 项目、13306/16379 端口，MySQL 数据存放 tmpfs；不复用开发实例或开发卷。

```powershell
docker compose -f docker-compose.test.yml up -d --wait
$env:RUN_INTEGRATION='1'
$env:MYSQL_ADDR='127.0.0.1:13306'
$env:MYSQL_DATABASE='locallife_test'
$env:MYSQL_USER='locallife'
$env:MYSQL_PASSWORD='locallife_dev'
$env:REDIS_ADDR='127.0.0.1:16379'
$env:REDIS_PASSWORD=''
$env:REDIS_DB='0'
go test ./tests/integration -v -count=1
docker compose -f docker-compose.test.yml down
Remove-Item Env:RUN_INTEGRATION
# 后续开发请重新打开终端，或重新加载 .env 恢复开发地址。
```

Linux/macOS 在启动测试 Compose 后执行 `make integration`，完成后运行相同的 `docker compose -f docker-compose.test.yml down`。

如果显式设置了 `RUN_INTEGRATION=1`，依赖不可用必须失败，不能静默 skip。数据库名必须以 `_test` 结尾。测试只在独立测试库应用迁移，业务样本插入事务最终 rollback；Redis 只清理本测试产生的随机 key，未使用 FLUSHDB。

检查项：当前九张业务表、migration 版本和 dirty 状态、重复 migration no-op、数据库订单唯一约束、条件扣库存、非负库存约束、GORM context/pool、Redis 读写和 TTL、真实依赖的 readiness 及连接关闭后 503。

以上条目是基础设施与 schema 验证，**不能替代秒杀业务并发验收**。当前业务并发正确性结果见后面的 Seckill V1 记录；短时 HTTP 负载基线见 [秒杀负载报告](performance/seckill.md)，V2 对照与持续负载仍待后续阶段完成。

## Race

推荐在带 GCC 的官方 Go 容器中同时运行全部测试和真实依赖测试：

```powershell
docker compose -f docker-compose.test.yml run --rm test
```

该命令自动启动独立 MySQL/Redis 测试实例，使用 Go 1.26.4 和 `CGO_ENABLED=1` 执行 `go test -race ./... -count=1`。项目目录只读挂载，Go module 和编译缓存使用命名卷。

依赖镜像拉取后可重复运行；若默认 Go 代理不可达，可在本次终端设置 `$env:GOPROXY='https://goproxy.cn,direct'`。完成测试后运行 `docker compose -f docker-compose.test.yml down` 停止测试依赖；不添加 `-v`，保留编译缓存。

也可以在具备 C 编译器的主机直接执行：

```powershell
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

Windows 需要 PATH 上可用的兼容 C 编译器（如 MinGW-w64 GCC）。只设置 CGO_ENABLED 不会安装编译器。Linux 上同样需要 GCC/Clang，测试可在具备这些工具的环境运行。

## 秒杀 HTTP 负载基线验收（2026-09-17）

- 新增显式 `RUN_LOAD=1` 的真实 TCP 负载工具，保留同步事务 V1 的装配；默认测试不运行负载。复跑命令、环境、统计口径和原始结果见 [负载报告](performance/seckill.md)。
- 1/16/64 并发 × 库存充足/超额抢购 × 三轮，共 6300 次请求、3600 个已提交订单；系统错误 0、未派发 0，最终库存均为 0，无超卖或重复用户订单，成功响应逐一对应 MySQL 订单。
- 所有报告包含 QPS、nearest-rank P95/P99、成功数、业务拒绝、系统错误率、全部非成功率，以及数据库连接池等待。测量仅为短时闭环对照，不外推持续生产容量。
- 审查修复了共用准备阶段期限导致超时统计失真的边界：负载独立计时、取消后停止发放、单独统计未派发、先保存 HTTP 结果再用新期限核验；取消回归先失败再通过。
- 响应分类、统计函数与取消调度的无依赖测试 `TestLoad* -count=10` 通过；修正了 Windows 快速回环可出现零耗时的测试假设，不对真实延迟做抬高。
- 修复后重新跑三轮非 Race 负载并保存公开基线。Windows `scripts/check.ps1` 的格式、全量测试、vet、build 通过；Docker `RUN_LOAD=1` 的完整 `go test -race ./... -count=1` 通过，包含真实依赖及负载工具，Race 耗时未混入基线。
- 独立复审通过；公开文档本地文件链接和测量源文件摘要已核对。异步下单与故障恢复仍待实现。

## Seckill V2 内部受理组件验收（2026-09-17）

- `Prepare / Reserve / Receipt` 的真实 Redis 测试通过；初始化只创建新状态与 `orders` Consumer Group，不重置已有库存。
- 40 用户各请求两次、库存 15：受理 15、重复拒绝 15、售罄 50，Redis 库存 0、Stream 事件 15，受理回执逐一对应事件与用户。
- 活动未开始、已结束、零库存、状态丢失、Stream 丢失或类型错误、损坏库存及 XADD 达到最大 ID 后失败，均不写购买标记、不扣库存、不新增受理事件。
- 券 ID 与用户 ID 同时为 uint64 最大值时，Prepare → Reserve → Receipt 和真实 Stream 字段保持完整十进制字符串，无 Lua 浮点舍入。
- 回执不等同于最终订单；重复请求不再次扣减，事件缺失时报告依赖失败，不把不确定结果当成不存在。状态不设自动过期。
- HSET、XADD 或清理用 XDEL 被 ACL 拒绝时，在预检查阶段退出，无库存、购买标记或事件的部分写入。测试使用随机临时账号，并通过 `ACL WHOAMI` 核实身份，结束后删除账号。
- ACL 测试覆盖写前拒绝；目前没有通过故障注入直接触发“XADD 成功后 HSET 失败”的清理分支，不以这些测试宣称覆盖所有 Redis 运行时故障或持久化丢失。
- 三轮 Lua 受理 Benchmark 为 216131–240711 ns/op、768 B/op、27 allocs/op，结束后验证事件数量和零库存；范围与原始结果见 [Lua 基线](performance/seckill.md#lua-受理路径基线)。
- Windows `scripts/check.ps1` 的格式、全量测试、vet、build 通过；Docker 全量 `go test -race ./... -count=1` 通过，包含真实 MySQL/Redis 用例。独立只读审查无阻断发现，另行通过 Cache/Model 单元测试。

本轮不包含 HTTP 异步入口、MySQL generation 激活隔离、消费者、最终订单状态和恢复补偿；当前 HTTP 仍为 V1。必要接入条件见 [ADR 006](adr/006-seckill-v1-transaction.md#后续接入的必要条件)。

## 用户模块验收（2026-09-17）

- Windows amd64 / Go 1.26.4：格式检查、`go test ./... -count=1`、`go vet ./...` 和 `go build ./...` 通过。
- 真实 MySQL 8.4 / Redis 7.4：Phase 0 全部集成用例通过，先前待验收项已补齐。
- 验证码：并发校验仅一次成功、重发冷却、错误次数耗尽和过期测试通过。
- 用户：16 个并发首次注册只保留一条记录；重新登录使用同一用户 ID。
- 会话：Token 生成、固定 TTL、当前用户查询、退出、过期、验证码重放拒绝和 Redis 故障测试通过。
- HTTP：严格 JSON 校验、身份上下文、错误映射、禁止缓存和日志脱敏测试通过。
- 实际命令入口：在临时回环端口启动 `cmd/server`，完成验证码→登录→当前用户→退出流程，退出后返回 401；样本用户已清理，服务已停止。
- Linux Docker / Go 1.26.4 / GCC：完整 `go test -race ./... -count=1` 通过，包含真实依赖集成测试。
- 短信供应商尚未接入；开发验证码仅限显式开启的本地模式。

集成测试会在专用测试库写入样本，并清理本测试创建的用户记录；验证码和冷却 key 自动过期。Redis Session 通过 logout 或 TTL 清理。

## 商户 V1 历史验收（2026-09-17）

- Windows amd64 / Go 1.26.4：`scripts/check.ps1` 通过，包含格式、全部单元测试、vet 与 build。
- Service：Cache Aside 命中跳过数据库，未命中回填 30 分钟 TTL；缓存读写故障、请求取消、404、503、分页边界与空集合均有测试。
- HTTP：公开查询、默认分页、分类筛选、uint64 ID 字符串编码、非法/重复参数、上下文与错误映射通过。
- 真实 MySQL 8.4 / Redis 7.4：分类顺序、跨页筛选、坐标精度、详情首次回填、命中旧值、删除后回源、缓存过期、损坏记录修复、不存在记录不缓存与 Redis 不可用时的查询通过。
- Linux Docker / Go 1.26.4 / GCC：完整 `go test -race ./... -count=1` 通过，包含基础框架、认证与商户真实依赖集成测试。
- 可选 `scripts/seed-demo.sql` 在专用测试库连续执行两次，仍为两个示例分类、三个虚构商户，无重复插入。
- 实际 `cmd/server` 入口：在临时回环端口启动，readiness、分类、分类分页、连续两次详情请求均为 200；ID 为字符串、坐标精度正确、非法分页为 400。验证进程已停止。
- 独立代码审查完成，未发现阻断本轮交付的问题。

商户集成测试只删除本次创建的分类、商户与对应 Redis key；不清空数据库。V1 不包含空值缓存、热点治理或商户写接口，本轮没有运行性能 Benchmark。

## 商户缓存治理验收（2026-09-17）

- 保留 V1 冻结参考，真实 MySQL/Redis 对照：100 次重复不存在查询从 100 次 DB 读取降到 1 次；64 个重叠冷查询从 64 次降到 1 次；100 次暖缓存查询均为 0 次。并发对照人为添加 50ms 数据库延迟，完整方法见 [缓存对照与 Benchmark](performance/cache.md)。
- 单元测试：空值与故障区分、同 ID 合并、首请求取消不取消共享工作、64 个不同 ID 的默认容量策略（测试缩小容量验证边界）、普通错误与 2 秒超时后的 5 秒退避、硬过期不延长、关闭取消及等待任务。
- 真实依赖：逻辑过期的 100 次旧值读取仅启动一次回源，释放受控查询后可读新值；Lua CAS 拒绝已删除或替换快照的旧发布；硬过期记录不再返回；数据库删除后转换为空值；正负 TTL 范围符合契约。
- 维护命令：数据库事务更新名称与地址后 DEL，保留坐标和分类，同值编辑可重试；缺失商户返回 404；删除失败保留已提交结果并报错，DEL 有 100ms context 预算。
- 独立审查发现后台超时漏掉退避，新增测试先复现失败；改用服务生命周期下独立的 100ms 退避写入预算后通过。维护 DEL 超时预算同样经过先失败、后通过的回归验证，复审无剩余发现。
- 实际 `cmd/server` 与 `cmd/shop-update`：暖缓存 → 修改并确认 key 已删除 → 注入逻辑过期旧值 → 首次 HTTP 返回旧值 → 后台更新后返回新名称和空地址；不存在 ID 连续两次 HTTP 404，观察到空值 TTL 约 34 秒。进程已停止，示例名称与地址已恢复。
- 暖缓存三次 Benchmark 为 176167–179634 ns/op、1752 B/op、26 allocs/op、零 DB 读取；仅作为当前环境 Service + Redis 回归基线，不换算为 HTTP QPS 或线上性能结论。
- 审查修复后重新执行 Windows `scripts/check.ps1`，格式、全量测试、vet 与 build 通过；Docker Linux `go test -race ./... -count=1` 全量通过，包含真实 MySQL/Redis 集成测试。公开文档的 60 个本地文件链接检查通过。

缓存一致性与空 key 竞争的限制见 [商户接口](api/shop.md#缓存一致性边界)。该阶段尚不包含缓存专用指标、多实例压力或完整系统负载验收。

## GEO 模块验收（2026-09-17）

- Windows Go 1.26.4：`scripts/check.ps1` 的格式、全部单元测试、vet、build 通过。
- HTTP 与 Service：必填分类和坐标、有限数值、经纬度/半径/数量边界、重复参数、保持距离顺序、MySQL 实体回填、空结果与依赖错误通过。
- 真实 MySQL/Redis：距离排序、半径限制、数量限制、分类隔离、数据库删除后的过滤、名称即时更新、未初始化与已初始化空索引、正式索引丢失后的失败语义通过。
- 重建：超过单批大小的 501 条位置数据与并发查询同时执行，结果来自完整的一代索引；正式 key 无 TTL，临时 key 有 TTL。
- 独立审查未发现阻断问题；按审查建议补充空/非空索引反复发布时的并发查询，确保结果为完整候选或空数组，不出现跨版本读取的错误 503。
- 故障注入：在第一批写入后删除临时 key，修复前可复现错误发布；补充发布前 ZCARD 校验后，用例通过且旧索引保持可读。
- Docker Linux / Go 1.26.4：全量 `go test -race ./... -count=1` 通过，包含所有真实依赖集成用例。
- 实际 `cmd/geo-rebuild` 成功构建两个示例分类（分别 2/1 个商户），实际 `cmd/server` HTTP 查询返回两个同类附近商户，距离递增，最近距离为约 0.1904 米；验证进程已停止。

GEO 测试只清理自身分类的 index/ready key 和样本数据，没有使用 FLUSHDB。距离来自 Redis 球面近似计算，本轮未声明位置精度或吞吐性能提升。

## Blog / Like 模块验收（2026-09-17）

- Windows Go 1.26.4：`scripts/check.ps1` 格式、全量单元测试、vet 与 build 通过。
- 增量迁移 000002 新增 `blog_like` 和列表索引；原始迁移未修改。真实依赖检查当前版本 2、九张业务表、dirty=false，重复 up 为 no-op。
- Service / HTTP：可信作者身份、JSON 未知字段拒绝、64 KiB 请求体、中文长度边界、分页参数、空集合、缺失目标、认证失效与日志不含正文或 Token 均有验证。
- 真实 MySQL：同时间戳内容按 ID 稳定分页，商户/作者筛选取交集；20 个并发重复点赞后计数为 1，另一个用户点赞后为 2，20 个重复取消后保留另一用户的 1 个赞。
- 真实 Redis Session 与 HTTP：发布、公开读取、重复点赞、取消、个人点赞状态、会话删除后拒绝写入通过。
- Docker Linux / Go 1.26.4：完整 `go test -race ./... -count=1` 通过，包含所有真实依赖集成测试。
- 实际 `cmd/server` 临时回环端口：验证码登录→发布中文内容→重复点赞→详情计数 1→取消后计数 0→按作者查询→退出后点赞返回 401，完整链路通过。验证进程已停止，创建的内容、点赞和用户已清理。
- 独立代码审查通过，审查者另行重跑 Service、Handler 与迁移资源测试，无待修复问题。

此阶段的点赞计数直接来自 MySQL 关系表，无 Redis 计数器或热点榜单；没有做吞吐提升声明。

## Follow / Feed 模块验收（2026-09-17）

- Windows Go 1.26.4：`scripts/check.ps1` 格式、全量单元测试、vet 与 build 通过。
- Service / HTTP：登录身份、禁止自关注、缺失目标、参数边界、禁止缓存、关注列表公开字段投影和错误映射通过。
- 真实 MySQL：16 个并发重复关注最终只保留一条关系；重复取消、关注状态、取消后过滤与重新关注的时间边界通过。
- 真实 Redis：9 条内容中 7 条同分，按每页 2 条持续读取；第一页后插入同分迟到内容并重建，旧快照仍完整读取原 9 条，无重复或丢失，新快照读取 10 条。
- 快照：用户隔离、固定 5 分钟 TTL、缺失快照返回 409、过滤后空页仍推进游标、空 Feed 与重复推送幂等均有测试。
- 失败恢复：关闭 Redis 客户端后发布返回 503，MySQL 内容仍存在；恢复连接后重建得到正确内容 ID 与时间分值。重复条目导致重建校验失败时，旧 live 索引保持可读。
- 独立代码审查完成；发现游标缺字段/null 被默认值接受的问题，六组回归用例先复现失败，修复后返回 400 且不访问 FeedStore；复审通过。
- Docker Linux / Go 1.26.4 / GCC：修复后完整 `go test -race ./... -count=1` 通过，包含所有真实依赖集成测试。
- 实际 `cmd/server` 和 `cmd/feed-rebuild`：两个用户登录→重复关注后列表 1 人→发布 6 条→读取首页→发布第 7 条→CLI 重建→旧快照恰好 6 条、新首页 7 条→取消关注后可见 0 条→退出后 Feed 返回 401。验证服务已停止，样本内容、关注关系和用户已清理。

本阶段完成可运行的同步 Push Feed，恢复依赖受控重建；尚无自动投递重试或容量限制。快照首屏复制与发布扇出成本待后续 Benchmark 测量，不声明吞吐收益。

## Voucher 模块验收（2026-09-17）

- Windows Go 1.26.4：`scripts/check.ps1` 格式、全量单元测试、vet 与 build 通过。
- Service：活动开始前、开始时刻、结束前、结束时刻与结束后均有明确用例；验证未开始/已结束优先于售罄，同一页只读取一次当前时间。
- 编码：普通券 `seckill:null`，ID 与整数分金额采用 JSON 字符串；覆盖大于 JavaScript 安全整数范围及 uint64 最大值的精度。
- HTTP：公开查询、默认与自定义分页、商户筛选、非法/重复/空值/溢出参数、Request ID、deadline、`no-store` 和 400/404/408/503/504 响应通过。
- 真实 MySQL：一次 LEFT JOIN 映射普通券与四种活动状态，按商户跨页查询有序且不混入其他商户；不存在商户/空页返回空数组，不存在券返回 404。
- 修改活动库存为 0 后，下一次查询立即返回最新库存及 `sold_out`；关闭数据库连接后，详情与列表均映射为脱敏 503，取消的查询保留 408 语义。
- Docker Linux / Go 1.26.4 / GCC：完整 `go test -race ./... -count=1` 通过，包含全部真实 MySQL/Redis 集成测试。
- 实际 `cmd/server`：临时回环端口公开查询两个示例券、详情与按商户分页成功；非法页大小返回 400，不存在的券返回 404。验证进程已停止。
- 示例脚本顺序重复执行：一张普通券、一张活动券，无重复插入；将库存从 20 改为 19 后再次导入，库存仍为 19，活动开始和结束时间均未变化。
- 独立代码审查完成，无待修复问题；审查者另行重跑 Voucher 的 Service / Handler 测试通过。

本阶段只完成优惠券与活动查询，未将查询状态视为库存预留或下单成功；秒杀事务、订单查询和并发不超卖验证在 Seckill V1 阶段实现。

## Seckill V1 与基础业务闭环验收（2026-09-17）

- Windows Go 1.26.4：`scripts/check.ps1` 格式、全量单元测试、vet 与 build 通过。
- Service：活动开始前/开始时刻/结束前/结束时刻/结束后、重复购买、库存条件更新失败、各事务阶段故障和提交失败均有验证。提交失败不返回已生成的订单作为成功结果。
- HTTP：可信登录身份、请求体拒绝、ID 字符串、分页边界、409 业务错误码、`no-store` 与鉴权通过。
- 真实 MySQL 8.4：40 个用户同时提交 80 次请求，初始库存 15；成功 15、重复购买 15、售罄 50，最终库存 0、订单 15、不同购买用户 15，无超卖或重复订单。
- 回滚：扣库存后因用户外键失败而插单失败，库存保持不变；绕过 Service 重复检查、直接触发订单唯一约束，同样回滚库存扣减。
- 锁等待：阻塞活动行后，等待请求达到 deadline 返回 timeout 且无库存/订单变更；活动在等待期间结束，获得锁后返回 `activity_ended`。
- 真实 Redis Session 与订单 HTTP：成功下单、重复拒绝、本人列表/详情、其他用户详情 404、跨用户列表隔离、按券筛选与降序分页、注销后 401 通过。
- 数据库不可用时，下单、详情与列表均返回安全的 503，不暴露驱动错误。
- Docker Linux / Go 1.26.4 / GCC：完整 `go test -race ./... -count=1` 通过，包含全部真实依赖集成测试。
- 独立代码审查无待修复问题，审查者独立重跑 Service / Handler / 错误映射单元测试通过，并核对 GORM 的提交错误传播。
- 实际 `cmd/geo-rebuild`、`cmd/server` 完整链路：两个账号登录→查询商户/GEO（附近 2 家）→关注→发布→点赞→Feed（1 条、1 个赞）→查询活动券→下单（库存 20→19）→重复购买 409→本人订单 1 条→他人详情 404→退出后订单 401。临时服务已停止，本轮创建的订单、内容、关系与用户已清理。

基础业务演示链路已具备。上述并发用例用于正确性验收，不作为 QPS、P95/P99 或性能提升数据；缓存治理、异步秒杀、恢复验证、指标和负载对照仍在 [剩余清单](progress.md) 中。

## 首次构建记录（2026-09-17，历史）

- 环境：Windows amd64，Go 1.26.4。
- 已运行通过：配置/错误/HTTP/生命周期/连接配置/迁移资源测试，`scripts/check.ps1`（含格式、test/vet/build）、`go mod verify`；Linux amd64 的交叉编译也通过。
- 启动失败路径：实际运行 `go run ./cmd/server`，MySQL 不可用时输出脱敏诊断并以状态 1 退出。
- 独立审查发现的启动取消排空、超时响应 JSON 拼接问题，均通过先失败后通过的回归测试修复，并已复审。
- 真实 MySQL/Redis 集成：未运行；Windows 没有 Docker CLI，Docker Desktop WSL 仅有提示入口且无可用引擎，3306/6379 没有可用服务。
- Race：默认 CGO 关闭时命令拒绝运行；再次设置 `CGO_ENABLED=1` 后确认报错 `C compiler "gcc" not found`，尚未运行竞态检测。
- Benchmark：未实现、未运行，Phase 0 不报告性能数字。

以上历史环境限制现已通过 Docker 测试环境解决，当前状态以各模块验收记录为准。
