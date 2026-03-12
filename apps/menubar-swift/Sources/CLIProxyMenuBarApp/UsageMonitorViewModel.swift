import Foundation
import UserNotifications
import AppKit

private struct UsageAccountAccumulator {
    let id: String
    let provider: String
    let authID: String?
    let title: String
    let subtitle: String?
    var keys: [UsageKeyGroup]
    var totalRequests: Int64
    var totalTokens: Int64
}

protocol ClipboardWriting {
    func clearContents()
    @discardableResult
    func setString(_ string: String, forType type: NSPasteboard.PasteboardType) -> Bool
}

final class SystemClipboard: ClipboardWriting {
    private let pasteboard: NSPasteboard

    init(pasteboard: NSPasteboard = .general) {
        self.pasteboard = pasteboard
    }

    func clearContents() {
        pasteboard.clearContents()
    }

    @discardableResult
    func setString(_ string: String, forType type: NSPasteboard.PasteboardType) -> Bool {
        pasteboard.setString(string, forType: type)
    }
}

@MainActor
final class UsageMonitorViewModel: ObservableObject {
    @Published var summary: UsageSummary?
    @Published var isRefreshing = false
    @Published var errorMessage: String?
    @Published var serviceStatus: LocalServiceStatus = .unknown
    @Published var apiKeys: [APIKeyEntry] = []
    @Published var authTargets: [AuthTarget] = []
    @Published var clientKeyManagementAvailable = true
    @Published var selectedProvider = ""
    @Published var selectedAuthID = ""
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
    private let client: any CLIProxyAPIManaging
    private let clipboard: ClipboardWriting
    private let runtimeConfigProvider: @Sendable () -> RuntimeConfig
    private let serviceStatusProvider: @Sendable (RuntimeConfig) async -> LocalServiceStatus
    private var managedClientKeys: [ManagedClientAPIKey] = []

    private static let pollingIntervalSeconds: Double = 10
    private static let monitorEnabledKey = "menubar.monitorEnabled"

    init(
        client: any CLIProxyAPIManaging = CLIProxyAPIClient(),
        clipboard: ClipboardWriting = SystemClipboard(),
        autoRefresh: Bool = true,
        runtimeConfigProvider: @escaping @Sendable () -> RuntimeConfig = { RuntimeConfigLoader.load() },
        serviceStatusProvider: @escaping @Sendable (RuntimeConfig) async -> LocalServiceStatus = {
            await LocalServiceController.queryStatus(config: $0)
        }
    ) {
        self.client = client
        self.clipboard = clipboard
        self.runtimeConfigProvider = runtimeConfigProvider
        self.serviceStatusProvider = serviceStatusProvider

        let defaults = UserDefaults.standard
        if defaults.object(forKey: Self.monitorEnabledKey) == nil {
            self.monitorEnabled = true
        } else {
            self.monitorEnabled = defaults.bool(forKey: Self.monitorEnabledKey)
        }

        setupNotifications()

        if autoRefresh {
            Task { await refreshNow() }
            reconfigureMonitorLoop()
        }
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

    var usageProviderGroups: [UsageProviderGroup] {
        guard !keyUsages.isEmpty else {
            return []
        }

        let authByID = Dictionary(uniqueKeysWithValues: authTargets.map { ($0.id, $0) })
        let apiKeyByID = Dictionary(uniqueKeysWithValues: apiKeys.map { ($0.id, $0) })
        var accountBuckets: [String: UsageAccountAccumulator] = [:]

        for keyUsage in keyUsages {
            let entry = apiKeyByID[keyUsage.id]
            let normalizedAuthID = Self.normalizedIdentifier(entry?.authID)
            let boundTarget = normalizedAuthID.flatMap { authByID[$0] }
            let provider = Self.displayProviderName(entry?.provider ?? boundTarget?.provider)
            let title: String
            let subtitle: String?

            if let boundTarget {
                title = boundTarget.displayName
                subtitle = boundTarget.secondaryLabel
            } else if let entry {
                let fallbackTitle = entry.accountLabel.trimmingCharacters(in: .whitespacesAndNewlines)
                if let normalizedAuthID, fallbackTitle == "未绑定" {
                    title = normalizedAuthID
                } else if fallbackTitle.isEmpty {
                    title = normalizedAuthID == nil ? "未绑定 Key" : "未同步账号"
                } else {
                    title = fallbackTitle
                }
                subtitle = entry.accountDetail
            } else {
                title = "未同步 Key"
                subtitle = nil
            }

            let groupID = normalizedAuthID.map { "\(provider)::\($0)" } ?? "\(provider)::unbound"
            let usageKey = UsageKeyGroup(
                id: keyUsage.id,
                label: entry?.masked ?? keyUsage.label,
                totalRequests: keyUsage.totalRequests,
                totalTokens: keyUsage.totalTokens,
                modelCalls: keyUsage.modelCalls
            )

            if var bucket = accountBuckets[groupID] {
                bucket.keys.append(usageKey)
                bucket.totalRequests += keyUsage.totalRequests
                bucket.totalTokens += keyUsage.totalTokens
                accountBuckets[groupID] = bucket
            } else {
                accountBuckets[groupID] = UsageAccountAccumulator(
                    id: groupID,
                    provider: provider,
                    authID: normalizedAuthID,
                    title: title,
                    subtitle: subtitle,
                    keys: [usageKey],
                    totalRequests: keyUsage.totalRequests,
                    totalTokens: keyUsage.totalTokens
                )
            }
        }

        let accounts = accountBuckets.values.map { bucket in
            UsageAccountGroup(
                id: bucket.id,
                provider: bucket.provider,
                authID: bucket.authID,
                title: bucket.title,
                subtitle: bucket.subtitle,
                totalRequests: bucket.totalRequests,
                totalTokens: bucket.totalTokens,
                keys: bucket.keys.sorted(by: Self.compareUsageKeyGroups)
            )
        }

        let providers = Dictionary(grouping: accounts) { account in
            account.provider
        }

        return providers
            .map { provider, accounts in
                UsageProviderGroup(
                    provider: provider,
                    accounts: accounts.sorted(by: Self.compareUsageAccountGroups)
                )
            }
            .sorted(by: Self.compareUsageProviderGroups)
    }

    var hasConfigFile: Bool {
        runtimeConfigProvider().configPath != nil
    }

    var availableProviders: [String] {
        let providers = selectableAuthTargets.map(\.provider).filter { !$0.isEmpty }
        return Array(Set(providers)).sorted()
    }

    var filteredAuthTargets: [AuthTarget] {
        let candidates = selectableAuthTargets
        guard !selectedProvider.isEmpty else {
            return candidates
        }
        return candidates.filter { $0.provider == selectedProvider }
    }

    var selectedAuthTarget: AuthTarget? {
        filteredAuthTargets.first { $0.id == selectedAuthID }
    }

    var providerKeyGroups: [ProviderKeyGroup] {
        var accountGroups = authTargets.map { target in
            AccountKeyGroup(
                id: target.id,
                provider: Self.displayProviderName(target.provider),
                authID: target.id,
                title: target.displayName,
                subtitle: target.secondaryLabel,
                statusText: target.status ?? (target.disabled ? "disabled" : "active"),
                disabled: target.disabled,
                unavailable: target.unavailable,
                modelCount: target.models.count,
                keys: apiKeys
                    .filter { $0.authID == target.id }
                    .sorted(by: Self.compareAPIKeys)
            )
        }

        let knownAuthIDs = Set(authTargets.map(\.id))
        let orphanKeys = apiKeys.filter { entry in
            guard let authID = entry.authID, !authID.isEmpty else {
                return true
            }
            return !knownAuthIDs.contains(authID)
        }

        let orphanGroups = Dictionary(grouping: orphanKeys, by: Self.orphanAccountGroupKey)
        accountGroups.append(contentsOf: orphanGroups.map { _, entries in
            let sortedEntries = entries.sorted(by: Self.compareAPIKeys)
            let sample = sortedEntries[0]
            let hasScopedAuthID = !((sample.authID ?? "").trimmingCharacters(in: .whitespacesAndNewlines)).isEmpty
            let title = hasScopedAuthID
                ? (sample.accountLabel == "未绑定" ? (sample.authID ?? "未同步账号") : sample.accountLabel)
                : "未绑定 Key"
            let subtitle = sample.authID == nil ? nil : sample.accountDetail
            let statusText = sample.authID == nil ? "legacy" : "未同步"
            return AccountKeyGroup(
                id: Self.orphanAccountGroupKey(for: sample),
                provider: Self.displayProviderName(sample.provider),
                authID: sample.authID,
                title: title,
                subtitle: subtitle,
                statusText: statusText,
                disabled: false,
                unavailable: false,
                modelCount: sortedEntries.reduce(0) { partial, entry in
                    max(partial, entry.modelIDs.count)
                },
                keys: sortedEntries
            )
        })

        let groups = Dictionary(grouping: accountGroups) { account in
            Self.displayProviderName(account.provider)
        }

        return groups
            .map { provider, accounts in
                ProviderKeyGroup(
                    provider: provider,
                    accounts: accounts.sorted(by: Self.compareAccountGroups)
                )
            }
            .sorted(by: Self.compareProviderGroups)
    }

    var serviceProviderGroups: [ServiceProviderGroup] {
        let boundKeyCountByAuthID = managedClientKeys.reduce(into: [String: Int]()) { partial, entry in
            let authID = entry.scope?.authID.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard !authID.isEmpty else {
                return
            }
            partial[authID, default: 0] += 1
        }

        let accountGroups = authTargets.map { target in
            ServiceAccountGroup(
                id: target.id,
                provider: Self.displayProviderName(target.provider),
                authID: target.id,
                title: target.displayName,
                subtitle: target.secondaryLabel,
                statusText: target.status ?? (target.disabled ? "disabled" : "active"),
                disabled: target.disabled,
                unavailable: target.unavailable,
                modelCount: target.models.count,
                boundKeyCount: boundKeyCountByAuthID[target.id] ?? 0
            )
        }

        let groups = Dictionary(grouping: accountGroups) { account in
            Self.displayProviderName(account.provider)
        }

        return groups
            .map { provider, accounts in
                ServiceProviderGroup(
                    provider: provider,
                    accounts: accounts.sorted(by: Self.compareServiceAccountGroups)
                )
            }
            .sorted(by: Self.compareServiceProviderGroups)
    }

    var canManageScopedKeys: Bool {
        selectedAuthTarget != nil && clientKeyManagementAvailable
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

    private var selectableAuthTargets: [AuthTarget] {
        authTargets.filter(\.isSelectable)
    }

    func openConfigFile() {
        if let configPath = runtimeConfigProvider().configPath {
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
            let fm = FileManager.default
            let homeURL = URL(fileURLWithPath: NSHomeDirectory(), isDirectory: true)
            let appDir = homeURL.appendingPathComponent(".cliproxyapi")
            let configPath = appDir.appendingPathComponent("config.yaml")
            
            do {
                if !fm.fileExists(atPath: appDir.path) {
                    try fm.createDirectory(at: appDir, withIntermediateDirectories: true)
                }
                try LocalRuntimeDefaults.defaultConfigYAML().write(
                    to: configPath,
                    atomically: true,
                    encoding: .utf8
                )
                actionMessage = "已生成默认配置 ~/.cliproxyapi/config.yaml"
                await refreshNow()
            } catch {
                actionMessage = "创建配置失败: \(error.localizedDescription)"
            }
        }
    }

    func refreshNow() async {
        let runtimeConfig = runtimeConfigProvider()

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

    func copyKey(_ key: String) {
        clipboard.clearContents()
        if clipboard.setString(key, forType: .string) {
            actionMessage = "已复制 Key"
        } else {
            actionMessage = "复制 Key 失败"
        }
    }

    func startLocalService() {
        Task {
            let runtimeConfig = runtimeConfigProvider()
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
            let runtimeConfig = runtimeConfigProvider()
            wasRunningPreviously = false // Prevent crash alert when intentionally stopping
            await LocalServiceController.stop(config: runtimeConfig)
            actionMessage = "本地服务已停止"
            await refreshNow()
        }
    }

    func setSelectedProvider(_ provider: String) {
        selectedProvider = provider
        normalizeSelectedAuth()
    }

    func setSelectedAuthID(_ authID: String) {
        selectedAuthID = authID
    }

    func addManualKey() {
        Task {
            let runtimeConfig = runtimeConfigProvider()
            let trimmedKey = newKeyInput.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmedKey.isEmpty else {
                actionMessage = APIKeyStoreError.emptyKey.localizedDescription
                return
            }
            guard let target = selectedAuthTarget else {
                actionMessage = "请先选择要绑定的账号"
                return
            }
            do {
                guard !managedClientKeys.contains(where: { $0.key == trimmedKey }) else {
                    actionMessage = APIKeyStoreError.keyAlreadyExists.localizedDescription
                    return
                }
                var updated = managedClientKeys
                updated.append(
                    ManagedClientAPIKey(
                        key: trimmedKey,
                        enabled: true,
                        note: newKeyNoteInput.trimmingCharacters(in: .whitespacesAndNewlines),
                        scope: ClientAPIKeyScope(
                            provider: target.provider,
                            authID: target.id,
                            models: []
                        )
                    )
                )
                try await saveManagedKeys(updated, runtimeConfig: runtimeConfig)
                newKeyInput = ""
                newKeyNoteInput = ""
                actionMessage = "Key 已添加并绑定到账号"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func generateAndAddKey() {
        Task {
            let runtimeConfig = runtimeConfigProvider()
            guard let target = selectedAuthTarget else {
                actionMessage = "请先选择要绑定的账号"
                return
            }
            do {
                let key = APIKeyStore.generateKey()
                var updated = managedClientKeys
                updated.append(
                    ManagedClientAPIKey(
                        key: key,
                        enabled: true,
                        note: newKeyNoteInput.trimmingCharacters(in: .whitespacesAndNewlines),
                        scope: ClientAPIKeyScope(
                            provider: target.provider,
                            authID: target.id,
                            models: []
                        )
                    )
                )
                try await saveManagedKeys(updated, runtimeConfig: runtimeConfig)
                newKeyInput = ""
                newKeyNoteInput = ""
                actionMessage = "已生成新 Key 并绑定到账号"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func removeKey(_ key: String) {
        Task {
            let runtimeConfig = runtimeConfigProvider()
            do {
                let updated = managedClientKeys.filter { $0.key != key }
                try await saveManagedKeys(updated, runtimeConfig: runtimeConfig)
                actionMessage = "Key 已删除"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func updateKeyNote(_ key: String, note: String) {
        Task {
            let runtimeConfig = runtimeConfigProvider()
            do {
                var updated = managedClientKeys
                if let index = updated.firstIndex(where: { $0.key == key }) {
                    updated[index].note = note.trimmingCharacters(in: .whitespacesAndNewlines)
                    try await saveManagedKeys(updated, runtimeConfig: runtimeConfig)
                }
                actionMessage = "备注已更新"
            } catch {
                actionMessage = error.localizedDescription
            }
            await refreshServiceAndKeys(runtimeConfig: runtimeConfig)
        }
    }

    func setKeyEnabled(_ key: String, enabled: Bool) {
        Task {
            let runtimeConfig = runtimeConfigProvider()
            do {
                var updated = managedClientKeys
                if let index = updated.firstIndex(where: { $0.key == key }) {
                    updated[index].enabled = enabled
                    try await saveManagedKeys(updated, runtimeConfig: runtimeConfig)
                }
                actionMessage = "状态已更新"
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

    private func saveManagedKeys(_ keys: [ManagedClientAPIKey], runtimeConfig: RuntimeConfig) async throws {
        try await client.saveClientAPIKeys(
            baseURL: runtimeConfig.baseURL,
            managementKey: runtimeConfig.managementKey,
            keys: keys
        )
    }

    private func refreshServiceAndKeys(runtimeConfig: RuntimeConfig) async {
        serviceStatus = await serviceStatusProvider(runtimeConfig)

        do {
            authTargets = try await client.fetchAuthTargets(
                baseURL: runtimeConfig.baseURL,
                managementKey: runtimeConfig.managementKey
            )
            normalizeSelectionAfterRefresh()
        } catch {
            authTargets = []
            selectedProvider = ""
            selectedAuthID = ""
        }

        do {
            managedClientKeys = try await client.fetchClientAPIKeys(
                baseURL: runtimeConfig.baseURL,
                managementKey: runtimeConfig.managementKey
            )
            clientKeyManagementAvailable = true
            rebuildAPIKeyEntries()
        } catch {
            managedClientKeys = []
            clientKeyManagementAvailable = !Self.isMissingClientKeyManagementEndpoint(error)
            apiKeys = []
            if let legacyEntries = try? APIKeyStore.loadEntries(configPath: runtimeConfig.configPath) {
                apiKeys = legacyEntries
            }
        }
    }

    private func normalizeSelectionAfterRefresh() {
        let providers = availableProviders
        if providers.isEmpty {
            selectedProvider = ""
            selectedAuthID = ""
            return
        }

        if !providers.contains(selectedProvider) {
            selectedProvider = providers[0]
        }
        normalizeSelectedAuth()
    }

    private func normalizeSelectedAuth() {
        let authIDs = filteredAuthTargets.map(\.id)
        if authIDs.contains(selectedAuthID) {
            return
        }
        selectedAuthID = authIDs.first ?? ""
    }

    private func rebuildAPIKeyEntries() {
        let authByID = Dictionary(uniqueKeysWithValues: authTargets.map { ($0.id, $0) })
        apiKeys = managedClientKeys.map { entry in
            let boundTarget = entry.scope.map { authByID[$0.authID] } ?? nil
            let provider = entry.scope?.provider ?? boundTarget?.provider
            let accountLabel = boundTarget?.displayName ?? Self.fallbackAccountLabel(for: entry.scope)
            let accountDetail = boundTarget?.secondaryLabel ?? Self.fallbackAccountDetail(for: entry.scope)
            let modelIDs = boundTarget?.modelIDs ?? entry.scope?.models ?? []

            return APIKeyEntry(
                id: entry.key,
                masked: APIKeyStore.mask(entry.key),
                note: entry.note,
                enabled: entry.enabled,
                createdAt: nil,
                provider: provider,
                authID: entry.scope?.authID,
                accountLabel: accountLabel,
                accountDetail: accountDetail,
                modelIDs: modelIDs
            )
        }
    }

    private static func displayProviderName(_ provider: String?) -> String {
        let trimmed = provider?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? "未绑定" : trimmed
    }

    private static func fallbackAccountLabel(for scope: ClientAPIKeyScope?) -> String {
        guard let scope else {
            return "未绑定"
        }
        let authID = scope.authID.trimmingCharacters(in: .whitespacesAndNewlines)
        return authID.isEmpty ? "未绑定" : authID
    }

    private static func fallbackAccountDetail(for scope: ClientAPIKeyScope?) -> String? {
        guard let scope else {
            return nil
        }
        let authID = scope.authID.trimmingCharacters(in: .whitespacesAndNewlines)
        return authID.isEmpty ? nil : authID
    }

    private static func normalizedIdentifier(_ value: String?) -> String? {
        let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }

    private static func orphanAccountGroupKey(for entry: APIKeyEntry) -> String {
        let provider = displayProviderName(entry.provider)
        let authID = entry.authID?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if authID.isEmpty {
            return "\(provider)::unbound"
        }
        return "\(provider)::\(authID)"
    }

    private static func compareAPIKeys(lhs: APIKeyEntry, rhs: APIKeyEntry) -> Bool {
        if lhs.enabled != rhs.enabled {
            return lhs.enabled && !rhs.enabled
        }
        return lhs.id < rhs.id
    }

    private static func compareAccountGroups(lhs: AccountKeyGroup, rhs: AccountKeyGroup) -> Bool {
        if lhs.authID == nil && rhs.authID != nil {
            return false
        }
        if lhs.authID != nil && rhs.authID == nil {
            return true
        }
        if lhs.totalKeys == rhs.totalKeys {
            return lhs.title.localizedCaseInsensitiveCompare(rhs.title) == .orderedAscending
        }
        return lhs.totalKeys > rhs.totalKeys
    }

    private static func compareProviderGroups(lhs: ProviderKeyGroup, rhs: ProviderKeyGroup) -> Bool {
        if lhs.provider == "未绑定" {
            return false
        }
        if rhs.provider == "未绑定" {
            return true
        }
        return lhs.provider.localizedCaseInsensitiveCompare(rhs.provider) == .orderedAscending
    }

    private static func compareServiceAccountGroups(lhs: ServiceAccountGroup, rhs: ServiceAccountGroup) -> Bool {
        if lhs.boundKeyCount == rhs.boundKeyCount {
            return lhs.title.localizedCaseInsensitiveCompare(rhs.title) == .orderedAscending
        }
        return lhs.boundKeyCount > rhs.boundKeyCount
    }

    private static func compareServiceProviderGroups(lhs: ServiceProviderGroup, rhs: ServiceProviderGroup) -> Bool {
        if lhs.provider == "未绑定" {
            return false
        }
        if rhs.provider == "未绑定" {
            return true
        }
        return lhs.provider.localizedCaseInsensitiveCompare(rhs.provider) == .orderedAscending
    }

    private static func compareUsageKeyGroups(lhs: UsageKeyGroup, rhs: UsageKeyGroup) -> Bool {
        if lhs.totalTokens == rhs.totalTokens {
            if lhs.totalRequests == rhs.totalRequests {
                return lhs.id < rhs.id
            }
            return lhs.totalRequests > rhs.totalRequests
        }
        return lhs.totalTokens > rhs.totalTokens
    }

    private static func compareUsageAccountGroups(lhs: UsageAccountGroup, rhs: UsageAccountGroup) -> Bool {
        if lhs.authID == nil && rhs.authID != nil {
            return false
        }
        if lhs.authID != nil && rhs.authID == nil {
            return true
        }
        if lhs.totalTokens == rhs.totalTokens {
            if lhs.totalRequests == rhs.totalRequests {
                return lhs.title.localizedCaseInsensitiveCompare(rhs.title) == .orderedAscending
            }
            return lhs.totalRequests > rhs.totalRequests
        }
        return lhs.totalTokens > rhs.totalTokens
    }

    private static func compareUsageProviderGroups(lhs: UsageProviderGroup, rhs: UsageProviderGroup) -> Bool {
        if lhs.provider == "未绑定" {
            return false
        }
        if rhs.provider == "未绑定" {
            return true
        }
        return lhs.provider.localizedCaseInsensitiveCompare(rhs.provider) == .orderedAscending
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

    private static func isMissingClientKeyManagementEndpoint(_ error: Error) -> Bool {
        guard case let APIClientError.httpError(statusCode, _) = error else {
            return false
        }
        return statusCode == 404
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
