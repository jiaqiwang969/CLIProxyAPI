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
2. `~/.cliproxyapi/config.yaml`
3. 当前目录 `config.yaml`
4. 当前目录 `apps/server-go/config.yaml`
5. 当前目录上级 `../CLIProxyAPI-wjq/apps/server-go/config.yaml`
6. `~/05-api-代理/CLIProxyAPI-wjq/config.yaml`
7. `~/05-api-代理/CLIProxyAPI-wjq/apps/server-go/config.yaml`
8. `~/CLIProxyAPI-wjq/config.yaml`
9. `~/CLIProxyAPI-wjq/apps/server-go/config.yaml`

并自动读取：

- `port`
- `remote-management.secret-key`

管理密钥对齐说明：

- 如果 `remote-management.secret-key` 是明文，menubar 直接使用它。
- 如果它已经被后端启动时自动回写成 bcrypt 哈希，menubar 会对这两类本机运行时自动回退到默认明文 `cliproxy-menubar-dev`：
  - `~/.cliproxyapi/config.yaml`
  - 仓库开发路径 `.../apps/server-go/config.yaml`
- 如果你用的是其他自定义配置路径，而且密钥已经被哈希回写，请显式设置环境变量 `CLIPROXY_MANAGEMENT_KEY=<明文密钥>`。

如果 menubar 里点击“生成默认配置”，它会创建：

- `host: "127.0.0.1"`
- `port: 8317`
- `remote-management.secret-key: "cliproxy-menubar-dev"`

后端二进制会优先查找：

- 与 `config.yaml` 同目录下的 `cli-proxy-api`
- 与 `config.yaml` 同目录下的 `server`
- 仓库内 `apps/server-go/cli-proxy-api`
- 仓库内 `apps/server-go/server`

注意：

- menubar 的“启动服务/停止服务”是直接拉起或终止本地二进制，不是接管 `launchd`。
- 如果你已经用 nix-darwin/LaunchAgent 托管了 `~/.cliproxyapi/cli-proxy-api`，menubar 仍可读取管理接口，但启动/停止语义仍按“本地二进制”处理。

## 使用的接口

- `/v0/management/usage`
- `/v0/management/client-api-keys`
- `/v0/management/auth-files?include_models=true`

鉴权：

- Header: `Authorization: Bearer <MANAGEMENT_KEY>`

## 环境变量

- `CLIPROXY_CONFIG_PATH`: 强制指定 menubar 读取的 `config.yaml`
- `CLIPROXY_MANAGEMENT_KEY`: 覆盖配置文件里的管理密钥；用于自定义路径下的 bcrypt 回写场景
- `CLIPROXY_BASE_URL`: 直接指定管理 API 地址；设置后 menubar 不再自动解析 `config.yaml`
