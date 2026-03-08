import Foundation
import AppKit
import Testing
@testable import CLIProxyMenuBarApp

private actor StubAPIClient: CLIProxyAPIManaging {
    var usageSummary = UsageSummary(totalRequests: 0, totalTokens: 0, keyUsages: [])
    var authTargets: [AuthTarget] = []
    var clientKeys: [ManagedClientAPIKey] = []
    var clientKeysError: Error?
    private(set) var savedKeySnapshots: [[ManagedClientAPIKey]] = []

    func fetchUsageSummary(baseURL: String, managementKey: String) async throws -> UsageSummary {
        usageSummary
    }

    func fetchClientAPIKeys(baseURL: String, managementKey: String) async throws -> [ManagedClientAPIKey] {
        if let clientKeysError {
            throw clientKeysError
        }
        return clientKeys
    }

    func saveClientAPIKeys(
        baseURL: String,
        managementKey: String,
        keys: [ManagedClientAPIKey]
    ) async throws {
        clientKeys = keys
        savedKeySnapshots.append(keys)
    }

    func fetchAuthTargets(baseURL: String, managementKey: String) async throws -> [AuthTarget] {
        authTargets
    }

    func seed(authTargets: [AuthTarget], clientKeys: [ManagedClientAPIKey]) {
        self.authTargets = authTargets
        self.clientKeys = clientKeys
    }

    func setClientKeysError(_ error: Error?) {
        self.clientKeysError = error
    }

    func setUsageSummary(_ summary: UsageSummary) {
        self.usageSummary = summary
    }

    func latestSavedKeys() -> [ManagedClientAPIKey]? {
        savedKeySnapshots.last
    }
}

private final class FakeClipboard: ClipboardWriting {
    var copiedString: String?

    func clearContents() {}

    @discardableResult
    func setString(_ string: String, forType type: NSPasteboard.PasteboardType) -> Bool {
        copiedString = string
        return true
    }
}

@MainActor
@Test func refreshNowLoadsAuthTargetsAndScopedKeyBindings() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "auggie-main",
                provider: "auggie",
                name: "auggie-main.json",
                label: "Auggie Main",
                email: "main@auggie.test",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gpt-5-4", displayName: "GPT-5.4"),
                    AuthModelInfo(id: "claude-sonnet-4-6", displayName: "Claude Sonnet 4.6")
                ]
            )
        ],
        clientKeys: [
            ManagedClientAPIKey(
                key: "sk-bound",
                enabled: true,
                note: "primary",
                scope: ClientAPIKeyScope(
                    provider: "auggie",
                    authID: "auggie-main",
                    models: []
                )
            )
        ]
    )

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()

    #expect(viewModel.authTargets.count == 1)
    #expect(viewModel.selectedProvider == "auggie")
    #expect(viewModel.selectedAuthID == "auggie-main")
    #expect(viewModel.apiKeys.count == 1)
    #expect(viewModel.apiKeys[0].provider == "auggie")
    #expect(viewModel.apiKeys[0].authID == "auggie-main")
    #expect(viewModel.apiKeys[0].accountLabel == "Auggie Main")
    #expect(viewModel.apiKeys[0].modelIDs == ["gpt-5-4", "claude-sonnet-4-6"])
}

@MainActor
@Test func addManualKeySavesScopedBindingForSelectedAuth() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "antigravity-main",
                provider: "antigravity",
                name: "antigravity-main.json",
                label: "Antigravity Main",
                email: nil,
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gemini-3.1-pro-high", displayName: "Gemini 3.1 Pro High")
                ]
            )
        ],
        clientKeys: []
    )

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()
    viewModel.newKeyInput = "  sk-new-bound  "
    viewModel.newKeyNoteInput = "gemini"

    viewModel.addManualKey()
    for _ in 0 ..< 20 {
        await Task.yield()
    }

    let savedKeys = try #require(await client.latestSavedKeys())
    #expect(savedKeys.count == 1)
    #expect(savedKeys[0].key == "sk-new-bound")
    #expect(savedKeys[0].note == "gemini")
    #expect(savedKeys[0].enabled == true)
    #expect(savedKeys[0].scope?.provider == "antigravity")
    #expect(savedKeys[0].scope?.authID == "antigravity-main")
    #expect(viewModel.newKeyInput.isEmpty)
    #expect(viewModel.newKeyNoteInput.isEmpty)
}

@MainActor
@Test func refreshNowKeepsAuthTargetsVisibleWhenClientKeyEndpointIsUnavailable() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "auggie-main",
                provider: "auggie",
                name: "auggie-main.json",
                label: "Auggie Main",
                email: "main@auggie.test",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: []
            ),
            AuthTarget(
                id: "antigravity-main",
                provider: "antigravity",
                name: "antigravity-main.json",
                label: "Antigravity Main",
                email: nil,
                account: nil,
                accountType: nil,
                status: "disabled",
                statusMessage: "expired",
                disabled: true,
                unavailable: false,
                models: []
            )
        ],
        clientKeys: []
    )
    await client.setClientKeysError(APIClientError.httpError(statusCode: 404, body: "404 page not found"))

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()

    #expect(viewModel.authTargets.count == 2)
    #expect(viewModel.availableProviders == ["auggie"])
    #expect(viewModel.selectedProvider == "auggie")
    #expect(viewModel.clientKeyManagementAvailable == false)
}

@MainActor
@Test func copyKeyWritesRawKeyToClipboard() async {
    let clipboard = FakeClipboard()
    let viewModel = UsageMonitorViewModel(clipboard: clipboard, autoRefresh: false)

    viewModel.copyKey("sk-test-copy")

    #expect(clipboard.copiedString == "sk-test-copy")
    #expect(viewModel.actionMessage == "已复制 Key")
}

@MainActor
@Test func providerKeyGroupsShowAccountScopedBindingsAndUnboundKeysSeparately() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "antigravity-main",
                provider: "antigravity",
                name: "antigravity-main.json",
                label: "Antigravity Main",
                email: "gemini@test.dev",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gemini-3.1-pro-high", displayName: "Gemini 3.1 Pro High")
                ]
            ),
            AuthTarget(
                id: "auggie-main",
                provider: "auggie",
                name: "auggie-main.json",
                label: "Auggie Main",
                email: "main@auggie.test",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gpt-5-4", displayName: "GPT-5.4"),
                    AuthModelInfo(id: "claude-sonnet-4-6", displayName: "Claude Sonnet 4.6")
                ]
            )
        ],
        clientKeys: [
            ManagedClientAPIKey(
                key: "sk-antigravity",
                enabled: true,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "antigravity",
                    authID: "antigravity-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-auggie",
                enabled: true,
                note: "primary",
                scope: ClientAPIKeyScope(
                    provider: "auggie",
                    authID: "auggie-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-legacy",
                enabled: false,
                note: "legacy",
                scope: nil
            )
        ]
    )

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()

    #expect(viewModel.providerKeyGroups.map(\.provider) == ["antigravity", "auggie", "未绑定"])

    let antigravityGroup = try #require(viewModel.providerKeyGroups.first { $0.provider == "antigravity" })
    #expect(antigravityGroup.totalKeys == 1)
    #expect(antigravityGroup.accounts.count == 1)
    #expect(antigravityGroup.accounts[0].title == "Antigravity Main")
    #expect(antigravityGroup.accounts[0].keys.map(\.id) == ["sk-antigravity"])

    let auggieGroup = try #require(viewModel.providerKeyGroups.first { $0.provider == "auggie" })
    #expect(auggieGroup.totalKeys == 1)
    #expect(auggieGroup.accounts[0].title == "Auggie Main")
    #expect(auggieGroup.accounts[0].keys[0].note == "primary")

    let unboundGroup = try #require(viewModel.providerKeyGroups.first { $0.provider == "未绑定" })
    #expect(unboundGroup.totalKeys == 1)
    #expect(unboundGroup.accounts.count == 1)
    #expect(unboundGroup.accounts[0].title == "未绑定 Key")
    #expect(unboundGroup.accounts[0].keys.map(\.id) == ["sk-legacy"])
}

@MainActor
@Test func serviceProviderGroupsShowAccountStatusAndBoundKeyCounts() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "antigravity-main",
                provider: "antigravity",
                name: "antigravity-main.json",
                label: "Antigravity Main",
                email: "gemini@test.dev",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gemini-3.1-pro-high", displayName: "Gemini 3.1 Pro High")
                ]
            ),
            AuthTarget(
                id: "auggie-main",
                provider: "auggie",
                name: "auggie-main.json",
                label: "Auggie Main",
                email: "main@auggie.test",
                account: nil,
                accountType: nil,
                status: "expired",
                statusMessage: nil,
                disabled: true,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gpt-5-4", displayName: "GPT-5.4"),
                    AuthModelInfo(id: "claude-sonnet-4-6", displayName: "Claude Sonnet 4.6")
                ]
            )
        ],
        clientKeys: [
            ManagedClientAPIKey(
                key: "sk-antigravity-1",
                enabled: true,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "antigravity",
                    authID: "antigravity-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-antigravity-2",
                enabled: false,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "antigravity",
                    authID: "antigravity-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-auggie-1",
                enabled: true,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "auggie",
                    authID: "auggie-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-unbound",
                enabled: true,
                note: "",
                scope: nil
            )
        ]
    )

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()

    #expect(viewModel.serviceProviderGroups.map(\.provider) == ["antigravity", "auggie"])

    let antigravityGroup = try #require(viewModel.serviceProviderGroups.first { $0.provider == "antigravity" })
    #expect(antigravityGroup.totalKeys == 2)
    #expect(antigravityGroup.accounts.count == 1)
    #expect(antigravityGroup.accounts[0].title == "Antigravity Main")
    #expect(antigravityGroup.accounts[0].statusText == "active")
    #expect(antigravityGroup.accounts[0].modelCount == 1)
    #expect(antigravityGroup.accounts[0].boundKeyCount == 2)

    let auggieGroup = try #require(viewModel.serviceProviderGroups.first { $0.provider == "auggie" })
    #expect(auggieGroup.totalKeys == 1)
    #expect(auggieGroup.accounts.count == 1)
    #expect(auggieGroup.accounts[0].title == "Auggie Main")
    #expect(auggieGroup.accounts[0].statusText == "expired")
    #expect(auggieGroup.accounts[0].disabled == true)
    #expect(auggieGroup.accounts[0].boundKeyCount == 1)
}

@MainActor
@Test func usageProviderGroupsSplitContributionByProviderAccountKeyAndModel() async throws {
    let client = StubAPIClient()
    await client.seed(
        authTargets: [
            AuthTarget(
                id: "antigravity-main",
                provider: "antigravity",
                name: "antigravity-main.json",
                label: "Antigravity Main",
                email: "gemini@test.dev",
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "gemini-3.1-pro-high", displayName: "Gemini 3.1 Pro High")
                ]
            ),
            AuthTarget(
                id: "auggie-main",
                provider: "auggie",
                name: "auggie-main.json",
                label: "Auggie Main",
                email: nil,
                account: nil,
                accountType: nil,
                status: "active",
                statusMessage: nil,
                disabled: false,
                unavailable: false,
                models: [
                    AuthModelInfo(id: "claude-sonnet-4-6", displayName: "Claude Sonnet 4.6")
                ]
            )
        ],
        clientKeys: [
            ManagedClientAPIKey(
                key: "sk-antigravity",
                enabled: true,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "antigravity",
                    authID: "antigravity-main",
                    models: []
                )
            ),
            ManagedClientAPIKey(
                key: "sk-auggie",
                enabled: true,
                note: "",
                scope: ClientAPIKeyScope(
                    provider: "auggie",
                    authID: "auggie-main",
                    models: []
                )
            )
        ]
    )
    await client.setClientKeysError(nil)
    await client.setUsageSummary(
        UsageSummary(
            totalRequests: 3,
            totalTokens: 999,
            keyUsages: [
                APIKeyUsage(
                    id: "sk-antigravity",
                    label: "sk-antig...ity",
                    totalRequests: 1,
                    totalTokens: 658,
                    modelCalls: [
                        ModelCallCount(id: "gemini-3.1-pro-high", requests: 1, totalTokens: 658)
                    ]
                ),
                APIKeyUsage(
                    id: "sk-auggie",
                    label: "sk-auggi...ggie",
                    totalRequests: 2,
                    totalTokens: 341,
                    modelCalls: [
                        ModelCallCount(id: "claude-sonnet-4-6", requests: 1, totalTokens: 341),
                        ModelCallCount(id: "gpt-5-4", requests: 1, totalTokens: 0)
                    ]
                )
            ]
        )
    )

    let viewModel = UsageMonitorViewModel(
        client: client,
        clipboard: FakeClipboard(),
        autoRefresh: false,
        runtimeConfigProvider: {
            RuntimeConfig(
                baseURL: "http://localhost:8317",
                managementKey: "cliproxy-menubar-dev",
                configPath: nil
            )
        },
        serviceStatusProvider: { _ in .unknown }
    )

    await viewModel.refreshNow()

    #expect(viewModel.usageProviderGroups.map(\.provider) == ["antigravity", "auggie"])

    let antigravityGroup = try #require(viewModel.usageProviderGroups.first { $0.provider == "antigravity" })
    #expect(antigravityGroup.totalRequests == 1)
    #expect(antigravityGroup.totalTokens == 658)
    #expect(antigravityGroup.accounts[0].title == "Antigravity Main")
    #expect(antigravityGroup.accounts[0].keys[0].id == "sk-antigravity")
    #expect(antigravityGroup.accounts[0].keys[0].modelCalls[0].id == "gemini-3.1-pro-high")

    let auggieGroup = try #require(viewModel.usageProviderGroups.first { $0.provider == "auggie" })
    #expect(auggieGroup.totalRequests == 2)
    #expect(auggieGroup.totalTokens == 341)
    #expect(auggieGroup.accounts[0].title == "Auggie Main")
    #expect(auggieGroup.accounts[0].keys[0].modelCalls.count == 2)
    #expect(auggieGroup.accounts[0].keys[0].modelCalls[0].id == "claude-sonnet-4-6")
}
