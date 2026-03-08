import Foundation
import UserNotifications
import AppKit

@MainActor
final class UsageMonitorViewModel: ObservableObject {
    @Published var summary: UsageSummary?
    @Published var isRefreshing = false
    @Published var errorMessage: String?
    @Published var serviceStatus: LocalServiceStatus = .unknown
    @Published var apiKeys: [APIKeyEntry] = []
    @Published var newKeyInput = ""
    @Published var newKeyNoteInput = ""
    @Published var actionMessage: String?

    @Published var showOnlyErrorLogs = false
    @Published var serviceLogs: [LogLine] = []
    private var logProcess: Process?
    private var logFileHandle: FileHandle?

    // Tracks the last known run state to detect crashes
    private var wasRunningPreviously: Bool = false

    @Published var monitorEnabled: Bool {
        didSet {
            UserDefaults.standard.set(monitorEnabled, forKey: Self.monitorEnabledKey)
            reconfigureMonitorLoop()
            Task { await refreshNow() }
        }
    }

    private var monitorTask: Task<Void, Never>?
    private let client: CLIProxyAPIClient

    private static let pollingIntervalSeconds: Double = 10
    private static let monitorEnabledKey = "menubar.monitorEnabled"

    init(client: CLIProxyAPIClient = CLIProxyAPIClient()) {
        self.client = client

        let defaults = UserDefaults.standard
        if defaults.object(forKey: Self.monitorEnabledKey) == nil {
            self.monitorEnabled = true
        } else {
            self.monitorEnabled = defaults.bool(forKey: Self.monitorEnabledKey)
        }

        setupNotifications()

        Task { await refreshNow() }
        reconfigureMonitorLoop()
    }

    deinit {
        monitorTask?.cancel()
        logFileHandle?.readabilityHandler = nil
        logProcess?.terminate()
    }

    var menuBarTitle: String {
        if !monitorEnabled {
            return "OFF"
        }
        guard let totalRequests = summary?.displayRequests, let totalTokens = summary?.displayTokens else {
            return "--"
        }
        return "\(Self.compactNumber(totalRequests)) / \(Self.compactNumber(totalTokens))"
    }

    var keyUsages: [APIKeyUsage] {
        summary?.keyUsages ?? []
    }

    var hasConfigFile: Bool {
        RuntimeConfigLoader.load().configPath != nil
    }

    var serviceStatusText: String {
        if serviceStatus.isRunning {
            if let pid = serviceStatus.pid {
                return "运行中 (PID \(pid))"
            }
            return "运行中"
        }

        if let detail = serviceStatus.detail, !detail.isEmpty {
            return detail
        }
        return "已停止"
    }

    func requestsForKey(_ key: String) -> Int64 {
        keyUsages.first { $0.id == key }?.totalRequests ?? 0
    }

    var launchAtLoginEnabled: Bool {
        UserDefaults.standard.bool(forKey: "launchAtLogin")
    }

    func toggleLaunchAtLogin() {
        let newValue = !launchAtLoginEnabled
        UserDefaults.standard.set(newValue, forKey: "launchAtLogin")
    }

    var filteredServiceLogs: [LogLine] {
        if showOnlyErrorLogs {
            return serviceLogs.filter { $0.isError }
        }
        return serviceLogs
    }

    func openConfigFile() {
        if let configPath = RuntimeConfigLoader.load().configPath {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: configPath)
        }
    }

    func openLogFile() {
        let logPath = (NSTemporaryDirectory() as NSString).appendingPathComponent("cli-proxy-api.log")
        if FileManager.default.fileExists(atPath: logPath) {
            let url = URL(fileURLWithPath: logPath)
            let config = NSWorkspace.OpenConfiguration()
            if let consoleUrl = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.Console") {
                NSWorkspace.shared.open([url], withApplicationAt: consoleUrl, configuration: config, completionHandler: nil)
            } else {
                NSWorkspace.shared.open(url)
            }
        } else {
            actionMessage = "暂无本地日志文件"
        }
    }

    func checkForUpdates() {
        Task {
            guard let url = URL(string: "https://api.github.com/repos/jiaqiwang969/cliProxyAPI-Dashboard/releases/latest") else { return }
            do {
                let (data, response) = try await URLSession.shared.data(from: url)
                guard let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200,
                      let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let tagName = json["tag_name"] as? String else {
                    actionMessage = "检查更新失败"
                    return
                }
                
                if let htmlUrlStr = json["html_url"] as? String, let htmlUrl = URL(string: htmlUrlStr) {
                    actionMessage = "发现最新版本: \(tagName)"
                    NSWorkspace.shared.open(htmlUrl)
                }
            } catch {
                actionMessage = "检查更新出错: \(error.localizedDescription)"
            }
        }
    }

    func createDefaultConfig() {
        Task {
            let configContent = """
            port: 8317
            remote-management:
              allow-remote: false
              secret-key: ""
            usage-statistics-enabled: true
            api-keys:
              - "sk-default-key # your first key"
            """
            
            let fm = FileManager.default
            let homeURL = fm.homeDirectoryForCurrentUser
            let appDir = homeURL.appendingPathComponent(".cliproxyapi")
            let configPath = appDir.appendingPathComponent("config.yaml")
            
            do {
                if !fm.fileExists(atPath: appDir.path) {
                    try fm.createDirectory(at: appDir, withIntermediateDirectories: true)
                }
                try configContent.write(to: configPath, atomically: true, encoding: .utf8)
                actionMessage = "已生成默认配置 ~/.cliproxyapi/config.yaml"
                await refreshNow()
            } catch {
                actionMessage = "创建配置失败: \(error.localizedDescription)"
            }
        }
    }

    func refreshNow() async {
        let runtimeConfig = RuntimeConfigLoader.load()

        isRefreshing = true
        defer { isRefreshing = false }

        if monitorEnabled {
            do {
                let newSummary = try await client.fetchUsageSummary(
                    baseURL: runtimeConfig.baseURL,
                    managementKey: runtimeConfig.managementKey
                )
                summary = newSummary
                errorMessage = nil
            } catch {
                errorMessage = Self.makeFriendlyError(error, config: runtimeConfig)
            }
        } else {
            summary = nil
            errorMessage = nil
        }

        let previousStatus = serviceStatus.isRunning
        await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        let currentStatus = serviceStatus.isRunning
        
        // Crash detection: If it was running in the previous tick, but now it's not, and we didn't explicitly stop it
        if previousStatus && !currentStatus && wasRunningPreviously {
            sendCrashNotification()
        }
        wasRunningPreviously = currentStatus
        
        if serviceStatus.isRunning && logProcess == nil {
            startLogMonitoring()
        }
    }

    func toggleMonitor() {
        monitorEnabled.toggle()
    }
    
    func copyErrorLogs() {
        let errors = serviceLogs.filter { $0.isError }.map { $0.text }.joined(separator: "\n")
        if !errors.isEmpty {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(errors, forType: .string)
            actionMessage = "已复制最新错误日志"
        } else {
            actionMessage = "当前没有错误日志"
        }
    }

    func startLocalService() {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                try await LocalServiceController.start(config: runtimeConfig)
                wasRunningPreviously = true
                actionMessage = "本地服务已启动"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshNow()
        }
    }

    func stopLocalService() {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            wasRunningPreviously = false // Prevent crash alert when intentionally stopping
            await LocalServiceController.stop(config: runtimeConfig)
            actionMessage = "本地服务已停止"
            await refreshNow()
        }
    }

    func addManualKey() {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                try APIKeyStore.addKey(configPath: runtimeConfig.configPath, rawKey: newKeyInput)
                newKeyInput = ""
                newKeyNoteInput = ""
                actionMessage = "Key 已添加 (配置将自动热重载)"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func generateAndAddKey() {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                let key = APIKeyStore.generateKey()
                try APIKeyStore.addKey(configPath: runtimeConfig.configPath, rawKey: key)
                actionMessage = "已生成新 Key (配置将自动热重载)"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func removeKey(_ key: String) {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                try APIKeyStore.removeKey(configPath: runtimeConfig.configPath, keyToRemove: key)
                actionMessage = "Key 已删除 (配置将自动热重载)"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func updateKeyNote(_ key: String, note: String) {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                try APIKeyStore.updateKeyNote(configPath: runtimeConfig.configPath, keyId: key, note: note)
                actionMessage = "备注已更新" // config update triggers reload
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func setKeyEnabled(_ key: String, enabled: Bool) {
        Task {
            let runtimeConfig = RuntimeConfigLoader.load()
            do {
                try APIKeyStore.setKeyEnabled(configPath: runtimeConfig.configPath, keyId: key, enabled: enabled)
                actionMessage = "状态已更新 (配置将自动热重载)"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    private func reconfigureMonitorLoop() {
        monitorTask?.cancel()

        monitorTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(Self.pollingIntervalSeconds))
                guard !Task.isCancelled else {
                    return
                }
                await self?.refreshNow()
            }
        }
    }

    private func startLogMonitoring() {
        stopLogMonitoring()
        
        let logPath = (NSTemporaryDirectory() as NSString).appendingPathComponent("cli-proxy-api.log")
        guard FileManager.default.fileExists(atPath: logPath) else { return }
        
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/tail")
        process.arguments = ["-n", "100", "-f", logPath]
        
        let pipe = Pipe()
        process.standardOutput = pipe
        self.logFileHandle = pipe.fileHandleForReading
        self.logProcess = process
        
        self.logFileHandle?.readabilityHandler = { [weak self] fileHandle in
            let data = fileHandle.availableData
            guard !data.isEmpty else { return }
            
            if let str = String(data: data, encoding: .utf8) {
                let lines = str.components(separatedBy: .newlines).filter { !$0.isEmpty }
                Task { @MainActor [weak self] in
                    guard let self = self else { return }
                    for line in lines {
                        let isError = line.lowercased().contains("error") || line.lowercased().contains("panic")
                        self.serviceLogs.append(LogLine(text: line, isError: isError))
                    }
                    if self.serviceLogs.count > 100 {
                        self.serviceLogs.removeFirst(self.serviceLogs.count - 100)
                    }
                }
            }
        }
        
        process.terminationHandler = { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.stopLogMonitoring()
            }
        }
        
        try? process.run()
    }

    private func stopLogMonitoring() {
        logFileHandle?.readabilityHandler = nil
        logFileHandle = nil
        logProcess?.terminate()
        logProcess = nil
    }

    private func setupNotifications() {
        guard NotificationRuntimeSupport.canUseUserNotifications() else {
            return
        }
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { granted, error in
            if let error = error {
                print("Notification auth error: \(error)")
            }
        }
    }
    
    private func sendCrashNotification() {
        guard NotificationRuntimeSupport.canUseUserNotifications() else {
            return
        }
        let content = UNMutableNotificationContent()
        content.title = "CLIProxy 意外停止"
        content.body = "后台代理服务已退出，请检查控制台日志。"
        let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }

    private func refreshServiceAndKeys(runtimeConfig: RuntimeConfig) async {
        serviceStatus = await LocalServiceController.queryStatus(config: runtimeConfig)

        do {
            apiKeys = try APIKeyStore.loadEntries(configPath: runtimeConfig.configPath)
        } catch {
            apiKeys = []
        }
    }

    private static func makeFriendlyError(_ error: Error, config: RuntimeConfig) -> String {
        if case let APIClientError.httpError(statusCode, _) = error, statusCode == 401 {
            if config.managementKey.isEmpty {
                return "监控未授权：缺少 Management Key"
            }
            return "监控未授权：Management Key 无效"
        }

        if case let APIClientError.serverMessage(message) = error {
            if message.isEmpty {
                return "暂时无法读取统计"
            }
            return "暂时无法读取统计"
        }

        if case APIClientError.decodeError = error {
            return "统计数据格式暂不兼容，已自动跳过"
        }

        return "暂时无法读取统计"
    }

    static func compactNumber(_ value: Int64) -> String {
        let absolute = abs(Double(value))
        let sign = value < 0 ? "-" : ""

        switch absolute {
        case 1_000_000_000...:
            return String(format: "\(sign)%.1fB", absolute / 1_000_000_000)
        case 1_000_000...:
            return String(format: "\(sign)%.1fM", absolute / 1_000_000)
        case 1_000...:
            return String(format: "\(sign)%.1fK", absolute / 1_000)
        default:
            return "\(value)"
        }
    }
}

struct LogLine: Identifiable {
    let id = UUID()
    let text: String
    let isError: Bool
}
