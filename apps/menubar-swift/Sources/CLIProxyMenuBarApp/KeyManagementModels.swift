import Foundation

protocol CLIProxyAPIManaging: Sendable {
    func fetchUsageSummary(baseURL: String, managementKey: String) async throws -> UsageSummary
    func fetchClientAPIKeys(baseURL: String, managementKey: String) async throws -> [ManagedClientAPIKey]
    func saveClientAPIKeys(baseURL: String, managementKey: String, keys: [ManagedClientAPIKey]) async throws
    func fetchAuthTargets(baseURL: String, managementKey: String) async throws -> [AuthTarget]
}

struct ClientAPIKeyScope: Codable, Equatable, Sendable {
    var provider: String
    var authID: String
    var models: [String]

    enum CodingKeys: String, CodingKey {
        case provider
        case authID = "auth_id"
        case models
    }

    init(provider: String, authID: String, models: [String] = []) {
        self.provider = provider
        self.authID = authID
        self.models = models
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        self.authID = try container.decodeIfPresent(String.self, forKey: .authID) ?? ""
        self.models = try container.decodeIfPresent([String].self, forKey: .models) ?? []
    }
}

struct ManagedClientAPIKey: Codable, Equatable, Identifiable, Sendable {
    var key: String
    var enabled: Bool
    var note: String
    var scope: ClientAPIKeyScope?

    var id: String { key }

    enum CodingKeys: String, CodingKey {
        case key
        case enabled
        case note
        case scope
    }

    init(
        key: String,
        enabled: Bool = true,
        note: String = "",
        scope: ClientAPIKeyScope? = nil
    ) {
        self.key = key
        self.enabled = enabled
        self.note = note
        self.scope = scope
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.key = try container.decode(String.self, forKey: .key)
        self.enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
        self.note = try container.decodeIfPresent(String.self, forKey: .note) ?? ""
        self.scope = try container.decodeIfPresent(ClientAPIKeyScope.self, forKey: .scope)
    }
}

struct AccountKeyGroup: Identifiable, Equatable, Sendable {
    let id: String
    let provider: String
    let authID: String?
    let title: String
    let subtitle: String?
    let statusText: String
    let disabled: Bool
    let unavailable: Bool
    let modelCount: Int
    let keys: [APIKeyEntry]

    var totalKeys: Int {
        keys.count
    }
}

struct ServiceAccountGroup: Identifiable, Equatable, Sendable {
    let id: String
    let provider: String
    let authID: String
    let title: String
    let subtitle: String?
    let statusText: String
    let disabled: Bool
    let unavailable: Bool
    let modelCount: Int
    let boundKeyCount: Int
}

struct ServiceProviderGroup: Identifiable, Equatable, Sendable {
    let provider: String
    let accounts: [ServiceAccountGroup]

    var id: String {
        provider
    }

    var totalKeys: Int {
        accounts.reduce(0) { partial, account in
            partial + account.boundKeyCount
        }
    }
}

struct ProviderKeyGroup: Identifiable, Equatable, Sendable {
    let provider: String
    let accounts: [AccountKeyGroup]

    var id: String {
        provider
    }

    var totalKeys: Int {
        accounts.reduce(0) { partial, account in
            partial + account.totalKeys
        }
    }
}

struct UsageKeyGroup: Identifiable, Equatable, Sendable {
    let id: String
    let label: String
    let totalRequests: Int64
    let totalTokens: Int64
    let modelCalls: [ModelCallCount]
}

struct UsageAccountGroup: Identifiable, Equatable, Sendable {
    let id: String
    let provider: String
    let authID: String?
    let title: String
    let subtitle: String?
    let totalRequests: Int64
    let totalTokens: Int64
    let keys: [UsageKeyGroup]
}

struct UsageProviderGroup: Identifiable, Equatable, Sendable {
    let provider: String
    let accounts: [UsageAccountGroup]

    var id: String {
        provider
    }

    var totalRequests: Int64 {
        accounts.reduce(0) { partial, account in
            partial + account.totalRequests
        }
    }

    var totalTokens: Int64 {
        accounts.reduce(0) { partial, account in
            partial + account.totalTokens
        }
    }
}

struct AuthModelInfo: Codable, Equatable, Identifiable, Sendable {
    let id: String
    let displayName: String?

    enum CodingKeys: String, CodingKey {
        case id
        case displayName = "display_name"
    }
}

struct AuthTarget: Codable, Equatable, Identifiable, Sendable {
    let id: String
    let provider: String
    let name: String
    let label: String?
    let email: String?
    let account: String?
    let accountType: String?
    let status: String?
    let statusMessage: String?
    let disabled: Bool
    let unavailable: Bool
    let models: [AuthModelInfo]

    enum CodingKeys: String, CodingKey {
        case id
        case provider
        case name
        case label
        case email
        case account
        case accountType = "account_type"
        case status
        case statusMessage = "status_message"
        case disabled
        case unavailable
        case models
    }

    init(
        id: String,
        provider: String,
        name: String,
        label: String?,
        email: String?,
        account: String?,
        accountType: String?,
        status: String?,
        statusMessage: String?,
        disabled: Bool,
        unavailable: Bool,
        models: [AuthModelInfo]
    ) {
        self.id = id
        self.provider = provider
        self.name = name
        self.label = label
        self.email = email
        self.account = account
        self.accountType = accountType
        self.status = status
        self.statusMessage = statusMessage
        self.disabled = disabled
        self.unavailable = unavailable
        self.models = models
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try container.decode(String.self, forKey: .id)
        self.provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        self.name = try container.decodeIfPresent(String.self, forKey: .name) ?? self.id
        self.label = try container.decodeIfPresent(String.self, forKey: .label)
        self.email = try container.decodeIfPresent(String.self, forKey: .email)
        self.account = try container.decodeIfPresent(String.self, forKey: .account)
        self.accountType = try container.decodeIfPresent(String.self, forKey: .accountType)
        self.status = try container.decodeIfPresent(String.self, forKey: .status)
        self.statusMessage = try container.decodeIfPresent(String.self, forKey: .statusMessage)
        self.disabled = try container.decodeIfPresent(Bool.self, forKey: .disabled) ?? false
        self.unavailable = try container.decodeIfPresent(Bool.self, forKey: .unavailable) ?? false
        self.models = try container.decodeIfPresent([AuthModelInfo].self, forKey: .models) ?? []
    }

    var isSelectable: Bool {
        !disabled && !unavailable
    }

    var displayName: String {
        Self.firstNonEmpty(label, account, email, name, id) ?? id
    }

    var secondaryLabel: String? {
        let primary = displayName
        for candidate in [email, account, name, id] {
            guard let value = candidate?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else {
                continue
            }
            if value != primary {
                return value
            }
        }
        return nil
    }

    var modelIDs: [String] {
        models.map(\.id)
    }

    private static func firstNonEmpty(_ values: String?...) -> String? {
        for value in values {
            guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else {
                continue
            }
            return trimmed
        }
        return nil
    }
}
