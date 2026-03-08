import Foundation
import Testing
@testable import CLIProxyMenuBarApp

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
