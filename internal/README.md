# internal

这里放 `kirocli-go` 的核心实现。

建议按四层组织：

- `internal/domain`: 统一消息模型、流式事件、账号状态、错误分类
- `internal/application`: 请求编排、模型解析、账号调度、后台任务
- `internal/ports`: 对外抽象接口，如 `TokenProvider`、`UpstreamClient`、`AccountStore`
- `internal/adapters`: 具体实现，如 CLI transport、CSV/API token source、JSON store、HTTP handlers

目标是把“低风控 transport”“协议转换”“控制面能力”解耦，而不是继续堆在单个 handler 文件里。
