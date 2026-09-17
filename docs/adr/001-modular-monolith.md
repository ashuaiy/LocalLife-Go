# ADR 001：采用模块化单体

状态：Accepted，Phase 0。

## Context

本地生活业务包含 User、Shop、Feed 和 Seckill。当前目标是在单人可运行的环境里验证缓存、事务、异步处理与 Go 并发设计，同时能解释每个选择。

## Decision

使用 Go + Hertz + GORM + MySQL + Redis；统一可执行文件，按 Handler / Service / Repository 分层，连接由启动入口装配和管理。

Hertz 使用标准网络 transport，使 Windows 开发和后续 Linux 运行共享实现。暂不对比其他 transport 的性能，也不宣称该选择提升吞吐。

## Alternatives / Why not alternatives

- 微服务：可独立部署扩容，但目前没有独立团队和可测的隔离收益，会先增加 RPC、故障传播和跨服务一致性成本。
- 全部放在 Handler：文件更少，但业务规则难以单测，也无法清晰传递事务边界。
- 全内存演示：启动方便，但无法验证 MySQL 约束和 Redis 行为。

## Consequences

调试、部署和本地测试简单；模块之间仍共享进程和容量，包依赖需要保持单向。统一进程不是无限扩展方案。

## When to revisit

发现单个模块持续需要不同部署节奏或资源隔离，并有 profiling/负载测试证据时，再评估拆分；不能仅因为流量预期或增加技术栈种类而拆分。

## Validation

Phase 0 验证配置、错误映射、HTTP 和生命周期；后续通过真实依赖测试和压测逐项证明业务正确性。未测量性能前不记录性能收益。
