# kirocli-go

`kirocli-go` 是新的 2api 重构仓库，目标不是简单拼接 `kiro-go` 和 `kirocli2api`，而是用新的分层架构吸收两边最有价值的部分：

- 数据面默认继承 `kirocli2api` 的低风控链路：CLI 指纹、代理、可替换 token 来源。
- 控制面吸收 `kiro-go` 的管理能力：账号状态、模型目录、运行统计、后台刷新。
- 核心层统一请求模型、流式事件和错误分类，避免双实现继续分叉。

当前仓库已经具备：

- OpenAI `/v1/chat/completions`
- Anthropic `/v1/messages`
- `/v1/models`
- `/v1/messages/count_tokens`
- `/v1/stats`
- `/admin` 管理页面
- `/admin/api/*` 控制面接口
- 账号导入/启停/删除/刷新/调权/导出
- 运行时 stats 持久化
- 模型目录缓存与后台刷新

## 快速开始

### 1. 配置

```bash
cd kirocli-go
cp .env.example .env
```

至少要配置：

- `KIROCLI_GO_API_TOKEN`
- 一种账号来源：
  - `KIROCLI_GO_ACCOUNT_SOURCE=env` + `KIROCLI_GO_BEARER_TOKEN`
  - 或 `csv/api` 相关变量

### 2. 启动

```bash
go run ./cmd/server
```

默认监听：

- API: `http://127.0.0.1:8089`
- Admin UI: `http://127.0.0.1:8089/admin`

### 3. 常用端点

- `POST /v1/chat/completions`
- `POST /v1/messages`
- `GET /v1/models`
- `GET /v1/stats`
- `GET /admin/api/status`

## 文档入口

- [Architecture](docs/architecture.md)
- [Reference Mapping](docs/reference-mapping.md)
- [Deployment](docs/deployment.md)
- [Admin API](docs/admin-api.md)

## 目录概览

```text
kirocli-go/
├── README.md
├── cmd/
├── docs/
│   ├── architecture.md
│   └── reference-mapping.md
└── internal/
```

## 部署说明

状态文件默认写入：

- `data/accounts_state.json`
- `data/stats_state.json`
- `data/catalog_state.json`

可通过环境变量改掉：

- `KIROCLI_GO_ACCOUNT_STATE_PATH`
- `KIROCLI_GO_STATS_STATE_PATH`
- `KIROCLI_GO_CATALOG_STATE_PATH`

后台任务默认开启：

- 启动后首轮模型目录刷新
- 周期模型目录刷新
- 周期状态持久化

相关配置：

- `KIROCLI_GO_MODEL_REFRESH_ENABLED`
- `KIROCLI_GO_MODEL_REFRESH_ON_START`
- `KIROCLI_GO_MODEL_REFRESH_INTERVAL_SEC`
- `KIROCLI_GO_STATE_PERSIST_ENABLED`
- `KIROCLI_GO_STATE_PERSIST_INTERVAL_SEC`

## 设计约束

- V1 默认只启用 CLI 风格上游链路，不在默认链路里混入 IDE 指纹。
- 所有协议转换都先进入统一内部模型，再由格式化器输出 OpenAI / Anthropic。
- token 来源、账号存储、上游 transport 都必须可替换，避免再次把实现绑死在单一文件里。
- 管理面和数据面分离，管理功能不能反向污染请求主链路。

## 当前重点

1. 收口 admin 页面体验与部署说明。
2. 继续把运行时状态与控制面能力做完整。
3. 在稳定后再考虑前端拆分或更复杂的后台任务。
