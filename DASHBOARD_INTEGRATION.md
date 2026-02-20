# 外部仪表板集成指南

## 概述
CLIProxyAPI 现已集成外部仪表板，提供完整的管理API兼容性。外部仪表板可以通过 `/v0/management/` API 端点访问所有功能。

## 访问仪表板

### 方式 1：Token 看板（我们的实现）
```
http://localhost:8317/console
```

### 方式 2：外部仪表板（兼容模式）
```
http://localhost:8317/dashboard
```

## 管理 API 端点

### 统计和日志
- `GET /v0/management/usage` - 获取使用统计
- `GET /v0/management/activity` - 获取活动日志
- `GET /v0/management/stats/trends` - 获取使用趋势
- `GET /v0/management/events` - 获取事件
- `GET /v0/management/logs` - 获取日志
- `DELETE /v0/management/logs` - 清除日志

### API 密钥管理
- `GET /v0/management/api-keys` - 获取API密钥列表
- `PUT /v0/management/api-keys` - 更新API密钥
- `DELETE /v0/management/api-keys` - 删除API密钥

### 认证和配置
- `GET /v0/management/get-auth-status` - 获取认证状态
- `GET /v0/management/config` - 获取配置
- `GET /v0/management/config/yaml` - 获取YAML配置
- `PUT /v0/management/config/yaml` - 更新YAML配置

### 设置
- `GET /v0/management/debug` - 获取调试设置
- `PUT /v0/management/debug` - 更新调试设置
- `GET /v0/management/usage-statistics-enabled` - 获取使用统计启用状态
- `PUT /v0/management/usage-statistics-enabled` - 更新使用统计启用状态
- `GET /v0/management/request-log` - 获取请求日志设置
- `PUT /v0/management/request-log` - 更新请求日志设置

## 测试 API

### 获取使用统计
```bash
curl -s http://localhost:8317/v0/management/usage | jq '.'
```

### 获取API密钥
```bash
curl -s http://localhost:8317/v0/management/api-keys | jq '.'
```

### 获取日志
```bash
curl -s http://localhost:8317/v0/management/logs | jq '.'
```

### 获取认证状态
```bash
curl -s http://localhost:8317/v0/management/get-auth-status | jq '.'
```

## 架构

### ManagementAPIAdapter
位置：`internal/console/management_adapter.go`

ManagementAPIAdapter 是一个适配层，将外部仪表板的 `/v0/management/` API 请求转换为我们的内部 ConsoleManager API。

### 主要功能
1. **使用统计** - 从 ConsoleManager 获取Token使用统计
2. **日志管理** - 获取和清除API调用日志
3. **密钥管理** - 管理API密钥
4. **配置管理** - 获取和更新配置
5. **设置管理** - 管理各种系统设置

## 集成流程

1. **请求到达** - 外部仪表板发送请求到 `/v0/management/*`
2. **路由匹配** - Gin 路由器匹配到相应的处理程序
3. **适配转换** - ManagementAPIAdapter 将请求转换为内部格式
4. **数据获取** - ConsoleManager 获取相应的数据
5. **响应返回** - 适配器将数据转换为外部仪表板期望的格式

## 支持的模型

### Claude 模型
- claude-opus-4-6
- claude-sonnet-4-6
- claude-haiku-4-5-20251001

### Gemini 模型
- gemini-3.1-pro-high
- gemini-3.1-pro
- gemini-3.1-flash

## 快速开始

### 1. 启动服务
```bash
make run
```

### 2. 访问仪表板
```
http://localhost:8317/console
```

### 3. 测试API
```bash
curl -s http://localhost:8317/v0/management/usage | jq '.'
```

## 故障排除

### 仪表板返回404
确保服务器已启动，并且静态文件路径正确。

### API返回空数据
这是正常的，因为系统刚启动。进行一些API调用后，数据将被记录。

### 认证失败
某些端点可能需要认证。检查 `MANAGEMENT_PASSWORD` 环境变量。

## 下一步

1. 自定义仪表板样式
2. 添加更多管理功能
3. 实现实时数据更新
4. 添加用户权限管理
