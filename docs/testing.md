# 基础框架与用户模块验证

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

检查项：八张表、migration 版本和 dirty 状态、重复 migration no-op、数据库订单唯一约束、条件扣库存、非负库存约束、GORM context/pool、Redis 读写和 TTL、真实依赖的 readiness 及连接关闭后 503。

这些是基础设施与 schema 验证，**不是秒杀业务并发验收**。秒杀 Service 和负载测试在后续阶段完成。

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

## 首次构建记录（2026-09-17，历史）

- 环境：Windows amd64，Go 1.26.4。
- 已运行通过：配置/错误/HTTP/生命周期/连接配置/迁移资源测试，`scripts/check.ps1`（含格式、test/vet/build）、`go mod verify`；Linux amd64 的交叉编译也通过。
- 启动失败路径：实际运行 `go run ./cmd/server`，MySQL 不可用时输出脱敏诊断并以状态 1 退出。
- 独立审查发现的启动取消排空、超时响应 JSON 拼接问题，均通过先失败后通过的回归测试修复，并已复审。
- 真实 MySQL/Redis 集成：未运行；Windows 没有 Docker CLI，Docker Desktop WSL 仅有提示入口且无可用引擎，3306/6379 没有可用服务。
- Race：默认 CGO 关闭时命令拒绝运行；再次设置 `CGO_ENABLED=1` 后确认报错 `C compiler "gcc" not found`，尚未运行竞态检测。
- Benchmark：未实现、未运行，Phase 0 不报告性能数字。

以上历史环境限制现已通过 Docker 测试环境解决，最新状态以上方用户模块验收记录为准。
