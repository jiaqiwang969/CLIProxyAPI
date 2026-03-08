# CLIProxy Menu Bar Monitor (Swift)

极简菜单栏版本（macOS），按你的场景做三件事：

1. 启动/停止本地 `cli-proxy-api` 服务
2. 申请（生成）/管理 `sk-key`
3. 按 `sk-key -> 模型` 查看调用贡献

## 功能

- 菜单栏显示总请求数（关闭监控时显示 `OFF`）
- 服务页：显示本地服务状态，并支持启动/停止
- Keys 页：添加、生成、删除 `sk-key`（脱敏展示）
- 贡献页：按 `sk-key -> 模型` 展示调用次数与占比（优先 `antigravity/*`）
- 监控开关（开启/关闭）+ 手动刷新
- 自动读取 CLIProxyAPI 配置（不要求用户手动填写）

兼容说明：
- 如果 `usage` 数据里没有 `antigravity/` 前缀（旧版统计格式），会自动回退展示实际上游模型名，避免空列表。
- `sk-key` 在 UI 中会做脱敏显示（如 `sk-xxxx...yyyy`）。

## 运行

```bash
cd apps/menubar-swift
swift run CLIProxyMenuBar
```

## 自动配置来源

应用会按顺序自动查找 `config.yaml`：

1. `CLIPROXY_CONFIG_PATH`（环境变量）
2. 当前目录 `config.yaml`
3. 当前目录 `apps/server-go/config.yaml`
4. 当前目录上级 `../CLIProxyAPI-wjq/apps/server-go/config.yaml`
5. `~/05-api-代理/CLIProxyAPI-wjq/config.yaml`
6. `~/05-api-代理/CLIProxyAPI-wjq/apps/server-go/config.yaml`
7. `~/CLIProxyAPI-wjq/config.yaml`
8. `~/CLIProxyAPI-wjq/apps/server-go/config.yaml`
9. `~/.cliproxyapi/config.yaml`

并自动读取：

- `port`
- `remote-management.secret-key`

后端二进制会优先查找：

- 与 `config.yaml` 同目录下的 `cli-proxy-api`
- 与 `config.yaml` 同目录下的 `server`
- 仓库内 `apps/server-go/cli-proxy-api`
- 仓库内 `apps/server-go/server`

## 使用的接口

- `/v0/management/usage`

鉴权：

- Query: `?key=<MANAGEMENT_KEY>`
- Header: `Authorization: Bearer <MANAGEMENT_KEY>`
