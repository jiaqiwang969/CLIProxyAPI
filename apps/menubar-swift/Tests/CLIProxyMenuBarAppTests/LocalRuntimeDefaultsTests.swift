import Testing
@testable import CLIProxyMenuBarApp

@Test func defaultConfigYAMLUsesLocalhostAndDefaultManagementKey() {
    let yaml = LocalRuntimeDefaults.defaultConfigYAML()

    #expect(yaml.contains("host: \"127.0.0.1\""))
    #expect(yaml.contains("secret-key: \"cliproxy-menubar-dev\""))
    #expect(yaml.contains("usage-statistics-enabled: true"))
}
