import AppKit
import SwiftUI
import ServiceManagement

@main
struct CLIProxyMenuBarApp: App {
    @StateObject private var viewModel = UsageMonitorViewModel()
    @AppStorage("launchAtLogin") private var launchAtLogin = false

    init() {
        NSApplication.shared.setActivationPolicy(.accessory)
    }

    var body: some Scene {
        MenuBarExtra {
            MenuBarDashboardView(viewModel: viewModel)
        } label: {
            Label {
                Text(viewModel.menuBarTitle)
            } icon: {
                if viewModel.serviceStatus.isRunning {
                    Image(systemName: viewModel.monitorEnabled ? "bolt.fill" : "bolt.slash")
                } else if viewModel.errorMessage != nil {
                    Image(systemName: "exclamationmark.triangle")
                } else {
                    Image(systemName: "pause.circle")
                }
            }
        }
        .menuBarExtraStyle(.window)
        .onChange(of: launchAtLogin) { newValue in
            Task {
                do {
                    if newValue {
                        if SMAppService.mainApp.status == .enabled { return }
                        try SMAppService.mainApp.register()
                    } else {
                        if SMAppService.mainApp.status == .notRegistered { return }
                        try await SMAppService.mainApp.unregister()
                    }
                } catch {
                    // If registration fails (e.g. not in /Applications or lacking permissions),
                    // revert the toggle on the main thread and show the error via the view model.
                    await MainActor.run {
                        launchAtLogin = false
                        viewModel.actionMessage = "开机自启设置失败: 请尝试将 App 移动到「应用程序」文件夹"
                        print("Failed to update Launch at Login setting: \(error)")
                    }
                }
            }
        }
    }
}
