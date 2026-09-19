# 本地运行

需要 Go 1.26.4、Docker Desktop 和 Docker Compose。以下命令从仓库根目录执行。

## 启动依赖与迁移

首次使用时复制配置；已有 `.env` 时保留自己的配置。

```powershell
if (!(Test-Path .env)) { Copy-Item .env.example .env }
. .\scripts\env.ps1
docker compose up -d --wait
go run ./cmd/migrate up
```

## 可选：导入商户示例

示例包含两个分类、三个虚构商户。脚本不属于数据库迁移，可顺序重复执行；已存在的同分类同名示例不会重复插入，也不会覆盖。通过容器内的环境变量连接该 Compose 实例。

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Get-Content -Raw -Encoding UTF8 .\scripts\seed-demo.sql |
  docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --default-character-set=utf8mb4 --user="$MYSQL_USER" --database="$MYSQL_DATABASE"'
```

## 构建附近商户索引

导入或变更商户数据后运行：

```powershell
go run ./cmd/geo-rebuild
```

该命令按分类从 MySQL 重建 Redis GEO 索引。未构建的分类查询附近商户会返回 503；流程与边界见 [GEO 接口](api/geo.md)。

## 可选：导入优惠券示例

完成迁移和上述商户示例导入后，执行：

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Get-Content -Raw -Encoding UTF8 .\scripts\seed-vouchers.sql |
  docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --default-character-set=utf8mb4 --user="$MYSQL_USER" --database="$MYSQL_DATABASE"'
```

脚本为示例面馆添加一张普通券和一张秒杀券。秒杀库存初始为 20，首次导入时生成七天后结束的活动；顺序重复执行保留已有金额、库存与时间窗，不重新开放已结束活动。这是可选演示数据，不属于数据库迁移。

## 启动 HTTP 服务

```powershell
go run ./cmd/server
```

默认地址为 `http://127.0.0.1:8080`。另开终端查询：

升级已有实例也需先执行 `go run ./cmd/migrate up`。当前迁移版本为 3，新增公开资料字段和笔记图片，不删除已有数据。上传目录通过 `UPLOAD_DIR` 指定（默认 `uploads`），需要写权限；该目录已被 Git 忽略。

```powershell
Invoke-RestMethod http://127.0.0.1:8080/readyz
Invoke-RestMethod http://127.0.0.1:8080/api/v1/shop-types
Invoke-RestMethod http://127.0.0.1:8080/api/v1/vouchers
$page = Invoke-RestMethod 'http://127.0.0.1:8080/api/v1/shops?page=1&page_size=10'
$page.data
if ($page.data.items.Count -gt 0) {
  $shopID = $page.data.items[0].id
  Invoke-RestMethod "http://127.0.0.1:8080/api/v1/shops/$shopID"
  $typeID = $page.data.items[0].type_id
  Invoke-RestMethod "http://127.0.0.1:8080/api/v1/shops/nearby?type_id=$typeID&longitude=121.4737012&latitude=31.2304001&radius_m=5000"
}
```

商户接口无需登录。认证开发流程需在启动服务前设置 `$env:AUTH_DEV_CODES='true'`，并保持 HTTP 绑定回环地址；详见 [认证接口](api/auth.md)。登录后的内容发布与点赞见 [探店接口](api/blog.md)，商户筛选和缓存行为见 [商户接口](api/shop.md)。

资料、签到、共同关注、图片上传与通知用法见 [社区接口](api/community.md)。完整功能匹配与暂缓范围见 [迁移清单](migration.md)。

使用两个不同手机号登录，先让读者关注作者，再由作者发布探店内容，读者即可通过 `GET /api/v1/feed` 读取动态。关注与游标契约见 [关注 Feed](api/feed.md)。

优惠券查询无需登录，可通过 `shop_id` 筛选商户；金额单位、分页与活动状态见 [优惠券接口](api/voucher.md)。

## 秒杀与订单查询

使用已登录用户的 Token 和实际秒杀券 ID：

```powershell
$headers = @{ Authorization = "Bearer $token" }
Invoke-RestMethod "http://127.0.0.1:8080/api/v1/vouchers/$voucherID/seckill" -Method Post -Headers $headers
Invoke-RestMethod "http://127.0.0.1:8080/api/v1/orders?voucher_id=$voucherID" -Headers $headers
```

下单不带请求体；重复购买返回 409 且不再次扣库存。请使用示例秒杀券，普通券没有秒杀活动。活动过期不会因重新导入示例脚本而延期。响应、订单所有权和不确定提交结果的查询方式见 [秒杀订单接口](api/order.md)。

## Feed 索引恢复

Redis 索引丢失或发布后的推送失败时，先暂停内容发布和关注变更，再加载应用配置，为受影响的读者逐个重建：

```powershell
. .\scripts\env.ps1
go run ./cmd/feed-rebuild -user-id 7
```

将 `7` 替换为实际读者 ID，避免同一读者并发重建。完成后恢复写入并刷新 Feed 首页；旧分页快照保持原候选直到 5 分钟后过期。该命令不补发当前关注时间之前的内容，详细边界见 [失败与恢复](api/feed.md#失败与恢复)。

## 维护商户名称与地址

加载应用配置后，使用实际商户 ID：

```powershell
. .\scripts\env.ps1
go run ./cmd/shop-update -id 1 -name "示例·新名称" -address "示例路 2 号"
```

至少提供 `-name` 或 `-address`；省略字段保持原值，`-address=` 清空地址。命令只修改名称、地址，保留坐标和分类，数据库提交后删除详情缓存。若提示数据库已提交但缓存删除失败，待 Redis 恢复后重试相同编辑。该命令使用本地数据库配置，无需 HTTP Token；参数与一致性边界见 [商户维护](api/shop.md#商户维护)。

完成后用 Ctrl+C 停止服务，用 `docker compose down` 停止开发依赖。该命令保留开发数据卷。集成测试使用独立实例，执行方法见 [测试说明](testing.md)。
