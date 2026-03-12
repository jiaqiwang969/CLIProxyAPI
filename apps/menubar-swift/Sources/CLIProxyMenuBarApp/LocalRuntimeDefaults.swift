import Foundation

enum LocalRuntimeDefaults {
    static let host = "127.0.0.1"
    static let managementKey = "cliproxy-menubar-dev"

    static func defaultConfigYAML() -> String {
        """
        host: "\(host)"
        port: 8317
        remote-management:
          allow-remote: false
          secret-key: "\(managementKey)"
        usage-statistics-enabled: true
        api-keys:
          - "sk-default-key # your first key"
        """
    }
}
