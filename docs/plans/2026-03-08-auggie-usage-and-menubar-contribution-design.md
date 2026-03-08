# Auggie Usage And Menubar Contribution Design

## Goal

修复 Auggie 请求在管理统计里显示 `1 request / 0 tokens` 的问题，并把 menubar“贡献”页升级为按 `provider -> account -> key -> model` 展示，明确区分 `antigravity` 和 `auggie`。

## Confirmed Constraints

- 只改当前在用的 Swift menubar 和 Go 后端。
- provider 必须分开，不能把 `antigravity-*` 和 `auggie-*` 混在一起。
- 账号池概念暂缓，不在这次范围内。
- 现有 “服务” 页和 “Keys” 页结构保留；这次只补“贡献”页和 usage 统计。

## Root Cause

真实 Auggie `/chat-stream` 上游流里，token 使用量不在顶层 `usage` 或 `prompt_tokens` / `completion_tokens` 字段，而是在终态 chunk 的 `nodes[].token_usage` 中，例如：

- `nodes[].token_usage.input_tokens`
- `nodes[].token_usage.output_tokens`
- `nodes[].token_usage.cache_read_input_tokens`
- `nodes[].token_usage.cache_creation_input_tokens`

当前 `parseAuggieUsage(...)` 只看顶层 usage 字段，因此流结束后只能走 `ensurePublished()`，导致请求数被记下，但 token 全为 0。

## Approaches

### A. 只在 menubar 侧修饰显示

- 用 UI 规则掩盖 `0 词`，只显示请求数。
- 优点：改动最小。
- 缺点：数据仍然错误，管理接口与 GUI 都不可信。

### B. 只修后端 token，不改贡献页结构

- 补齐 Auggie token 统计，UI 继续按 key 平铺。
- 优点：先修数据正确性。
- 缺点：用户仍然看不出 `antigravity` / `auggie` / 账号归属。

### C. 后端修 token，前端按 provider/account/key/model 分组

- 后端从 Auggie 原始终态 chunk 提取 `nodes[].token_usage`。
- menubar 用现有 `client-api-keys` 和 `auth-files` 把 usage 归并到 provider / account。
- 优点：同时解决数据准确性和结构可读性。
- 缺点：需要同时改 Go 和 Swift。

## Recommendation

采用 C。

原因：

- `0 词` 是数据层错误，必须在后端修。
- “贡献”页混在一起是结构问题，必须在前端重组。
- 两部分写集分离，风险可控。

## Design

### Backend

- 扩展 `parseAuggieUsage(...)`，支持从 `nodes[].token_usage` 读取 usage。
- 对同一 payload 内多个 `nodes[].token_usage`，按字段取最大值，避免重复累计同一终态总量。
- 保持现有 `reporter.publish(...)` / `ensurePublished(...)` 机制不变；只修 usage 提取。

### Menubar Data Flow

- 继续从 `/v0/management/usage` 读取 key usage。
- 扩展 usage 解码模型，读取每个 model 的 `total_requests` 和 `total_tokens`。
- 在 `UsageMonitorViewModel` 中，把 usage key 与 `managedClientKeys`、`authTargets` 关联，生成：
  - provider 组
  - account 组
  - key 组
  - model 行

### Menubar UI

- “贡献”页显示 provider 分组胶囊。
- 每个 account 卡片显示账号名、次级标识、请求数、词数。
- 每个 key 行显示 masked key、请求数、词数。
- 每个 model 行显示模型 ID、请求数、词数。
- 未绑定或无法匹配账号的 usage，归到“未绑定” provider/account。

## Testing

### Backend

- 为 `parseAuggieUsage(...)` 增加针对 `nodes[].token_usage` 的单元测试。
- 为 Auggie usage 统计增加回归测试，验证真实执行路径能记录 token，而不仅是 request。

### Menubar

- 为 API client 增加 usage 解码测试，确保 model token/request 被正确读取。
- 为 view model 增加 contribution grouping 测试，验证 provider/account/key/model 归并正确。
- 为 layout 增加 contribution 分组高度测试，确保内容少时自动增高、过多时滚动。
