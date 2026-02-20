# Token 看板 - 集成指南

## 📋 项目概述

Token 看板是一个完整的 Web 应用，用于实时监控 API 使用情况和 Token 消耗。它包括：

- **后端 API** (Go)：处理统计、日志、密钥管理
- **前端界面** (Vue 3)：提供用户友好的 Web 界面
- **数据管理**：内存存储，支持并发访问

---

## 🏗️ 架构设计

```
┌─────────────────────────────────────────────────────────┐
│                    用户浏览器                            │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│              Token 看板前端 (Vue 3)                      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │   仪表板     │  │   日志页面   │  │  密钥管理    │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└────────────────────┬────────────────────────────────────┘
                     │ HTTP/REST API
                     ▼
┌─────────────────────────────────────────────────────────┐
│              Token 看板后端 (Go)                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │           ConsoleManager                         │  │
│  │  ┌──────────────┐  ┌──────────────┐             │  │
│  │  │  统计信息    │  │  日志管理    │             │  │
│  │  └──────────────┘  └──────────────┘             │  │
│  │  ┌──────────────┐  ┌──────────────┐             │  │
│  │  │  密钥管理    │  │  趋势数据    │             │  │
│  │  └──────────────┘  └──────────────┘             │  │
│  └──────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────┐  │
│  │           HTTP Handler                          │  │
│  │  /api/console/stats                             │  │
│  │  /api/console/logs                              │  │
│  │  /api/console/keys                              │  │
│  │  /api/console/usage-trend                       │  │
│  │  /api/console/export                            │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

---

## 🔧 集成步骤

### 第 1 步：注册路由

在主服务器初始化代码中添加：

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/router-for-me/CLIProxyAPI/v6/internal/console"
)

func main() {
    router := gin.Default()

    // 创建 Token 看板管理器
    consoleManager := console.NewConsoleManager()

    // 创建处理程序
    consoleHandler := console.NewHandler(consoleManager)

    // 注册路由
    consoleHandler.RegisterRoutes(router)

    // 启动服务
    router.Run(":8317")
}
```

### 第 2 步：记录 API 调用

在 API 处理程序中记录调用：

```go
// 在处理 API 请求时
func handleChatCompletion(c *gin.Context) {
    // ... 处理请求 ...

    // 记录 API 调用
    consoleManager.RecordLog(
        c.Request.Method,           // 方法
        c.Request.URL.Path,         // 端点
        modelName,                  // 模型名称
        c.Writer.Status(),          // 状态码
        tokensUsed,                 // 消耗的 Token
        duration,                   // 耗时（毫秒）
    )
}
```

### 第 3 步：访问看板

启动服务后，访问：

```
http://localhost:8317/console
```

---

## 📦 文件结构

```
CLIProxyAPI/
├── internal/
│   └── console/
│       ├── manager.go           # 核心管理器
│       ├── handler.go           # HTTP 处理程序
│       └── manager_test.go      # 单元测试
└── web/
    └── token-console/
        ├── public/
        │   └── index.html       # 前端界面
        └── README.md            # 使用指南
```

---

## 🚀 部署指南

### 开发环境

```bash
# 1. 编译代码
go build -o cli-proxy-api ./cmd/server/main.go

# 2. 运行服务
./cli-proxy-api -config config.yaml

# 3. 访问看板
# 打开浏览器访问 http://localhost:8317/console
```

### 生产环境

#### 使用 Docker

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .

RUN go build -o cli-proxy-api ./cmd/server/main.go

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/cli-proxy-api .
COPY --from=builder /app/web ./web
COPY --from=builder /app/config.yaml .

EXPOSE 8317

CMD ["./cli-proxy-api", "-config", "config.yaml"]
```

构建和运行：

```bash
# 构建镜像
docker build -t cli-proxy-api:latest .

# 运行容器
docker run -p 8317:8317 cli-proxy-api:latest
```

#### 使用 Systemd

创建 `/etc/systemd/system/cli-proxy-api.service`：

```ini
[Unit]
Description=CLIProxyAPI Token Console
After=network.target

[Service]
Type=simple
User=api
WorkingDirectory=/opt/cli-proxy-api
ExecStart=/opt/cli-proxy-api/cli-proxy-api -config /opt/cli-proxy-api/config.yaml
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable cli-proxy-api
sudo systemctl start cli-proxy-api
```

---

## 🔌 API 集成示例

### 示例 1：在 Claude 处理程序中集成

```go
package handlers

import (
    "github.com/router-for-me/CLIProxyAPI/v6/internal/console"
)

type ClaudeHandler struct {
    consoleManager *console.ConsoleManager
}

func (h *ClaudeHandler) HandleChatCompletion(c *gin.Context) {
    startTime := time.Now()

    // ... 处理请求 ...

    // 记录 API 调用
    duration := time.Since(startTime).Milliseconds()
    h.consoleManager.RecordLog(
        "POST",
        "/v1/chat/completions",
        modelName,
        http.StatusOK,
        tokensUsed,
        duration,
    )
}
```

### 示例 2：在中间件中集成

```go
func ConsoleMiddleware(consoleManager *console.ConsoleManager) gin.HandlerFunc {
    return func(c *gin.Context) {
        startTime := time.Now()

        c.Next()

        // 记录 API 调用
        duration := time.Since(startTime).Milliseconds()
        consoleManager.RecordLog(
            c.Request.Method,
            c.Request.URL.Path,
            c.GetString("model"),
            c.Writer.Status(),
            c.GetInt64("tokens"),
            duration,
        )
    }
}

// 使用中间件
router.Use(ConsoleMiddleware(consoleManager))
```

---

## 📊 数据流

### Token 使用流程

```
1. 用户发送 API 请求
   ↓
2. 服务器处理请求
   ↓
3. 调用 consoleManager.RecordLog()
   ↓
4. 更新统计信息
   ↓
5. 存储日志记录
   ↓
6. 前端实时显示更新
```

### 日志记录流程

```
API 请求
  ↓
记录日志信息
  ↓
更新 API 调用计数
  ↓
更新 Token 消耗
  ↓
更新模型统计
  ↓
保持日志数量在限制内
```

---

## 🔒 安全考虑

### 1. API 密钥管理

- 密钥值在前端显示时被隐藏
- 支持创建和删除密钥
- 记录密钥的最后使用时间

### 2. 访问控制

建议添加身份验证中间件：

```go
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        if token == "" {
            c.JSON(http.StatusUnauthorized, gin.H{
                "code": 401,
                "msg":  "Unauthorized",
            })
            c.Abort()
            return
        }
        c.Next()
    }
}

// 使用
api := router.Group("/api/console")
api.Use(AuthMiddleware())
```

### 3. 数据隐私

- 不存储敏感信息
- 定期清理旧日志
- 支持数据导出和备份

---

## 📈 性能优化

### 1. 日志限制

```go
// 设置最大日志数
consoleManager.maxLogs = 10000
```

### 2. 并发访问

ConsoleManager 使用 `sync.RWMutex` 支持并发访问：

```go
// 读操作（多个 goroutine 可以并发执行）
stats := consoleManager.GetStats()
logs := consoleManager.GetLogs(100)

// 写操作（互斥执行）
consoleManager.RecordLog(...)
consoleManager.CreateAPIKey(...)
```

### 3. 缓存策略

```go
// 缓存统计信息
var cachedStats *TokenStats
var cacheTime time.Time

func GetStatsCached() *TokenStats {
    if time.Since(cacheTime) < 5*time.Second {
        return cachedStats
    }
    cachedStats = consoleManager.GetStats()
    cacheTime = time.Now()
    return cachedStats
}
```

---

## 🧪 测试

### 运行单元测试

```bash
go test ./internal/console/... -v
```

### 运行基准测试

```bash
go test ./internal/console/... -bench=. -benchmem
```

### 测试覆盖率

```bash
go test ./internal/console/... -cover
```

---

## 📝 配置示例

### config.yaml

```yaml
# Token 看板配置
console:
  enabled: true
  port: 8317
  path: /console
  max_logs: 1000
  max_keys: 100

  # 日志保留时间（天）
  log_retention_days: 30

  # 自动清理间隔（小时）
  cleanup_interval: 24
```

### 环境变量

```bash
# Token 看板端口
export CONSOLE_PORT=8317

# Token 看板路径
export CONSOLE_PATH=/console

# 最大日志数
export CONSOLE_MAX_LOGS=1000

# 最大密钥数
export CONSOLE_MAX_KEYS=100
```

---

## 🐛 故障排除

### 问题 1：看板无法访问

```bash
# 检查服务是否运行
curl http://localhost:8317/console

# 查看服务日志
tail -f logs/error.log

# 检查端口是否被占用
lsof -i :8317
```

### 问题 2：API 返回 500 错误

```bash
# 查看详细错误日志
grep -i console logs/error.log

# 检查请求参数
curl -X GET http://localhost:8317/api/console/stats | jq '.'
```

### 问题 3：日志数据不更新

```bash
# 检查 RecordLog 是否被调用
grep -i "RecordLog" logs/debug.log

# 手动刷新看板
curl http://localhost:8317/api/console/logs
```

---

## 📚 相关文档

- [Token 看板使用指南](./README.md)
- [API 文档](./API.md)
- [部署指南](./DEPLOYMENT.md)

---

## 🎯 下一步

### 短期（1-2 周）
- [ ] 部署到测试环境
- [ ] 进行功能测试
- [ ] 收集用户反馈

### 中期（1 个月）
- [ ] 添加数据库支持（持久化存储）
- [ ] 实现数据导出功能
- [ ] 添加告警功能

### 长期（2-3 个月）
- [ ] 实现实时通知
- [ ] 添加高级分析功能
- [ ] 支持多用户和权限管理

---

## 📞 获取帮助

### 查看日志

```bash
# 查看最近的日志
tail -f logs/error.log

# 搜索特定错误
grep -i "error" logs/error.log
```

### 测试 API

```bash
# 获取统计信息
curl http://localhost:8317/api/console/stats | jq '.'

# 获取日志
curl http://localhost:8317/api/console/logs | jq '.'

# 创建密钥
curl -X POST http://localhost:8317/api/console/keys \
  -H "Content-Type: application/json" \
  -d '{"name":"test-key","description":"test"}'
```

---

**需要帮助？** 查看完整文档或联系技术支持。
