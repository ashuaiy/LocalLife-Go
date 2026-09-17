# ADR 002：显式迁移与应用拥有连接生命周期

状态：Accepted，Phase 0。

## Context

服务需要在 Windows 启动，依赖不可用时应明确失败，连接不能在错误路径泄漏。数据库结构需要可审查、可重复执行的版本记录，并保护订单唯一约束。

## Decision

- 环境变量为唯一运行时配置来源。默认配置对应本地 Compose；解析失败时只报告变量名。
- 启动入口装配 MySQL / Redis，使用带 deadline 的 Ping。初始化中途失败时关闭已打开的资源。
- 请求带有独立 deadline；服务信号控制监听器与排空，之后再关闭连接池。
- 使用标准库 `slog` 记录结构化访问日志，统一恢复策略隐藏原始 panic 值。
- 使用 golang-migrate 和嵌入 SQL，由独立 `cmd/migrate up` 执行。版本表、advisory lock 和 dirty 状态由成熟迁移库管理。
- SQL 定义数据约束，服务不会自动变更 schema。

## Alternatives / Why not alternatives

- YAML + 环境变量覆盖：可表达层级结构，但当前配置有限，会引入优先级和额外解析复杂度。
- GORM AutoMigrate：适合早期试验，但启动即执行 DDL，变更审查和迁移失败恢复路径不够明确。
- 自制 SQL 分割器/迁移账本：依赖少，但容易误处理分号、锁和失败状态。
- 只使用 Hertz Spin：默认信号流程便利；本实现需要让 bind 错误进入退出码，并直接测试 context 取消和监听器关闭，因此显式管理 Run/Shutdown。

## Consequences

运行前多一步迁移命令；MySQL DDL 并不因为用了迁移库就具备整文件事务性，失败可能需要人工检查部分已应用结构。迁移取消在语句边界进行，驱动网络超时和 statement timeout 限制单次等待。

协作式 HTTP deadline 要求业务与驱动传递 context。恢复保护只针对本次请求；后续后台 Consumer 必须建立单独的 lifecycle context 与恢复策略。

## When to revisit

当配置数量或远程分发需求显著增加时评估文件/配置服务。当迁移开始涉及大表在线 DDL 时，单独设计发布窗口与回滚方案。P2 再统一框架日志与链路追踪。

## Validation

测试包括：无效配置、错误脱敏、依赖超时、404/405、panic、端口冲突、真实 HTTP 请求、取消关闭、迁移资源可读取。真实 MySQL/Redis 测试另外检查重复迁移、外键表、唯一订单、库存约束、GORM context 与 Redis TTL；上述测试现已通过，记录见测试指南。
