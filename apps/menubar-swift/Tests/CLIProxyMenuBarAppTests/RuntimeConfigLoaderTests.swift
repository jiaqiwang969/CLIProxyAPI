import Foundation
import Testing
@testable import CLIProxyMenuBarApp

@Suite(.serialized)
struct RuntimeConfigLoaderTests {
    @Test func runtimeConfigPrefersManagedConfigOverRepoConfig() throws {
        let home = NSHomeDirectory()
        let managedConfigPath = (home as NSString).appendingPathComponent(".cliproxyapi/config.yaml")
        let repoConfigPath = (home as NSString).appendingPathComponent("05-api-代理/CLIProxyAPI-wjq/apps/server-go/config.yaml")

        #expect(FileManager.default.fileExists(atPath: managedConfigPath))
        #expect(FileManager.default.fileExists(atPath: repoConfigPath))

        let config = RuntimeConfigLoader.load()

        #expect(config.configPath == managedConfigPath)
        #expect(config.managementKey == "cliproxy-menubar-dev")
    }

    @Test func runtimeConfigUsesDefaultMenubarKeyForRepoServerConfigWithHashedSecret() throws {
        let tempRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
        let repoConfigDir = tempRoot
            .appendingPathComponent("workspace")
            .appendingPathComponent("apps")
            .appendingPathComponent("server-go")
        let repoConfigPath = repoConfigDir.appendingPathComponent("config.yaml")
        let originalConfigPath = ProcessInfo.processInfo.environment["CLIPROXY_CONFIG_PATH"]
        let originalBaseURL = ProcessInfo.processInfo.environment["CLIPROXY_BASE_URL"]
        let originalManagementKey = ProcessInfo.processInfo.environment["CLIPROXY_MANAGEMENT_KEY"]

        try FileManager.default.createDirectory(at: repoConfigDir, withIntermediateDirectories: true)
        try """
        port: 9123
        remote-management:
          secret-key: "$2a$10$abcdefghijklmnopqrstuv1234567890abcdefghijklmnopqrstuv"
        """.write(to: repoConfigPath, atomically: true, encoding: .utf8)

        setenv("CLIPROXY_CONFIG_PATH", repoConfigPath.path, 1)
        unsetenv("CLIPROXY_BASE_URL")
        unsetenv("CLIPROXY_MANAGEMENT_KEY")

        defer {
            if let originalConfigPath {
                setenv("CLIPROXY_CONFIG_PATH", originalConfigPath, 1)
            } else {
                unsetenv("CLIPROXY_CONFIG_PATH")
            }
            if let originalBaseURL {
                setenv("CLIPROXY_BASE_URL", originalBaseURL, 1)
            } else {
                unsetenv("CLIPROXY_BASE_URL")
            }
            if let originalManagementKey {
                setenv("CLIPROXY_MANAGEMENT_KEY", originalManagementKey, 1)
            } else {
                unsetenv("CLIPROXY_MANAGEMENT_KEY")
            }
            try? FileManager.default.removeItem(at: tempRoot)
        }

        let config = RuntimeConfigLoader.load()

        #expect(config.configPath == repoConfigPath.path)
        #expect(config.baseURL == "http://localhost:9123")
        #expect(config.managementKey == "cliproxy-menubar-dev")
    }
}
