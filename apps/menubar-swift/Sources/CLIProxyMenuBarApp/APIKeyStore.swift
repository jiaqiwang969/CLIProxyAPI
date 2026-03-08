import Foundation
import Security

struct APIKeyEntry: Identifiable, Equatable {
    let id: String
    let masked: String
    var note: String
    var enabled: Bool
    var createdAt: Date?
}

enum APIKeyStoreError: LocalizedError {
    case configNotFound
    case keyAlreadyExists
    case emptyKey

    var errorDescription: String? {
        switch self {
        case .configNotFound:
            return "未找到 config.yaml"
        case .keyAlreadyExists:
            return "该 Key 已存在"
        case .emptyKey:
            return "Key 不能为空"
        }
    }
}

enum APIKeyStore {
    static func loadEntries(configPath: String?) throws -> [APIKeyEntry] {
        let keyTuples = try loadKeys(configPath: configPath)
        return keyTuples.map { tuple in
            APIKeyEntry(id: tuple.key, masked: mask(tuple.key), note: tuple.note, enabled: tuple.enabled, createdAt: nil)
        }
    }

    static func addKey(configPath: String?, rawKey: String) throws {
        let key = rawKey.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !key.isEmpty else {
            throw APIKeyStoreError.emptyKey
        }

        var keys = try loadKeys(configPath: configPath)
        guard !keys.contains(where: { $0.key == key }) else {
            throw APIKeyStoreError.keyAlreadyExists
        }
        keys.append((key: key, note: "", enabled: true))
        try saveKeys(keys, configPath: configPath)
    }

    static func removeKey(configPath: String?, keyToRemove: String) throws {
        var keys = try loadKeys(configPath: configPath)
        keys.removeAll { $0.key == keyToRemove }
        try saveKeys(keys, configPath: configPath)
    }

    static func generateKey() -> String {
        let alphabet = Array("0123456789abcdef")
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)

        let suffix = bytes.flatMap { byte -> [Character] in
            let high = Int(byte >> 4)
            let low = Int(byte & 0x0f)
            return [alphabet[high], alphabet[low]]
        }

        return "sk-" + String(suffix)
    }

    static func updateKeyNote(configPath: String?, keyId: String, note: String) throws {
        var keys = try loadKeys(configPath: configPath)
        if let index = keys.firstIndex(where: { $0.key == keyId }) {
            keys[index].note = note
            try saveKeys(keys, configPath: configPath)
        }
    }

    static func setKeyEnabled(configPath: String?, keyId: String, enabled: Bool) throws {
        var keys = try loadKeys(configPath: configPath)
        if let index = keys.firstIndex(where: { $0.key == keyId }) {
            keys[index].enabled = enabled
            try saveKeys(keys, configPath: configPath)
        }
    }

    private static func loadKeys(configPath: String?) throws -> [(key: String, note: String, enabled: Bool)] {
        guard let configPath else {
            throw APIKeyStoreError.configNotFound
        }
        guard FileManager.default.fileExists(atPath: configPath) else {
            throw APIKeyStoreError.configNotFound
        }

        let raw = try String(contentsOfFile: configPath, encoding: .utf8)
        let lines = raw.components(separatedBy: .newlines)

        guard let block = apiKeysBlock(in: lines) else {
            return []
        }

        return lines[block.start + 1 ..< block.end]
            .compactMap(parseKeyLine)
    }

    private static func saveKeys(_ keys: [(key: String, note: String, enabled: Bool)], configPath: String?) throws {
        guard let configPath else {
            throw APIKeyStoreError.configNotFound
        }

        let raw = try String(contentsOfFile: configPath, encoding: .utf8)
        var lines = raw.components(separatedBy: .newlines)
        let blockLines = ["api-keys:"] + keys.map { tuple in
            var line = "  - \"\(tuple.key)\""
            if !tuple.enabled || !tuple.note.isEmpty {
                let notePart = tuple.note.isEmpty ? "" : " # \(tuple.note)"
                let disablePart = tuple.enabled ? "" : " # disabled"
                line += "\(disablePart)\(notePart)"
            }
            return line
        }

        if let block = apiKeysBlock(in: lines) {
            lines.replaceSubrange(block.start ..< block.end, with: blockLines)
        } else {
            if let last = lines.last, !last.isEmpty {
                lines.append("")
            }
            lines.append(contentsOf: blockLines)
        }

        let updated = lines.joined(separator: "\n")
        try updated.write(toFile: configPath, atomically: true, encoding: .utf8)
    }

    private static func apiKeysBlock(in lines: [String]) -> (start: Int, end: Int)? {
        var start: Int?

        for (index, line) in lines.enumerated() {
            let trimmed = line.trimmingCharacters(in: .whitespaces)

            if start == nil {
                if trimmed == "api-keys:" {
                    start = index
                }
                continue
            }

            if line.hasPrefix(" ") || line.hasPrefix("\t") || trimmed.isEmpty || trimmed.hasPrefix("#") {
                continue
            }

            return (start: start!, end: index)
        }

        if let start {
            return (start: start, end: lines.count)
        }

        return nil
    }

    private static func parseKeyLine(_ line: String) -> (key: String, note: String, enabled: Bool)? {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        let isCommentedOut = trimmed.hasPrefix("#")
        let dashStripped = isCommentedOut ? 
            String(trimmed.dropFirst().trimmingCharacters(in: .whitespaces).dropFirst().trimmingCharacters(in: .whitespaces)) : 
            String(trimmed.dropFirst().trimmingCharacters(in: .whitespaces))
            
        if dashStripped.isEmpty {
            return nil
        }

        let parts = dashStripped.components(separatedBy: "#")
        let rawValue = parts[0].trimmingCharacters(in: .whitespaces)
        let unquoted = rawValue.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        
        let trailingComment = parts.count > 1 ? parts[1...].joined(separator: "#").trimmingCharacters(in: .whitespaces) : ""
        let explicitDisabled = trailingComment.lowercased().contains("disabled") || isCommentedOut
        let note = trailingComment.replacingOccurrences(of: "disabled", with: "", options: .caseInsensitive).trimmingCharacters(in: .whitespaces)
        
        return unquoted.isEmpty ? nil : (key: unquoted, note: note, enabled: !explicitDisabled)
    }

    private static func mask(_ value: String) -> String {
        if value.count <= 14 {
            return value
        }
        return "\(value.prefix(7))...\(value.suffix(4))"
    }
}
