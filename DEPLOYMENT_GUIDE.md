# CLIProxyAPI - 一键部署指南

## 🚀 快速开始（推荐）

### 第 1 步：一键部署

```bash
# 进入项目目录
cd /Users/jqwang/05-api-代理/CLIProxyAPI

# 一键部署（自动安装依赖、初始化配置、编译代码、生成密钥）
make setup
```

### 第 2 步：启动服务

```bash
# 启动服务
make run

# 或者后台启动
make start
```

### 第 3 步：访问看板

打开浏览器访问：
```
http://localhost:8317/console
```

### 第 4 步：申请 API 密钥

```bash
# 生成新的 API 密钥
make generate-key
```

---

## 📋 Makefile 命令详解

### 部署和启动

```bash
# 一键部署（推荐）
make setup

# 安装依赖
make install-deps

# 初始化配置文件
make init-config

# 编译代码
make build

# 启动服务（前台）
make run

# 启动服务（后台）
make start

# 停止服务
make stop
```

### 管理和监控

```bash
# 查看服务状态
make status

# 查看日志
make logs

# 查看配置信息
make show-config

# 查看支持的模型
make show-models

# 测试 API
make test-api
```

### 开发和测试

```bash
# 运行单元测试
make test

# 清理编译文件
make clean

# 完全清理（包括日志和数据）
make clean-all
```

### 获取帮助

```bash
# 显示所有可用命令
make help
```

---

## 🔑 API 密钥管理

### 生成密钥

```bash
# 交互式生成密钥
make generate-key

# 或者通过 API 生成
curl -X POST http://localhost:8317/api/keys/generate \
  -H "Content-Type: application/json" \
  -d '{
    "name": "生产环境密钥",
    "expires_in": 0
  }' | jq '.'
```

### 查看密钥列表

```bash
curl http://localhost:8317/api/keys | jq '.'
```

### 删除密钥

```bash
curl -X DELETE http://localhost:8317/api/keys/{key_id}
```

### 禁用/启用密钥

```bash
# 禁用密钥
curl -X POST http://localhost:8317/api/keys/{key_id}/disable

# 启用密钥
curl -X POST http://localhost:8317/api/keys/{key_id}/enable

# 撤销密钥
curl -X POST http://localhost:8317/api/keys/{key_id}/revoke
```

---

## 📊 支持的模型

### Claude 模型

```bash
# 查看所有 Claude 模型
make show-models

# 或通过 API 查看
curl http://localhost:8317/v1/models/provider/claude | jq '.'
```

**可用模型：**

| 模型 | 描述 | 上下文 | 成本 |
|------|------|--------|------|
| claude-opus-4-6 | 最强大的 Claude 模型 | 200K | $0.015/$0.075 per 1K |
| claude-sonnet-4-6 | 平衡性能和成本 | 200K | $0.003/$0.015 per 1K |
| claude-haiku-4-5-20251001 | 快速且经济 | 200K | $0.0008/$0.004 per 1K |

### Gemini 模型

```bash
# 查看所有 Gemini 模型
curl http://localhost:8317/v1/models/provider/gemini | jq '.'
```

**可用模型：**

| 模型 | 描述 | 上下文 | 成本 |
|------|------|--------|------|
| gemini-3.1-pro-high | 高精度的 Gemini 模型 | 1M | $0.0075/$0.03 per 1K |
| gemini-3.1-pro | 标准的 Gemini 模型 | 1M | $0.0075/$0.03 per 1K |
| gemini-3.1-flash | 快速的 Gemini 模型 | 1M | $0.0075/$0.03 per 1K |

---

## 🔌 API 使用示例

### 获取所有模型

```bash
curl http://localhost:8317/v1/models | jq '.'
```

### 获取单个模型详情

```bash
curl http://localhost:8317/v1/models/claude-sonnet-4-6 | jq '.'
```

### 使用 API 密钥调用 API

```bash
# 获取 Token 统计
curl -H "Authorization: Bearer {your-api-key}" \
  http://localhost:8317/api/console/stats | jq '.'

# 获取 API 日志
curl -H "Authorization: Bearer {your-api-key}" \
  http://localhost:8317/api/console/logs | jq '.'

# 调用 Claude 模型
curl -X POST http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer {your-api-key}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {"role": "user", "content": "Hello, how are you?"}
    ],
    "max_tokens": 100
  }' | jq '.'
```

---

## 📁 配置文件说明

### config.yaml

部署后会自动生成 `config.yaml` 文件，包含以下配置：

```yaml
# 服务器配置
server:
  port: 8317
  host: 0.0.0.0
  mode: debug

# Token 看板配置
console:
  enabled: true
  max_logs: 1000
  max_keys: 100

# Claude API 配置
claude:
  enabled: true
  api_key: "${CLAUDE_API_KEY}"
  enable_thinking: true

# Antigravity API 配置
antigravity:
  enabled: true
  api_key: "${ANTIGRAVITY_API_KEY}"

# 模型配置
models:
  claude:
    - name: claude-opus-4-6
    - name: claude-sonnet-4-6
    - name: claude-haiku-4-5-20251001
  gemini:
    - name: gemini-3.1-pro-high
    - name: gemini-3.1-pro
    - name: gemini-3.1-flash
```

---

## 🔐 环境变量配置

### 必需的环境变量

```bash
# Claude API 密钥
export CLAUDE_API_KEY="sk-ant-..."

# Antigravity API 密钥
export ANTIGRAVITY_API_KEY="..."
```

### 可选的环境变量

```bash
# 服务端口
export PORT=8317

# 日志级别
export LOG_LEVEL=info

# 最大日志数
export CONSOLE_MAX_LOGS=1000
```

---

## 📊 Token 看板功能

### 仪表板

访问 `http://localhost:8317/console` 查看：

- **Token 使用统计**：总数、已用、剩余、使用百分比
- **使用趋势图表**：7-90 天的 Token 消耗和 API 调用趋势
- **模型统计**：各模型的调用次数、消耗 Token、平均耗时、成功率

### 日志页面

- **API 调用日志**：记录所有 API 请求的详细信息
- **搜索过滤**：按端点、方法、状态码搜索
- **详细信息**：时间戳、HTTP 方法、状态码、消耗 Token、耗时

### 密钥管理

- **创建密钥**：生成新的 API 密钥
- **查看密钥**：显示密钥信息（隐藏完整值）
- **删除密钥**：删除不需要的密钥
- **复制密钥**：快速复制密钥到剪贴板

---

## 🐛 故障排除

### 问题 1：make setup 失败

**症状**：运行 `make setup` 时出错

**解决方案**：
```bash
# 检查 Go 是否安装
go version

# 手动安装依赖
go mod download
go mod tidy

# 重新运行 setup
make setup
```

### 问题 2：服务无法启动

**症状**：运行 `make run` 时出错

**解决方案**：
```bash
# 检查端口是否被占用
lsof -i :8317

# 查看详细错误
make run

# 查看日志
make logs
```

### 问题 3：无法访问看板

**症状**：访问 `http://localhost:8317/console` 返回 404

**解决方案**：
```bash
# 检查服务是否运行
make status

# 检查配置文件
cat config.yaml

# 重新启动服务
make stop
make run
```

### 问题 4：API 密钥生成失败

**症状**：运行 `make generate-key` 时出错

**解决方案**：
```bash
# 检查服务是否运行
curl http://localhost:8317/api/console/stats

# 通过 API 生成密钥
curl -X POST http://localhost:8317/api/keys/generate \
  -H "Content-Type: application/json" \
  -d '{"name":"test-key"}'
```

---

## 📈 性能优化

### 日志管理

```bash
# 查看日志大小
du -sh logs/

# 清理旧日志
rm logs/error.log.*

# 查看最近的日志
tail -f logs/error.log
```

### 内存管理

```bash
# 监控内存使用
top -p $(pgrep -f cli-proxy-api)

# 查看进程信息
ps aux | grep cli-proxy-api
```

---

## 🔄 更新和维护

### 更新代码

```bash
# 拉取最新代码
git pull origin main

# 重新编译
make build

# 重启服务
make stop
make run
```

### 备份配置

```bash
# 备份配置文件
cp config.yaml config.yaml.backup

# 备份日志
cp -r logs/ logs.backup/
```

### 恢复配置

```bash
# 恢复配置文件
cp config.yaml.backup config.yaml

# 重启服务
make stop
make run
```

---

## 📞 获取帮助

### 查看命令帮助

```bash
# 显示所有可用命令
make help

# 显示模型列表
make show-models

# 显示配置信息
make show-config

# 显示服务状态
make status
```

### 查看日志

```bash
# 实时查看日志
make logs

# 搜索特定错误
grep -i error logs/error.log

# 查看最近 100 行日志
tail -100 logs/error.log
```

### 测试 API

```bash
# 测试所有 API
make test-api

# 测试单个端点
curl http://localhost:8317/api/console/stats | jq '.'
```

---

## ✅ 部署检查清单

- [ ] 运行 `make setup` 完成部署
- [ ] 运行 `make run` 启动服务
- [ ] 访问 `http://localhost:8317/console` 查看看板
- [ ] 运行 `make generate-key` 生成 API 密钥
- [ ] 运行 `make show-models` 查看支持的模型
- [ ] 运行 `make test-api` 测试 API
- [ ] 查看 `make logs` 确认没有错误
- [ ] 运行 `make test` 执行单元测试

---

## 🎉 完成！

现在你已经成功部署了 CLIProxyAPI！

**下一步：**
1. 访问 Token 看板：`http://localhost:8317/console`
2. 生成 API 密钥：`make generate-key`
3. 开始使用 API：查看 API 使用示例
4. 监控使用情况：在看板中查看统计信息

**需要帮助？**
- 查看命令帮助：`make help`
- 查看日志：`make logs`
- 测试 API：`make test-api`

---

**祝你使用愉快！** 🚀
