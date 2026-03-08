import AppKit
import SwiftUI

private enum DashboardTab: String, CaseIterable, Identifiable {
    case service = "服务"
    case keys = "Keys"
    case usage = "贡献"
    case settings = "设置"

    var id: String { rawValue }
}

struct MenuBarDashboardView: View {
    @ObservedObject var viewModel: UsageMonitorViewModel
    @State private var selectedTab: DashboardTab = .usage
    @State private var noteDrafts: [String: String] = [:]
    @AppStorage("launchAtLogin") private var launchAtLogin = false

    private var keyBindingGroups: [ProviderKeyGroup] {
        viewModel.providerKeyGroups.compactMap { group in
            let accounts = group.accounts.filter { !$0.keys.isEmpty }
            guard !accounts.isEmpty else {
                return nil
            }
            return ProviderKeyGroup(provider: group.provider, accounts: accounts)
        }
    }

    private var usageGroups: [UsageProviderGroup] {
        viewModel.usageProviderGroups
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("CLIProxy 控制台")
                    .font(.headline)
                Spacer()
                Text(viewModel.monitorEnabled ? "MONITOR ON" : "MONITOR OFF")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            Picker("", selection: $selectedTab) {
                ForEach(DashboardTab.allCases) { tab in
                    Text(tab.rawValue).tag(tab)
                }
            }
            .pickerStyle(.segmented)

            Group {
                switch selectedTab {
                case .service:
                    servicePanel
                case .keys:
                    keysPanel
                case .usage:
                    usagePanel
                case .settings:
                    settingsPanel
                }
            }

            if let actionMessage = viewModel.actionMessage, !actionMessage.isEmpty {
                Text(actionMessage)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            if let errorMessage = viewModel.errorMessage, !errorMessage.isEmpty {
                Text(errorMessage)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            Divider()

            HStack(spacing: 8) {
                Button("刷新") {
                    Task { await viewModel.refreshNow() }
                }
                .disabled(viewModel.isRefreshing)

                Button(viewModel.monitorEnabled ? "暂停统计" : "开启统计") {
                    viewModel.toggleMonitor()
                }

                Spacer()

                Button("退出") {
                    NSApp.terminate(nil)
                }
            }
        }
        .padding(12)
        .frame(width: MenuBarLayout.panelWidth)
        .fixedSize(horizontal: false, vertical: true)
    }

    private var settingsPanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            Toggle("开机时自动启动", isOn: $launchAtLogin)
                .font(.callout)
            
            Divider()
            
            VStack(alignment: .leading, spacing: 4) {
                Text("API 地址")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(RuntimeConfigLoader.load().baseURL)
                    .font(.caption)
                    .textSelection(.enabled)
            }
            
            VStack(alignment: .leading, spacing: 4) {
                Text("配置文件路径")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                HStack {
                    Text(RuntimeConfigLoader.load().configPath ?? "未找到")
                        .font(.caption)
                        .textSelection(.enabled)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if viewModel.hasConfigFile {
                        Button(action: {
                            viewModel.openConfigFile()
                        }) {
                            Image(systemName: "folder")
                        }
                        .buttonStyle(.borderless)
                        .help("在访达中打开配置所在文件夹")
                    }
                }
            }
            
            Spacer()
            
            Button(action: {
                viewModel.checkForUpdates()
            }) {
                HStack(spacing: 4) {
                    Image(systemName: "arrow.triangle.2.circlepath")
                    Text("检查更新")
                }
            }
            .buttonStyle(.link)
            .font(.caption)
        }
    }

    private var servicePanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("状态：\(viewModel.serviceStatusText)")
                .font(.callout)

            HStack(spacing: 8) {
                Toggle(
                    "开机自启",
                    isOn: Binding(
                        get: { viewModel.launchAtLoginEnabled },
                        set: { _ in viewModel.toggleLaunchAtLogin() }
                    )
                )
                .toggleStyle(.switch)
                .disabled(true) // Requires LaunchAtLoginManager support or directly tying to AppStorage which we have in Settings now

                Spacer()

                Text("目前需要在“设置”页中配置系统自启")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                
            }

            if !viewModel.hasConfigFile {
                Text("未找到本地 config.yaml，无法控制服务")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                
                Button("生成默认配置") {
                    viewModel.createDefaultConfig()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }

            HStack(spacing: 8) {
                Button("启动服务") {
                    viewModel.startLocalService()
                }
                .disabled(viewModel.serviceStatus.isRunning || !viewModel.hasConfigFile)

                Button("停止服务") {
                    viewModel.stopLocalService()
                }
                .disabled(!viewModel.serviceStatus.isRunning)

                if viewModel.isRefreshing {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            Divider()

            HStack(spacing: 8) {
                Text("账号状态")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Text("\(viewModel.serviceProviderGroups.count) 个 Provider / \(viewModel.authTargets.count) 个账号 / \(viewModel.apiKeys.count) 个 Key")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if viewModel.serviceProviderGroups.isEmpty {
                Text("暂无服务账号，请先登录 Auggie 或 Antigravity。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 6)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(viewModel.serviceProviderGroups) { providerGroup in
                            VStack(alignment: .leading, spacing: 8) {
                                HStack(spacing: 8) {
                                    Text(providerGroup.provider)
                                        .font(.caption)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 3)
                                        .background(Color.secondary.opacity(0.12))
                                        .clipShape(Capsule())

                                    Spacer()

                                    Text("\(providerGroup.accounts.count) 个账号 / \(providerGroup.totalKeys) 个 Key")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }

                                ForEach(providerGroup.accounts) { account in
                                    VStack(alignment: .leading, spacing: 8) {
                                        HStack(spacing: 8) {
                                            Circle()
                                                .fill(authStatusColor(for: account))
                                                .frame(width: 8, height: 8)

                                            Text(account.title)
                                                .font(.callout)
                                                .lineLimit(1)
                                                .truncationMode(.middle)

                                            Spacer()

                                            Text(account.statusText)
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                        }

                                        HStack(spacing: 6) {
                                            if let subtitle = account.subtitle, !subtitle.isEmpty {
                                                Text(subtitle)
                                                    .font(.caption)
                                                    .foregroundStyle(.secondary)
                                                    .lineLimit(1)
                                                    .truncationMode(.middle)
                                            }

                                            Spacer()

                                            Text("\(account.modelCount) 个模型")
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                            Text("\(account.boundKeyCount) 个 Key")
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    .padding(10)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .background(Color.secondary.opacity(0.06))
                                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                }
                            }
                        }
                    }
                }
                .frame(height: MenuBarLayout.serviceAccountGroupsHeight(for: viewModel.serviceProviderGroups))
            }

            Divider()

            HStack(spacing: 8) {
                Text("运行日志")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Toggle("仅错误", isOn: $viewModel.showOnlyErrorLogs)
                    .toggleStyle(.switch)
                    .controlSize(.small)
                Button(action: {
                    viewModel.openLogFile()
                }) {
                    Image(systemName: "macwindow")
                }
                .buttonStyle(.borderless)
                .help("在 macOS 控制台中打开日志文件")
                Button(action: {
                    viewModel.copyErrorLogs()
                }) {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .help("复制错误日志")
            }

            if viewModel.filteredServiceLogs.isEmpty {
                Text("暂无日志")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 8)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 4) {
                        ForEach(viewModel.filteredServiceLogs) { line in
                            Text(line.text)
                                .font(.system(size: 11, design: .monospaced))
                                .foregroundStyle(line.isError ? .red : .secondary)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                }
                .frame(height: MenuBarLayout.serviceLogHeight(for: viewModel.filteredServiceLogs.count))
            }
        }
    }

    private var keysPanel: some View {
        VStack(alignment: .leading, spacing: 10) {
            if !viewModel.clientKeyManagementAvailable {
                Text("当前后端未提供 client-api-keys 接口，先只显示账号状态；升级后端后才可按账号绑定 Key。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 8) {
                Text("Key 管理")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Text("\(viewModel.apiKeys.count) 个 Key")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if viewModel.availableProviders.isEmpty {
                Text("暂无可绑定账号，请先在控制台登录 Auggie 或 Antigravity。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if viewModel.clientKeyManagementAvailable {
                VStack(alignment: .leading, spacing: 8) {
                    Text("新增 Key")
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    HStack(spacing: 6) {
                        Picker(
                            "Provider",
                            selection: Binding(
                                get: { viewModel.selectedProvider },
                                set: { viewModel.setSelectedProvider($0) }
                            )
                        ) {
                            ForEach(viewModel.availableProviders, id: \.self) { provider in
                                Text(provider).tag(provider)
                            }
                        }
                        .labelsHidden()
                        .frame(maxWidth: 130)

                        Picker(
                            "Account",
                            selection: Binding(
                                get: { viewModel.selectedAuthID },
                                set: { viewModel.setSelectedAuthID($0) }
                            )
                        ) {
                            ForEach(viewModel.filteredAuthTargets) { target in
                                Text(target.displayName).tag(target.id)
                            }
                        }
                        .labelsHidden()
                    }

                    if let target = viewModel.selectedAuthTarget {
                        HStack(spacing: 6) {
                            Text("当前绑定：\(target.provider) / \(target.displayName)")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                            Text("\(target.models.count) 个模型")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }

                    HStack(spacing: 6) {
                        TextField("粘贴 sk-key", text: $viewModel.newKeyInput)
                            .textFieldStyle(.roundedBorder)
                        TextField("备注（可选）", text: $viewModel.newKeyNoteInput)
                            .textFieldStyle(.roundedBorder)
                        Button("添加") {
                            viewModel.addManualKey()
                        }
                        .disabled(!viewModel.canManageScopedKeys)
                    }

                    HStack(spacing: 6) {
                        Button("生成并添加") {
                            viewModel.generateAndAddKey()
                        }
                        .disabled(!viewModel.canManageScopedKeys)
                        Spacer()
                    }
                }
            }

            if keyBindingGroups.isEmpty {
                Text("暂无 Key 或账号绑定信息")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(keyBindingGroups) { providerGroup in
                            VStack(alignment: .leading, spacing: 8) {
                                HStack(spacing: 8) {
                                    Text(providerGroup.provider)
                                        .font(.caption)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 3)
                                        .background(Color.secondary.opacity(0.12))
                                        .clipShape(Capsule())

                                    Spacer()

                                    Text("\(providerGroup.accounts.count) 个账号 / \(providerGroup.totalKeys) 个 Key")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }

                                ForEach(providerGroup.accounts) { account in
                                    VStack(alignment: .leading, spacing: 8) {
                                        HStack(spacing: 8) {
                                            Text(account.title)
                                                .font(.callout)
                                                .lineLimit(1)
                                                .truncationMode(.middle)

                                            Spacer()

                                            Text("\(account.totalKeys) 个 Key")
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                        }

                                        if account.keys.isEmpty {
                                            Text("暂无绑定 Key")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        } else {
                                            VStack(alignment: .leading, spacing: 8) {
                                                ForEach(account.keys) { entry in
                                                    keyEntryView(entry)
                                                }
                                            }
                                        }
                                    }
                                    .padding(10)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .background(Color.secondary.opacity(0.06))
                                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                }
                            }
                        }
                    }
                }
                .frame(height: MenuBarLayout.keysGroupHeight(for: keyBindingGroups))
            }
        }
    }

    private func keyEntryView(_ entry: APIKeyEntry) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(entry.masked)
                    .font(.callout)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .foregroundStyle(entry.enabled ? .primary : .secondary)

                Spacer()

                Text("\(viewModel.requestsForKey(entry.id))")
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)

                Toggle(
                    "",
                    isOn: Binding(
                        get: { entry.enabled },
                        set: { newValue in
                            viewModel.setKeyEnabled(entry.id, enabled: newValue)
                        }
                    )
                )
                .toggleStyle(.switch)
                .labelsHidden()
                .controlSize(.small)

                Button("复制") {
                    viewModel.copyKey(entry.id)
                }
                .buttonStyle(.borderless)

                Button("删除") {
                    viewModel.removeKey(entry.id)
                }
                .buttonStyle(.borderless)
            }

            HStack(spacing: 6) {
                TextField(
                    "备注",
                    text: Binding(
                        get: { noteDrafts[entry.id] ?? entry.note },
                        set: { noteDrafts[entry.id] = $0 }
                    )
                )
                .textFieldStyle(.roundedBorder)
                .font(.caption)

                Button("保存") {
                    let note = noteDrafts[entry.id] ?? entry.note
                    viewModel.updateKeyNote(entry.id, note: note)
                }
                .buttonStyle(.borderless)
                .font(.caption)
            }
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(NSColor.controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func authStatusColor(for account: ServiceAccountGroup) -> Color {
        if account.unavailable {
            return .red
        }
        if account.disabled {
            return .orange
        }
        if account.statusText.lowercased().contains("active") {
            return .green
        }
        if account.statusText == "未同步" {
            return .orange
        }
        return .secondary
    }

    private var usagePanel: some View {
        Group {
            if usageGroups.isEmpty {
                Text("暂无贡献数据")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 10)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 8) {
                        Text("Provider / 账号 / Key / 模型")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Spacer()
                        Text("\(usageGroups.count) 个 Provider / \(viewModel.keyUsages.count) 个 Key")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }

                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 12) {
                            ForEach(usageGroups) { providerGroup in
                                HStack(spacing: 8) {
                                    Text(providerGroup.provider)
                                        .font(.caption)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 3)
                                        .background(Color.secondary.opacity(0.12))
                                        .clipShape(Capsule())
                                    Spacer()
                                    Text("\(providerGroup.totalRequests) 次")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                    Text("\(UsageMonitorViewModel.compactNumber(providerGroup.totalTokens)) 词")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }

                                VStack(alignment: .leading, spacing: 10) {
                                    ForEach(providerGroup.accounts) { account in
                                        VStack(alignment: .leading, spacing: 8) {
                                            HStack(spacing: 8) {
                                                Text(account.title)
                                                    .font(.callout)
                                                    .lineLimit(1)
                                                    .truncationMode(.middle)
                                                Spacer()
                                                Text("\(account.totalRequests) 次")
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                                Text("\(UsageMonitorViewModel.compactNumber(account.totalTokens)) 词")
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                            }

                                            if let subtitle = account.subtitle, !subtitle.isEmpty {
                                                Text(subtitle)
                                                    .font(.caption)
                                                    .foregroundStyle(.secondary)
                                                    .lineLimit(1)
                                                    .truncationMode(.middle)
                                            }

                                            VStack(alignment: .leading, spacing: 8) {
                                                ForEach(account.keys) { key in
                                                    VStack(alignment: .leading, spacing: 6) {
                                                        HStack(spacing: 8) {
                                                            Text(key.label)
                                                                .font(.caption)
                                                                .foregroundStyle(.secondary)
                                                                .lineLimit(1)
                                                                .truncationMode(.middle)
                                                            Spacer()
                                                            Text("\(key.totalRequests)")
                                                                .font(.caption)
                                                                .monospacedDigit()
                                                            Text("\(UsageMonitorViewModel.compactNumber(key.totalTokens)) 词")
                                                                .font(.caption2)
                                                                .foregroundStyle(.secondary)
                                                        }

                                                        ForEach(key.modelCalls) { item in
                                                            HStack(spacing: 8) {
                                                                Text(item.id)
                                                                    .font(.callout)
                                                                    .lineLimit(1)
                                                                    .truncationMode(.middle)
                                                                Spacer()
                                                                Text("\(item.requests)")
                                                                    .font(.caption)
                                                                    .monospacedDigit()
                                                                    .foregroundStyle(.secondary)
                                                                Text("\(UsageMonitorViewModel.compactNumber(item.totalTokens))")
                                                                    .font(.caption2)
                                                                    .foregroundStyle(.secondary)
                                                            }
                                                        }
                                                    }
                                                    .padding(8)
                                                    .frame(maxWidth: .infinity, alignment: .leading)
                                                    .background(Color(NSColor.controlBackgroundColor))
                                                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                                                }
                                            }
                                        }
                                        .padding(10)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                        .background(Color.secondary.opacity(0.06))
                                        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                    }
                                }
                            }
                        }
                    }
                    .frame(height: MenuBarLayout.usageListHeight(for: usageGroups))
                }
            }
        }
    }
}
