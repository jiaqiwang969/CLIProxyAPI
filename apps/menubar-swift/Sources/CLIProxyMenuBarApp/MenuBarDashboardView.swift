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
        .frame(width: 400)
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
        .frame(maxHeight: 220)
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
                .frame(maxHeight: 140)
            }
        }
    }

    private var keysPanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !viewModel.hasConfigFile {
                Text("未找到本地 config.yaml，无法管理 Key")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                
                Button("生成默认配置") {
                    viewModel.createDefaultConfig()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            } else {
                HStack(spacing: 6) {
                    TextField("粘贴 sk-key", text: $viewModel.newKeyInput)
                        .textFieldStyle(.roundedBorder)
                    TextField("备注（可选）", text: $viewModel.newKeyNoteInput)
                        .textFieldStyle(.roundedBorder)
                    Button("添加") {
                        viewModel.addManualKey()
                    }
                }

                HStack(spacing: 6) {
                    Button("生成并添加") {
                        viewModel.generateAndAddKey()
                    }
                    Spacer()
                    Text("共 \(viewModel.apiKeys.count) 个")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }

            if viewModel.apiKeys.isEmpty {
                Text("暂无 sk-key")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ScrollView {
                    LazyVStack(spacing: 6) {
                        ForEach(Array(viewModel.apiKeys.enumerated()), id: \.element.id) { index, entry in
                            VStack(alignment: .leading, spacing: 4) {
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
                            .padding(.vertical, 4)
                            Divider()
                        }
                    }
                }
                .frame(maxHeight: 240)
            }
        }
    }

    private var usagePanel: some View {
        return Group {
            if viewModel.keyUsages.isEmpty {
                Text("暂无贡献数据")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 10)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        ForEach(viewModel.keyUsages) { keyUsage in
                            VStack(alignment: .leading, spacing: 6) {
                                HStack(spacing: 8) {
                                    Text(keyUsage.label)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                    Spacer()
                                    Text("\(keyUsage.totalRequests)")
                                        .font(.caption)
                                        .monospacedDigit()
                                    Text("\(UsageMonitorViewModel.compactNumber(keyUsage.totalTokens)) 词")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }

                                ForEach(keyUsage.modelCalls) { item in
                                    HStack(spacing: 8) {
                                        Text(item.id)
                                            .lineLimit(1)
                                            .truncationMode(.middle)
                                        Spacer()
                                        Text("\(item.requests)")
                                            .monospacedDigit()
                                            .foregroundStyle(.secondary)
                                    }
                                    .font(.callout)
                                }
                            }
                        }
                    }
                }
                .frame(maxHeight: 220)
            }
        }
    }
}
