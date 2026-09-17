# Phase 0 验证

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

```powershell
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

Windows 需要 PATH 上可用的兼容 C 编译器（如 MinGW-w64 GCC）。只设置 CGO_ENABLED 不会安装编译器。Linux 上同样需要 GCC/Clang，测试可在具备这些工具的环境运行。

## 本次构建记录（2026-09-17）

- 环境：Windows amd64，Go 1.26.4。
- 已运行通过：配置/错误/HTTP/生命周期/连接配置/迁移资源测试，`scripts/check.ps1`（含格式、test/vet/build）、`go mod verify`；Linux amd64 的交叉编译也通过。
- 启动失败路径：实际运行 `go run ./cmd/server`，MySQL 不可用时输出脱敏诊断并以状态 1 退出。
- 独立审查发现的启动取消排空、超时响应 JSON 拼接问题，均通过先失败后通过的回归测试修复，并已复审。
- 真实 MySQL/Redis 集成：未运行；Windows 没有 Docker CLI，Docker Desktop WSL 仅有提示入口且无可用引擎，3306/6379 没有可用服务。
- Race：默认 CGO 关闭时命令拒绝运行；再次设置 `CGO_ENABLED=1` 后确认报错 `C compiler "gcc" not found`，尚未运行竞态检测。
- Benchmark：未实现、未运行，Phase 0 不报告性能数字。

实现是可供下一阶段扩展的基础；真实数据库迁移、连接和 race 结果在补齐环境前仍属于待验收项。
