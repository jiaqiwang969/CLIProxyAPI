# Menubar Service/Keys Separation Design

## Goal

把 menubar 中的 provider/account/status 服务态信息集中到“服务”页，把“Keys”页收敛为 Key 绑定与管理入口，减少同一信息在两个 tab 中重复出现。

## Scope

- 修改 Swift menubar 前端。
- 不改后端接口与配置结构。
- 保持现有 key 绑定模型不变。

## Design

### 服务页

- 保留本地服务启动、停止、日志区域。
- 新增 provider -> account 的服务状态分组。
- 每个账号卡片显示：
  - provider
  - 账号显示名
  - 次级标识（邮箱或账号）
  - 当前状态
  - 模型数
  - 已绑定 key 数

### Keys 页

- 保留新增 key、生成 key、启停 key、备注、复制、删除。
- 保留按 provider/account 分组展示 key，便于看绑定归属。
- 移除账号状态灯、状态文案、邮箱/账号等服务态信息。
- 每个账号分组只强调“这是哪个绑定目标”和“下面有哪些 key”。

## Testing

- 为 view model 增加服务分组测试，验证 provider/account/status/key-count 归类正确。
- 为布局增加服务账号区域高度测试，避免新增区域导致面板高度回退。
- 运行 menubar 的 Swift 测试集。
