# kirocli-go Architecture

## 1. 目标

`kirocli-go` 的目标不是做第三个风格混乱的代理，而是明确分成三部分：

- 数据面：稳定接 OpenAI / Anthropic 客户端请求，并用低风控方式访问上游。
- 控制面：管理账号、模型、状态、统计、封禁与后台刷新。
- 核心层：统一消息模型、统一流式事件、统一错误分类和模型解析。

V1 的首要原则：

- 默认只走 CLI 风格上游链路。
- 不混用 CLI token 和 IDE 指纹。
- 不把账号源、上游 transport、协议格式化硬编码到一个 handler 中。

## 2. 设计原则

### 2.1 单一事实来源

所有外部请求先进入统一内部模型，再决定如何发往上游、如何回给客户端。

### 2.2 指纹与业务解耦

低风控能力是 transport/profile 的职责，不应该散落在 handler、translator、token 刷新逻辑里。

### 2.3 控制面与数据面分离

账号管理、后台刷新、导入导出、模型目录刷新属于控制面，不应和请求主链路耦合在一起。

### 2.4 可替换接口优先

以下能力必须抽象为 port：

- token 获取与回收
- 上游请求发送
- 账号存储
- 模型目录
- 客户端响应格式化

### 2.5 V1 先稳，不先全

V1 优先保证：

- OpenAI `/v1/chat/completions`
- Anthropic `/v1/messages`
- `/v1/models`
- `/v1/messages/count_tokens`

MCP、admin UI、导出导入、额外上游 profile 放在第二阶段接入。

## 3. 目标仓库结构

```text
kirocli-go/
├── cmd/
│   └── server/
├── internal/
│   ├── domain/
│   │   ├── account/
│   │   ├── message/
│   │   ├── model/
│   │   ├── stream/
│   │   └── errors/
│   ├── application/
│   │   ├── chat/
│   │   ├── catalog/
│   │   ├── auth/
│   │   ├── admin/
│   │   ├── scheduler/
│   │   └── background/
│   ├── ports/
│   │   ├── token.go
│   │   ├── upstream.go
│   │   ├── store.go
│   │   ├── formatter.go
│   │   └── catalog.go
│   ├── adapters/
│   │   ├── upstream/
│   │   │   ├── clihttp/
│   │   │   └── idehttp/
│   │   ├── token/
│   │   │   ├── csv/
│   │   │   ├── api/
│   │   │   └── configstore/
│   │   ├── store/
│   │   │   └── jsonfile/
│   │   ├── http/
│   │   │   ├── openai/
│   │   │   ├── anthropic/
│   │   │   ├── models/
│   │   │   ├── admin/
│   │   │   └── middleware/
│   │   ├── formatter/
│   │   │   ├── openai/
│   │   │   └── anthropic/
│   │   └── mcp/
│   │       └── websearch/
│   └── bootstrap/
└── docs/
```

## 4. 分层职责

## 4.1 Domain

Domain 只定义稳定概念，不依赖 Gin、HTTP 请求头或具体 AWS 端点。

建议保留的核心对象：

- `UnifiedRequest`
- `UnifiedMessage`
- `UnifiedTool`
- `UnifiedToolResult`
- `UnifiedStreamEvent`
- `ResolvedModel`
- `AccountState`
- `UpstreamError`

这些对象是整个仓库的“内部语言”。

## 4.2 Application

Application 负责编排，不直接关心文件存储或具体 header 细节。

关键服务：

- `ChatService`
  - 校验请求
  - 归一化消息
  - 解析模型
  - 获取 token
  - 调用上游
  - 将上游事件交给 formatter
- `ModelCatalogService`
  - 刷新模型目录
  - 返回客户端可见模型
  - 生成 thinking 变体
- `AccountService`
  - 账号导入、禁用、封禁、状态回写
- `SchedulerService`
  - 选号、冷却、权重、额度优先级
- `BackgroundJobs`
  - token 刷新
  - 模型目录刷新
  - 账号元数据刷新
  - 统计持久化

## 4.3 Ports

推荐一开始就固定这些接口：

```go
type TokenProvider interface {
    Acquire(ctx context.Context, hint AcquireHint) (Lease, error)
    ReportSuccess(ctx context.Context, lease Lease, meta SuccessMeta) error
    ReportFailure(ctx context.Context, lease Lease, meta FailureMeta) error
}

type UpstreamClient interface {
    Send(ctx context.Context, req UpstreamRequest) (UpstreamStream, error)
}

type AccountStore interface {
    List(ctx context.Context) ([]AccountRecord, error)
    Save(ctx context.Context, record AccountRecord) error
    UpdateStatus(ctx context.Context, id string, status AccountStatus) error
}

type ModelCatalog interface {
    Resolve(ctx context.Context, externalModel string) (ResolvedModel, error)
    List(ctx context.Context) ([]ResolvedModel, error)
}

type Formatter interface {
    WriteStream(ctx context.Context, stream UpstreamStream, w io.Writer) error
    WriteJSON(ctx context.Context, stream UpstreamStream, w io.Writer) error
}
```

## 4.4 Adapters

Adapters 是最容易变的部分。

重点拆成四块：

- `adapters/upstream/clihttp`
  - 保留 CLI 风格 UA、header、origin、`uTLS`、代理能力
- `adapters/upstream/idehttp`
  - 作为实验 profile，不进入默认链路
- `adapters/token/*`
  - CSV
  - 外部 API
  - 本地控制面存储
- `adapters/http/*`
  - OpenAI / Anthropic / Models / Admin 路由与 handler

## 5. 请求主链路

## 5.1 OpenAI / Anthropic 数据面

```text
Client Request
  -> HTTP Handler
  -> Request Decoder
  -> UnifiedRequest
  -> Model Resolver
  -> TokenProvider.Acquire
  -> Upstream Encoder
  -> CLI Upstream Client
  -> UnifiedStreamEvent Decoder
  -> OpenAI / Anthropic Formatter
  -> Client Response
```

关键要求：

- handler 不直接拼 AWS header
- token 失效、quota、封禁信号必须通过 `ReportFailure` 统一回流
- formatter 只消费统一事件，不反向依赖上游 payload 结构

## 5.2 控制面链路

```text
Admin API
  -> AccountService / CatalogService
  -> AccountStore / ModelCatalog
  -> Background Jobs State
```

控制面负责：

- 导入账号
- 查看状态
- 手动禁用/启用
- 更新权重
- 刷新模型目录
- 导出账号
- 查看请求统计

## 6. 上游 Profile 设计

这里必须单独强调，因为这是风控边界。

定义两个 profile：

- `cli`
  - 默认 profile
  - 继承 `kirocli2api`
  - 含 `uTLS`、CLI header、CLI origin、CLI OIDC 刷新
- `ide`
  - 非默认 profile
  - 继承 `kiro-go`
  - 含 `machineId`、IDE 风格 UA、可选双端点试验

设计红线：

- 一个请求只能命中一个 profile
- `TokenProvider` 需要知道 lease 属于哪个 profile
- 不允许在 `cli` profile 中混入 `KiroIDE-*` header
- 不允许在 `ide` profile 中重用 CLI envState 伪装

## 7. 统一模型层

新仓不要再保留两套互相平行的“OpenAI 转 Q”和“Anthropic 转 Q”大文件。

应该拆成：

- `request decoder`
  - OpenAI request -> UnifiedRequest
  - Anthropic request -> UnifiedRequest
- `message normalizer`
  - 处理 system、tool、tool_result、图片、thinking
- `upstream encoder`
  - UnifiedRequest -> UpstreamRequest
- `response formatter`
  - UnifiedStreamEvent -> OpenAI / Anthropic 输出

其中：

- `kiro-go/proxy/converters/core.go` 更适合作为 message normalizer 参考
- `kirocli2api/Utils/Openai2Q.go` 和 `Utils/Anthropic2Q.go` 更适合作为上游 encoder 行为参考

## 8. 错误与风控处理

统一错误分类，不要在 handler 中靠字符串散判。

至少要统一成四类：

- `auth_error`
- `quota_error`
- `ban_error`
- `network_error`

推荐链路：

1. `UpstreamClient` 返回原始响应或 transport error
2. `ErrorClassifier` 输出结构化错误
3. `TokenProvider.ReportFailure` 决定：
   - 标记失效
   - 冷却
   - 封禁
   - 降级重试
4. `Formatter` 决定输出成 OpenAI/Anthropic 风格错误

## 9. 后台任务

后台任务不要藏在 handler 初始化里到处扩散，统一放在 `application/background`。

建议首批任务：

- `TokenRefreshJob`
- `ModelCatalogRefreshJob`
- `AccountMetadataRefreshJob`
- `StatsPersistJob`
- `BanReconcileJob`

## 10. 存储策略

V1 建议支持三类状态来源：

- `Runtime State`
  - 活跃 lease
  - 冷却状态
  - 请求统计
- `Control Plane Store`
  - 账号
  - 配置
  - thinking 设置
  - endpoint/profile 设置
- `External Source`
  - CSV
  - 外部账号 API

建议默认本地实现用 JSON file，后续可换 SQLite，但 application 层不感知。

## 11. V1 实施顺序

### Phase 0

- 仓库骨架
- ports 定义
- unified model 定义

### Phase 1

- CLI transport
- token provider
- OpenAI / Anthropic 请求解码
- 统一 event 解码

### Phase 2

- model resolver
- models endpoint
- count_tokens
- 错误分类与 lease 回写

### Phase 3

- admin API
- account store
- 账号导入导出
- 后台刷新

### Phase 4

- MCP/web_search
- fake cache usage
- truncation recovery
- ide profile 实验

## 12. 明确不做的事情

V1 不做：

- 直接把 `kiro-go/proxy/handler.go` 或 `kirocli2api/API/Messages.go` 原样复制进来
- 在主链路里同时支持 CLI 和 IDE 混合 header
- 把 token 刷新、协议转换、流式组包继续塞进一个文件
- 先上 UI 再整理核心接口

这几个坑如果不先避开，新仓很快会复刻旧仓问题。
