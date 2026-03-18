# Admin API

所有 `admin API` 都要求：

- Header: `Authorization: Bearer <KIROCLI_GO_API_TOKEN>`

基准路径：

- `/admin/api`

## 读取接口

### `GET /admin/api/version`

返回版本号。

### `GET /admin/api/config`

返回当前生效的非敏感配置视图。

### `GET /admin/api/doctor`

返回最小诊断结果：

- 状态文件路径检查
- 账号来源是否配置
- 后台任务是否启用

### `GET /admin/api/status`

返回：

- 账号数
- 活跃账号数
- `by_status`
- 累积 stats

### `GET /admin/api/accounts`

返回当前账号快照：

- `id`
- `source`
- `status`
- `weight`
- `disabled`
- `has_bearer`
- `has_refresh`
- `expires_at`
- `cooldown_until`
- `last_used_at`
- `last_error`
- `failures`

### `GET /admin/api/models`

返回：

- 运行时模型目录快照
- 当前客户端可见模型列表

### `GET /admin/api/export`

导出当前账号池视图，包含：

- managed 账号
- 外部来源账号的运行时状态与 override

### `GET /admin/api/request-logs`

查询参数：

- `limit`
- `offset`
- `protocol`
- `endpoint`
- `model`
- `account_id`
- `success`
- `failure_reason`

## 写接口

### `POST /admin/api/accounts/import`

请求体：

```json
{
  "id": "optional-id",
  "weight": 100,
  "bearer_token": "",
  "refresh_token": "",
  "client_id": "",
  "client_secret": ""
}
```

规则：

- `bearer_token` 和 `refresh_token` 至少提供一个
- 若只给 `refresh_token`，服务会尝试刷新出 bearer

### `POST /admin/api/accounts/{id}/enable`

启用账号。

### `POST /admin/api/accounts/{id}/disable`

禁用账号。

### `DELETE /admin/api/accounts/{id}`

删除账号：

- `managed` 账号会被真正移除
- 外部来源账号会被标记禁用

### `POST /admin/api/accounts/{id}/refresh`

手动刷新指定账号 bearer/token 过期状态。

### `POST /admin/api/accounts/{id}/weight`

请求体：

```json
{
  "weight": 250
}
```

### `POST /admin/api/models/refresh`

立即触发一次模型目录刷新。
