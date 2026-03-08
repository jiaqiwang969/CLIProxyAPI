import Foundation
import Testing
@testable import CLIProxyMenuBarApp

@Test func addKeyReplacesInlineEmptyAPIKeysList() throws {
    let tempDirectory = FileManager.default.temporaryDirectory
        .appendingPathComponent(UUID().uuidString, isDirectory: true)
    try FileManager.default.createDirectory(at: tempDirectory, withIntermediateDirectories: true)

    let configURL = tempDirectory.appendingPathComponent("config.yaml")
    let config = """
    api-keys: []
    auth-dir: \(tempDirectory.path)/auth
    host: 127.0.0.1
    port: 8317
    remote-management:
      allow-remote: false
      disable-control-panel: true
      secret-key: ""
    usage-statistics-enabled: true
    """
    try config.write(to: configURL, atomically: true, encoding: .utf8)

    try APIKeyStore.addKey(configPath: configURL.path, rawKey: "sk-test-inline")

    let updated = try String(contentsOf: configURL, encoding: .utf8)
    let apiKeysHeaders = updated
        .components(separatedBy: .newlines)
        .filter { $0.trimmingCharacters(in: .whitespaces).hasPrefix("api-keys:") }

    #expect(apiKeysHeaders.count == 1)
    #expect(updated.contains("api-keys:\n  - \"sk-test-inline\""))
}
