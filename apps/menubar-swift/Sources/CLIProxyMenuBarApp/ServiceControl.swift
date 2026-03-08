import Foundation

struct LocalServiceStatus: Equatable {
    let isRunning: Bool
    let pid: Int?
    let detail: String?

    static let unknown = LocalServiceStatus(isRunning: false, pid: nil, detail: nil)
}

enum LocalServiceControlError: LocalizedError {
    case configNotFound
    case binaryNotFound(String)
    case failedToStart

    var errorDescription: String? {
        switch self {
        case .configNotFound:
            return "未找到 config.yaml，无法控制本地服务"
        case let .binaryNotFound(path):
            return "未找到可执行文件: \(path)"
        case .failedToStart:
            return "服务启动失败"
        }
    }
}

enum LocalServiceController {
    static func queryStatus(config: RuntimeConfig) async -> LocalServiceStatus {
        let pid = await processID(port: config.port)
        if let pid {
            return LocalServiceStatus(isRunning: true, pid: pid, detail: nil)
        }

        if let binaryPath = config.binaryPath,
           !FileManager.default.isExecutableFile(atPath: binaryPath) {
            return LocalServiceStatus(isRunning: false, pid: nil, detail: "缺少二进制: \(binaryPath)")
        }

        return LocalServiceStatus(isRunning: false, pid: nil, detail: nil)
    }

    static func start(config: RuntimeConfig) async throws {
        guard let configPath = config.configPath else {
            throw LocalServiceControlError.configNotFound
        }
        guard let binaryPath = config.binaryPath else {
            throw LocalServiceControlError.binaryNotFound("(unknown)")
        }
        guard FileManager.default.isExecutableFile(atPath: binaryPath) else {
            throw LocalServiceControlError.binaryNotFound(binaryPath)
        }

        let logPath = (NSTemporaryDirectory() as NSString).appendingPathComponent("cli-proxy-api.log")
        let command = "nohup \(shellQuote(binaryPath)) -config \(shellQuote(configPath)) >\(shellQuote(logPath)) 2>&1 &"
        _ = try await runShell(command)

        try? await Task.sleep(for: .seconds(1))
        let status = await queryStatus(config: config)
        guard status.isRunning else {
            throw LocalServiceControlError.failedToStart
        }
    }

    static func stop(config: RuntimeConfig) async {
        let pid = await processID(port: config.port)
        if let pid {
            _ = try? await runShell("kill \(pid)")
        }

        if let configPath = config.configPath {
            _ = try? await runShell("pkill -f \(shellQuote("cli-proxy-api -config \(configPath)"))")
        }
    }

    private static func processID(port: Int) async -> Int? {
        let command = "lsof -t -iTCP:\(port) -sTCP:LISTEN -n -P | head -n 1"
        guard let output = try? await runShell(command) else {
            return nil
        }
        let trimmed = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return Int(trimmed)
    }

    private static func runShell(_ command: String) async throws -> String {
        try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/bin/zsh")
            process.arguments = ["-lc", command]

            let pipe = Pipe()
            process.standardOutput = pipe
            process.standardError = pipe
            process.standardInput = nil

            process.terminationHandler = { proc in
                Task {
                    let data = try? pipe.fileHandleForReading.readToEnd()
                    let output = String(decoding: data ?? Data(), as: UTF8.self)
                    if proc.terminationStatus == 0 {
                        continuation.resume(returning: output)
                    } else {
                        continuation.resume(throwing: NSError(
                            domain: "LocalServiceController",
                            code: Int(proc.terminationStatus),
                            userInfo: [NSLocalizedDescriptionKey: output]
                        ))
                    }
                }
            }

            do {
                try process.run()
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }

    private static func shellQuote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
