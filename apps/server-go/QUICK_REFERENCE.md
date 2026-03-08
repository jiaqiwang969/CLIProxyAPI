# 🚀 CLIProxyAPI - 快速参考指南

## ⚡ 3 步快速开始

```bash
# 第 1 步：一键部署
make setup

# 第 2 步：启动服务
make run

# 第 3 步：访问看板
# 打开浏览器访问：http://localhost:8317/console
```

---

## 📋 常用命令速查

### 部署和启动
```bash
make setup              # 一键部署（推荐）
make run                # 启动服务（前台）
make start              # 启动服务（后台）
make stop               # 停止服务
```

### 管理和监控
```bash
make status             # 查看服务状态
make logs               # 查看日志
make show-models        # 查看支持的模型
make generate-key       # 生成 API 密钥
make test-api           # 测试 API
```

### 开发和测试
```bash
make test               # 运行单元测试
make build              # 编译代码
make clean              # 清理编译文件
```

---

## 🔑 API 密钥申请

### 方式 1：交互式生成（推荐）
```bash
make generate-key
```

### 方式 2：通过 API 生成
```bash
curl -X POST http://localhost:8317/api/keys/generate \
  -H "Content-Type: application/json" \
  -d '{"name":"my-key"}' | jq '.data.key'
```

---

## 📊 支持的模型

### Claude 模型
```bash
# 查看所有 Claude 模型
curl http://localhost:8317/v1/models/provider/claude | jq '.'

# 可用模型：
# - claude-opus-4-6 (最强大)
# - claude-sonnet-4-6 (推荐)
# - claude-haiku-4-5-20251001 (快速)
```

### Gemini 模型
```bash
# 查看所有 Gemini 模型
curl http://localhost:8317/v1/models/provider/gemini | jq '.'

# 可用模型：
# - gemini-3.1-pro-high (高精度)
# - gemini-3.1-pro (标准)
# - gemini-3.1-flash (快速)
```

---

## 🔌 API 使用示例

### 获取所有模型
```bash
curl http://localhost:8317/v1/models | jq '.'
```

### 获取 Token 统计
```bash
curl -H "Authorization: Bearer {your-api-key}" \
  http://localhost:8317/api/console/stats | jq '.'
```

### 获取 API 日志
```bash
curl -H "Authorization: Bearer {your-api-key}" \
  http://localhost:8317/api/console/logs | jq '.'
```

### 调用 Claude 模型
```bash
curl -X POST http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer {your-api-key}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 100
  }' | jq '.'
```

---

## 📁 重要文件

| 文件 | 说明 |
|------|------|
| `Makefile` | 一键部署工具 |
| `config.yaml.example` | 配置文件模板 |
| `DEPLOYMENT_GUIDE.md` | 详细部署指南 |
| `PROJECT_SUMMARY.md` | 项目完整总结 |
| `web/token-console/` | Token 看板前端 |
| `internal/console/` | Token 看板后端 |
| `internal/models/` | 模型管理系统 |

---

## 🐛 常见问题

### Q: make setup 失败怎么办？
```bash
# 检查 Go 是否安装
go version

# 手动安装依赖
go mod download
go mod tidy

# 重新运行
make setup
```

### Q: 如何查看服务是否运行？
```bash
make status
```

### Q: 如何查看日志？
```bash
make logs
```

### Q: 如何停止服务？
```bash
make stop
```

---

## 📞 获取帮助

```bash
make help               # 显示所有命令
make show-config        # 显示配置信息
make show-models        # 显示模型列表
make test-api           # 测试 API
```

---

## ✅ 部署检查清单

- [ ] 运行 `make setup`
- [ ] 运行 `make run`
- [ ] 访问 `http://localhost:8317/console`
- [ ] 运行 `make generate-key`
- [ ] 运行 `make show-models`
- [ ] 运行 `make test-api`

---

## 🎉 完成！

现在你已经可以：
- ✅ 一键部署 CLIProxyAPI
- ✅ 访问 Token 看板
- ✅ 申请 API 密钥
- ✅ 使用 Claude 和 Gemini 模型
- ✅ 监控 API 使用情况

**祝你使用愉快！** 🚀
