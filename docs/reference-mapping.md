# Reference Mapping

这份文档定义 `kiro-go` 和 `kirocli2api` 到 `kirocli-go` 的迁移归属。

目标不是“照搬文件”，而是给每块旧实现明确一个处理策略：

- `Adopt`: 可以直接吸收核心逻辑，允许小改
- `Rewrite`: 只保留行为与规则，按新架构重写
- `Reference`: 只作为行为参考，不进入主实现
- `Drop`: 不进入新仓默认版本

## 1. 能力归属总览

| 能力 | 主参考来源 | 新仓归属 |
| --- | --- | --- |
| 低风控 transport | `kirocli2api` | `internal/adapters/upstream/clihttp` |
| token 池与外部账号源 | `kirocli2api` | `internal/adapters/token/*` |
| 账号状态与后台刷新 | `kiro-go` | `internal/application/account` + `background` |
| 模型目录与模型解析 | `kiro-go` | `internal/application/catalog` + `domain/model` |
| OpenAI / Anthropic 内部统一模型 | 两者结合 | `internal/domain/message` |
| 上游请求编码 | 两者结合 | `internal/application/chat` + `adapters/upstream/*` |
| OpenAI / Anthropic 输出格式化 | 两者结合 | `internal/adapters/formatter/*` |
| 管理接口 | `kiro-go` | `internal/adapters/http/admin` |
| MCP/web_search | `kirocli2api` | `internal/adapters/mcp/websearch` |
| 错误增强与网络错误分类 | `kiro-go` | `internal/domain/errors` + `application/chat` |

## 2. `kirocli2api` -> `kirocli-go`

| 旧文件 | 新模块 | 策略 | 说明 |
| --- | --- | --- | --- |
| `API/ChatCompletions.go` | `internal/adapters/http/openai` + `internal/application/chat` + `internal/adapters/formatter/openai` | Rewrite | 入口、编排、流式输出要拆开，不能继续单文件承载 |
| `API/Messages.go` | `internal/adapters/http/anthropic` + `internal/adapters/mcp/websearch` + `internal/adapters/formatter/anthropic` | Rewrite | 当前文件承担过多职责，需拆为 Anthropic handler、formatter、MCP adapter |
| `API/Models.go` | `internal/adapters/http/models` + `internal/application/catalog` | Rewrite | 模型目录不应直接在 handler 里请求上游 |
| `API/CountTokens.go` | `internal/adapters/http/anthropic` + `internal/application/chat/tokenestimate` | Adopt | 逻辑简单，可以保留行为 |
| `Middleware/Auth.go` | `internal/adapters/http/middleware/auth.go` | Adopt | 保留 header 校验模式，但支持未来 admin/data plane 分离 |
| `Utils/Proxy.go` | `internal/adapters/upstream/clihttp/transport.go` | Adopt | 这是 `kirocli2api` 最值得保留的核心实现 |
| `Utils/GetBearer.go` | `internal/adapters/token/csv` + `internal/adapters/token/api` + `internal/application/auth` + `internal/application/scheduler` | Rewrite | 必须拆开；当前文件同时处理加载、刷新、禁用、补位、API 回写，耦合太高 |
| `Utils/Openai2Q.go` | `internal/application/chat/openai_decoder.go` + `internal/application/chat/upstream_encoder.go` | Rewrite | 只保留消息映射规则和 tool_result 处理逻辑 |
| `Utils/Anthropic2Q.go` | `internal/application/chat/anthropic_decoder.go` + `internal/application/chat/upstream_encoder.go` | Rewrite | 系统提示、长 tool doc、thinking 注入规则可保留 |
| `Utils/Q2Openai.go` | `internal/application/chat/upstream_event_decoder.go` | Rewrite | 保留 tool accumulator 思路，但输出统一事件，不直接产出 OpenAI 结构 |
| `Utils/Validation.go` | `internal/application/chat/validation.go` | Adopt | 简单校验逻辑可直接吸收 |
| `Utils/Logger.go` | `internal/adapters/observability/logger.go` | Reference | 保留日志落盘思路，但不要继续只分 normal/error 两个全局 logger |
| `Models/OpenAI.go` | `internal/adapters/http/openai/schema.go` | Adopt | 外部协议结构适合保留 |
| `Models/Anthropic.go` | `internal/adapters/http/anthropic/schema.go` | Adopt | 外部协议结构适合保留 |
| `Models/Q.go` | `internal/adapters/upstream/qschema.go` | Adopt | 上游请求/事件结构可保留为协议层 schema |
| `Models/Tokens.go` | `internal/domain/account/token.go` | Rewrite | 结构可参考，但要和 lease/profile 概念对齐 |
| `API/DebugToken.go` | `internal/adapters/http/admin/debug_token.go` | Drop | 不进入默认公开路由 |
| `API/DebugAnthropic2Q.go` | `internal/adapters/http/admin/debug_mapping.go` | Drop | 只可作为受保护调试接口，默认不开放 |
| `router.go` | `internal/bootstrap/http.go` | Rewrite | 路由注册要区分 data plane 与 admin plane |
| `main.go` | `cmd/server/main.go` | Rewrite | 只保留启动流程，不保留当前全局初始化方式 |

## 3. `kiro-go` -> `kirocli-go`

| 旧文件 | 新模块 | 策略 | 说明 |
| --- | --- | --- | --- |
| `config/config.go` | `internal/config` + `internal/adapters/store/jsonfile` + `internal/application/admin/settings.go` | Rewrite | 配置、账号持久化、运行统计要拆分，不能再用单大对象承载所有状态 |
| `pool/account.go` | `internal/application/scheduler/account_scheduler.go` | Adopt | 冷却、权重、使用量优先级都值得保留 |
| `proxy/model_resolver.go` | `internal/domain/model/resolver.go` + `internal/application/catalog/resolver_service.go` | Adopt | 这是最适合直接吸收的模块之一 |
| `proxy/kiro_api.go` | `internal/adapters/upstream/catalog_client.go` + `internal/application/account/metadata_refresh.go` | Rewrite | 保留模型目录和额度刷新行为，拆掉和旧 config 的直接耦合 |
| `proxy/kiro.go` | `internal/adapters/upstream/idehttp/client.go` | Reference | 作为 `ide` profile 参考，不进入 V1 默认链路 |
| `proxy/translator.go` | `internal/application/chat/*` | Reference | 保留 thinking 兼容和 tool name 裁剪规则，但不保留整文件结构 |
| `proxy/converters/core.go` | `internal/domain/message/normalize.go` + `internal/application/chat/message_pipeline.go` | Adopt | 统一消息模型、角色交替、tool schema 清理非常有价值 |
| `proxy/streaming/core.go` | `internal/adapters/formatter/openai` + `internal/adapters/formatter/anthropic` | Adopt | 可作为 formatter 的实现基础 |
| `proxy/errors/kiro.go` | `internal/domain/errors/upstream_classifier.go` | Adopt | 上游 reason 到用户友好错误的映射值得保留 |
| `proxy/errors/network.go` | `internal/domain/errors/network_classifier.go` | Adopt | 网络错误分类也适合独立保留 |
| `proxy/thinking_parser.go` | `internal/application/chat/thinking_parser.go` | Reference | 设计比当前主链路更好，但旧仓没有完全接上；新仓可按此思路重建 |
| `proxy/truncation.go` | `internal/application/chat/truncation_policy.go` | Adopt | 属于清晰的独立能力 |
| `proxy/fake_cache.go` | `internal/application/chat/cache_simulator.go` | Adopt | Anthropic fake cache usage 可以直接迁 |
| `proxy/handler.go` | `internal/adapters/http/*` + `internal/application/*` | Rewrite | 绝不能整文件搬；这是拆分对象的主要来源 |
| `auth/oidc.go` | `internal/adapters/token/refresh/oidc.go` | Adopt | token 刷新逻辑可直接作为 adapter |
| `auth/builderid.go` | `internal/application/admin/import_builderid.go` + `internal/adapters/token/importer/builderid.go` | Rewrite | 登录会话与导入逻辑要拆开 |
| `auth/iam_sso.go` | `internal/application/admin/import_iam_sso.go` + `internal/adapters/token/importer/iam_sso.go` | Rewrite | 同上 |
| `auth/sso_token.go` | `internal/application/admin/import_sso_token.go` | Adopt | SSO token 导入很有价值 |
| `auth/http_client.go` | `internal/adapters/upstream/idehttp/http_client.go` | Reference | 只在 `ide` profile 使用 |
| `web/index.html` | `ui/admin` 或独立前端仓 | Drop | 先不进入新仓核心重构范围 |
| `README.md` 功能清单 | `docs/architecture.md` 的验收能力 | Reference | 作为需求检查表，而不是实现来源 |

## 4. 明确不直接继承的部分

以下内容不要直接复制到新仓默认实现：

| 来源 | 内容 | 原因 |
| --- | --- | --- |
| `kiro-go` | IDE 风格默认 UA / `machineId` / `x-amzn-kiro-agent-mode` | V1 默认不走这条链路，避免与 CLI token 混用 |
| `kiro-go` | `proxy/handler.go` 的大一统处理方式 | 文件职责过载，不适合重构起点 |
| `kirocli2api` | `Utils/GetBearer.go` 的全局状态 + `panic` 方式 | 工程风险高，且难测 |
| `kirocli2api` | 未鉴权 debug 路由 | 不适合进入默认生产仓 |
| 两边 | 各自平行的“协议转换 + SSE 输出 + 上游调用”耦合写法 | 新仓必须拆成 decoder / encoder / event / formatter |

## 5. V1 默认技术选择

新仓 V1 默认选择如下：

| 维度 | 选择 | 原因 |
| --- | --- | --- |
| 上游 profile | `cli` | 保留低风控优势 |
| transport | `kirocli2api` 风格 `uTLS + proxy` | 这是数据面核心优势 |
| token source | CSV + 外部 API + 本地控制面 | 同时兼容现有供应链和未来自托管 |
| 模型解析 | `kiro-go` `model_resolver` | 行为成熟、规则清晰 |
| 消息标准化 | `kiro-go` `converters/core` 为主 | 更接近统一内部模型 |
| 响应格式化 | 两边合并重写 | 避免重复 SSE 逻辑长期分叉 |
| 控制面 | 以 `kiro-go` 设计为主，按新仓接口重写 | 功能成熟，但旧实现耦合高 |

## 6. 第一批落地顺序

建议按下面顺序实现，能最快形成可跑的 V1：

1. `ports`
   - `TokenProvider`
   - `UpstreamClient`
   - `ModelCatalog`
   - `AccountStore`
2. `clihttp transport`
3. `token/api` 与 `token/csv`
4. `UnifiedRequest` 与消息标准化
5. OpenAI + Anthropic data plane
6. `models` + `count_tokens`
7. 错误分类与失败回写
8. 控制面与后台任务
9. MCP/web_search
10. `ide` profile 试验

## 7. 一个关键决定

新仓里不再存在“谁是内核，谁是外壳”的旧问题。

新的内核应该是：

- 统一内部模型
- 统一 ports
- 统一调度与错误回流

而 `kiro-go` 和 `kirocli2api` 都只是参考实现来源，不再是架构中心。
