# Token 看板 - 使用指南

## 📊 功能概述

Token 看板是一个实时监控 API 使用情况和 Token 消耗的 Web 应用。它提供了以下功能：

### 1. 📈 仪表板
- **Token 使用统计**：显示总 Token 数、已使用、剩余等信息
- **使用趋势图表**：展示 Token 消耗和 API 调用的趋势
- **模型使用统计**：按模型统计调用次数、消耗 Token、平均耗时等

### 2. 📝 实时日志
- **API 调用日志**：记录所有 API 调用的详细信息
- **日志搜索**：支持按端点、方法、状态码搜索
- **日志过滤**：实时过滤和查看日志

### 3. 🔐 API 密钥管理
- **创建密钥**：生成新的 API 密钥
- **查看密钥**：查看已创建的密钥信息
- **删除密钥**：删除不需要的密钥
- **复制密钥**：快速复制密钥到剪贴板

---

## 🚀 快速开始

### 1. 访问看板

启动服务后，访问以下地址：

```
http://localhost:8317/console
```

### 2. 查看统计信息

在仪表板页面可以看到：
- 总 Token 数和使用情况
- Token 使用趋势图表
- 各模型的使用统计

### 3. 查看日志

切换到"日志"标签页，可以：
- 查看所有 API 调用日志
- 搜索特定的日志
- 查看请求方法、状态码、消耗 Token 等信息

### 4. 管理密钥

切换到"密钥管理"标签页，可以：
- 查看已创建的 API 密钥
- 创建新的密钥
- 删除不需要的密钥
- 复制密钥到剪贴板

---

## 📡 API 接口

### 获取统计信息

```bash
GET /api/console/stats
```

**响应示例：**
```json
{
  "code": 0,
  "data": {
    "total_tokens": 100000,
    "used_tokens": 65432,
    "remaining_tokens": 34568,
    "usage_percent": 65.43,
    "api_call_count": 1234,
    "models": [
      {
        "name": "claude-sonnet-4-6",
        "call_count": 456,
        "token_count": 23456,
        "avg_time": 1200,
        "success_rate": 99.8
      }
    ]
  }
}
```

### 获取使用趋势

```bash
GET /api/console/usage-trend?days=7
```

**参数：**
- `days` (可选)：天数，默认 7，最多 90

**响应示例：**
```json
{
  "code": 0,
  "data": {
    "labels": ["02-14", "02-15", "02-16", "02-17", "02-18", "02-19", "02-20"],
    "tokenData": [8000, 9500, 7200, 10500, 12300, 11800, 9700],
    "callData": [120, 150, 100, 180, 200, 190, 160]
  }
}
```

### 获取日志

```bash
GET /api/console/logs?limit=100
```

**参数：**
- `limit` (可选)：限制数量，默认 100，最多 1000

**响应示例：**
```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "timestamp": "2026-02-20T18:45:32Z",
      "method": "POST",
      "endpoint": "/v1/chat/completions",
      "status": 200,
      "tokens": 1234,
      "duration": 1200,
      "model": "claude-sonnet-4-6"
    }
  ]
}
```

### 获取 API 密钥

```bash
GET /api/console/keys
```

**响应示例：**
```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "name": "生产环境密钥",
      "value": "sk-prod-...",
      "created_at": "2026-01-15T00:00:00Z",
      "last_used": "2026-02-20T18:45:32Z",
      "enabled": true
    }
  ]
}
```

### 创建 API 密钥

```bash
POST /api/console/keys
Content-Type: application/json

{
  "name": "新密钥",
  "description": "用于测试环境"
}
```

**响应示例：**
```json
{
  "code": 0,
  "data": {
    "id": 3,
    "name": "新密钥",
    "value": "sk-prod-1708420800-3",
    "created_at": "2026-02-20T18:50:00Z",
    "last_used": "0001-01-01T00:00:00Z",
    "enabled": true
  }
}
```

### 删除 API 密钥

```bash
DELETE /api/console/keys/1
```

**响应示例：**
```json
{
  "code": 0,
  "msg": "密钥已删除"
}
```

### 导出统计数据

```bash
GET /api/console/export
```

**响应：** 返回 JSON 文件，包含所有统计数据、日志和密钥信息

---

## 🔧 配置

### 环境变量

```bash
# Token 看板端口
CONSOLE_PORT=8317

# Token 看板路径
CONSOLE_PATH=/console

# 最大日志数
CONSOLE_MAX_LOGS=1000
```

### 配置文件

在 `config.yaml` 中添加：

```yaml
console:
  enabled: true
  port: 8317
  path: /console
  max_logs: 1000
  max_keys: 100
```

---

## 📊 数据说明

### Token 使用统计

| 字段 | 说明 | 单位 |
|------|------|------|
| total_tokens | 总 Token 数 | 个 |
| used_tokens | 已使用 Token 数 | 个 |
| remaining_tokens | 剩余 Token 数 | 个 |
| usage_percent | 使用百分比 | % |
| api_call_count | API 调用总次数 | 次 |

### 模型统计

| 字段 | 说明 | 单位 |
|------|------|------|
| name | 模型名称 | - |
| call_count | 调用次数 | 次 |
| token_count | 消耗 Token 数 | 个 |
| avg_time | 平均耗时 | ms |
| success_rate | 成功率 | % |

### API 日志

| 字段 | 说明 | 单位 |
|------|------|------|
| id | 日志 ID | - |
| timestamp | 时间戳 | - |
| method | HTTP 方法 | - |
| endpoint | 请求端点 | - |
| status | HTTP 状态码 | - |
| tokens | 消耗 Token 数 | 个 |
| duration | 请求耗时 | ms |
| model | 使用的模型 | - |

---

## 🎨 界面说明

### 仪表板

**统计卡片：**
- 🔵 总 Token 数（蓝色）
- 🟢 已使用 Token（绿色）
- 🟡 剩余 Token（黄色）
- 🔴 API 调用次数（红色）

**使用趋势图表：**
- 蓝色线：Token 消耗趋势
- 紫色线：API 调用次数趋势

**模型统计表：**
- 显示各模型的调用次数、消耗 Token、平均耗时、成功率

### 日志页面

**日志项：**
- 时间戳：请求时间
- 方法：HTTP 方法（GET、POST、PUT、DELETE）
- 端点：请求的 API 端点
- 状态码：HTTP 响应状态码
- Token：消耗的 Token 数
- 耗时：请求耗时（毫秒）

**搜索功能：**
- 支持按端点、方法、状态码搜索
- 实时过滤日志

### 密钥管理页面

**密钥项：**
- 密钥名称
- 创建时间
- 最后使用时间
- 密钥值（隐藏部分）

**操作按钮：**
- 复制：复制密钥到剪贴板
- 删除：删除密钥

**新增密钥：**
- 输入密钥名称
- 输入描述（可选）
- 点击"创建"按钮

---

## 💡 最佳实践

### 1. 定期检查使用情况
- 每天查看 Token 使用统计
- 监控使用趋势，及时调整
- 关注异常的 API 调用

### 2. 密钥管理
- 为不同环境创建不同的密钥
- 定期轮换密钥
- 删除不使用的密钥
- 记录密钥的用途

### 3. 日志分析
- 定期查看 API 日志
- 分析错误日志，找出问题
- 监控 API 响应时间
- 跟踪 Token 消耗情况

### 4. 性能优化
- 分析哪些模型消耗 Token 最多
- 优化请求参数，减少 Token 消耗
- 使用缓存减少 API 调用
- 批量处理请求

---

## 🐛 故障排除

### 问题 1：看板无法访问

**症状：** 访问 `http://localhost:8317/console` 返回 404

**解决方案：**
1. 检查服务是否正常运行
2. 检查端口是否正确
3. 检查文件 `web/token-console/public/index.html` 是否存在

### 问题 2：API 接口返回错误

**症状：** API 接口返回 500 错误

**解决方案：**
1. 查看服务日志
2. 检查请求参数是否正确
3. 检查数据库连接是否正常

### 问题 3：日志数据不更新

**症状：** 日志页面显示的数据不更新

**解决方案：**
1. 点击"刷新"按钮手动刷新
2. 检查 API 调用是否正常记录
3. 检查浏览器缓存

### 问题 4：密钥创建失败

**症状：** 创建密钥时返回错误

**解决方案：**
1. 检查密钥名称是否为空
2. 检查是否达到最大密钥数限制
3. 查看服务日志了解详细错误

---

## 📞 获取帮助

### 查看日志

```bash
# 查看最近的日志
tail -f logs/error.log

# 搜索 Token 看板相关的日志
grep -i console logs/error.log
```

### 导出数据

```bash
# 导出所有统计数据
curl http://localhost:8317/api/console/export > token-stats.json
```

### 测试 API

```bash
# 获取统计信息
curl http://localhost:8317/api/console/stats | jq '.'

# 获取日志
curl http://localhost:8317/api/console/logs | jq '.'

# 获取密钥
curl http://localhost:8317/api/console/keys | jq '.'
```

---

## 📝 更新日志

### v1.0.0 (2026-02-20)
- ✅ 初始版本发布
- ✅ 实现仪表板功能
- ✅ 实现日志查看功能
- ✅ 实现密钥管理功能
- ✅ 实现 API 接口
- ✅ 实现使用趋势图表

---

## 📄 许可证

MIT License

---

**需要帮助？** 查看完整文档或联系技术支持。
