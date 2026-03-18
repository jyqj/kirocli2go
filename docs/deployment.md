# Deployment

## 最小启动条件

至少需要满足两项：

1. 配置 `KIROCLI_GO_API_TOKEN`
2. 提供一种账号来源

最简单的本地方式：

```bash
cd kirocli-go
cp .env.example .env
```

然后至少填写：

```bash
KIROCLI_GO_API_TOKEN=change_me
KIROCLI_GO_ACCOUNT_SOURCE=env
KIROCLI_GO_BEARER_TOKEN=...
```

## 启动

```bash
go run ./cmd/server
```

默认端口：

- API: `http://127.0.0.1:8089`
- Admin UI: `http://127.0.0.1:8089/admin`

## 推荐目录

```text
kirocli-go/
├── .env
├── data/
│   ├── accounts_state.json
│   ├── stats_state.json
│   └── catalog_state.json
└── logs/
```

## 后台任务

默认启用：

- 启动后模型目录预热刷新
- 周期模型目录刷新
- 周期状态持久化

相关配置：

- `KIROCLI_GO_MODEL_REFRESH_ENABLED`
- `KIROCLI_GO_MODEL_REFRESH_ON_START`
- `KIROCLI_GO_MODEL_REFRESH_INTERVAL_SEC`
- `KIROCLI_GO_STATE_PERSIST_ENABLED`
- `KIROCLI_GO_STATE_PERSIST_INTERVAL_SEC`

## 常用检查

健康检查：

```bash
curl http://127.0.0.1:8089/health
```

带鉴权查看状态：

```bash
curl http://127.0.0.1:8089/v1/stats \
  -H "Authorization: Bearer $KIROCLI_GO_API_TOKEN"
```

手动刷新模型目录：

```bash
curl -X POST http://127.0.0.1:8089/admin/api/models/refresh \
  -H "Authorization: Bearer $KIROCLI_GO_API_TOKEN"
```

## 运行期文件

### `accounts_state.json`

保存：

- 手动导入的 managed 账号
- 外部来源账号的 enable/disable/weight/cooldown override

### `stats_state.json`

保存：

- 请求数
- token 计数
- credits
- retry 统计

### `catalog_state.json`

保存：

- 上一次模型目录刷新时间
- 最近一次错误
- 最近缓存的模型 ID
