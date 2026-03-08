import Foundation

struct RuntimeConfig {
    let baseURL: String
    let managementKey: String
    let configPath: String?

    var port: Int {
        if let components = URLComponents(string: baseURL), let port = components.port {
            return port
        }
        return 8317
    }

    var binaryPath: String? {
        guard let configPath else {
            return nil
        }

        for path in candidateBinaryPaths(configPath: configPath) {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }
        return candidateBinaryPaths(configPath: configPath).first
    }

    private func candidateBinaryPaths(configPath: String) -> [String] {
        let directory = (configPath as NSString).deletingLastPathComponent
        guard !directory.isEmpty else {
            return []
        }

        var paths: [String] = [
            (directory as NSString).appendingPathComponent("cli-proxy-api"),
            (directory as NSString).appendingPathComponent("server")
        ]

        let normalizedDirectory = directory.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        if !normalizedDirectory.hasSuffix("apps/server-go") {
            let serverDir = (directory as NSString).appendingPathComponent("apps/server-go")
            paths.append((serverDir as NSString).appendingPathComponent("cli-proxy-api"))
            paths.append((serverDir as NSString).appendingPathComponent("server"))
        }

        var deduped: [String] = []
        var seen = Set<String>()
        for path in paths where seen.insert(path).inserted {
            deduped.append(path)
        }
        return deduped
    }
}

enum RuntimeConfigLoader {
    static func load() -> RuntimeConfig {
        let env = ProcessInfo.processInfo.environment

        let envBaseURL = env["CLIPROXY_BASE_URL"]?.trimmingCharacters(in: .whitespacesAndNewlines)
        let envKey = env["CLIPROXY_MANAGEMENT_KEY"]?.trimmingCharacters(in: .whitespacesAndNewlines)

        if let envBaseURL, !envBaseURL.isEmpty {
            return RuntimeConfig(
                baseURL: envBaseURL,
                managementKey: envKey ?? "",
                configPath: nil
            )
        }

        for path in candidateConfigPaths(env: env) {
            if let parsed = parseConfigFile(at: path) {
                return parsed
            }
        }

        return RuntimeConfig(
            baseURL: "http://localhost:8317",
            managementKey: envKey ?? "",
            configPath: nil
        )
    }

    private static func candidateConfigPaths(env: [String: String]) -> [String] {
        var paths: [String] = []

        if let explicit = env["CLIPROXY_CONFIG_PATH"], !explicit.isEmpty {
            paths.append(explicit)
        }

        let cwd = FileManager.default.currentDirectoryPath
        paths.append((cwd as NSString).appendingPathComponent("config.yaml"))
        paths.append((cwd as NSString).appendingPathComponent("apps/server-go/config.yaml"))
        paths.append((cwd as NSString).appendingPathComponent("../CLIProxyAPI-wjq/apps/server-go/config.yaml"))

        let home = NSHomeDirectory()
        paths.append((home as NSString).appendingPathComponent("05-api-代理/CLIProxyAPI-wjq/config.yaml"))
        paths.append((home as NSString).appendingPathComponent("05-api-代理/CLIProxyAPI-wjq/apps/server-go/config.yaml"))
        paths.append((home as NSString).appendingPathComponent("CLIProxyAPI-wjq/config.yaml"))
        paths.append((home as NSString).appendingPathComponent("CLIProxyAPI-wjq/apps/server-go/config.yaml"))
        paths.append((home as NSString).appendingPathComponent(".cliproxyapi/config.yaml"))

        var deduped: [String] = []
        var seen = Set<String>()
        for path in paths where !path.isEmpty {
            if seen.insert(path).inserted {
                deduped.append(path)
            }
        }
        return deduped
    }

    private static func parseConfigFile(at path: String) -> RuntimeConfig? {
        guard FileManager.default.fileExists(atPath: path) else {
            return nil
        }

        guard let raw = try? String(contentsOfFile: path, encoding: .utf8) else {
            return nil
        }

        let port = parsePort(from: raw) ?? 8317
        let key = parseRemoteManagementKey(from: raw) ?? ""

        return RuntimeConfig(
            baseURL: "http://localhost:\(port)",
            managementKey: key,
            configPath: path
        )
    }

    private static func parsePort(from yaml: String) -> Int? {
        for line in yaml.components(separatedBy: .newlines) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("#") {
                continue
            }
            if trimmed.hasPrefix("port:") {
                let value = trimmed.dropFirst("port:".count).trimmingCharacters(in: .whitespaces)
                if let port = Int(value) {
                    return port
                }
            }
        }
        return nil
    }

    private static func parseRemoteManagementKey(from yaml: String) -> String? {
        var inRemoteManagement = false

        for line in yaml.components(separatedBy: .newlines) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("#") || trimmed.isEmpty {
                continue
            }

            let isTopLevelKey = !line.hasPrefix(" ") && trimmed.hasSuffix(":")
            if isTopLevelKey {
                inRemoteManagement = trimmed == "remote-management:"
                continue
            }

            if inRemoteManagement && trimmed.hasPrefix("secret-key:") {
                let rawValue = trimmed.dropFirst("secret-key:".count).trimmingCharacters(in: .whitespaces)
                let unquoted = rawValue.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
                return unquoted
            }
        }

        return nil
    }
}
