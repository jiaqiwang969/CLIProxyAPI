import Foundation
import Testing
@testable import CLIProxyMenuBarApp

@Test func keysListHeightGrowsThenClamps() {
    #expect(MenuBarLayout.keysListHeight(for: 0) == 0)
    #expect(MenuBarLayout.keysListHeight(for: 1) == 112)
    #expect(MenuBarLayout.keysListHeight(for: 3) == 324)
    #expect(MenuBarLayout.keysListHeight(for: 10) == 360)
}

@Test func keysGroupHeightTracksProviderAndAccountChromeThenClamps() {
    let groups = [
        ProviderKeyGroup(
            provider: "auggie",
            accounts: [
                AccountKeyGroup(
                    id: "auggie-main",
                    provider: "auggie",
                    authID: "auggie-main",
                    title: "Auggie Main",
                    subtitle: "main@auggie.test",
                    statusText: "active",
                    disabled: false,
                    unavailable: false,
                    modelCount: 2,
                    keys: [
                        APIKeyEntry(
                            id: "sk-1",
                            masked: "sk-1",
                            note: "",
                            enabled: true,
                            createdAt: nil,
                            provider: "auggie",
                            authID: "auggie-main",
                            accountLabel: "Auggie Main",
                            accountDetail: nil,
                            modelIDs: ["gpt-5-4"]
                        )
                    ]
                )
            ]
        )
    ]

    #expect(MenuBarLayout.keysGroupHeight(for: []) == 0)
    #expect(MenuBarLayout.keysGroupHeight(for: groups) == 190)

    let large = Array(repeating: groups[0], count: 5)
    #expect(MenuBarLayout.keysGroupHeight(for: large) == 420)
}

@Test func serviceLogHeightGrowsThenClamps() {
    #expect(MenuBarLayout.serviceLogHeight(for: 0) == 0)
    #expect(MenuBarLayout.serviceLogHeight(for: 1) == 72)
    #expect(MenuBarLayout.serviceLogHeight(for: 4) == 72)
    #expect(MenuBarLayout.serviceLogHeight(for: 20) == 180)
}

@Test func serviceAccountGroupsHeightTracksProviderAndAccountRowsThenClamps() {
    let groups = [
        ServiceProviderGroup(
            provider: "auggie",
            accounts: [
                ServiceAccountGroup(
                    id: "auggie-main",
                    provider: "auggie",
                    authID: "auggie-main",
                    title: "Auggie Main",
                    subtitle: "main@auggie.test",
                    statusText: "active",
                    disabled: false,
                    unavailable: false,
                    modelCount: 2,
                    boundKeyCount: 1
                )
            ]
        )
    ]

    #expect(MenuBarLayout.serviceAccountGroupsHeight(for: []) == 0)
    #expect(MenuBarLayout.serviceAccountGroupsHeight(for: groups) == 102)

    let large = Array(repeating: groups[0], count: 5)
    #expect(MenuBarLayout.serviceAccountGroupsHeight(for: large) == 280)
}

@Test func usageListHeightTracksRenderedRowsThenClamps() {
    #expect(MenuBarLayout.usageListHeight(for: []) == 0)

    let groups = [
        UsageProviderGroup(
            provider: "auggie",
            accounts: [
                UsageAccountGroup(
                    id: "auggie-main",
                    provider: "auggie",
                    authID: "auggie-main",
                    title: "Auggie Main",
                    subtitle: "d14.api.augmentcode.com",
                    totalRequests: 2,
                    totalTokens: 341,
                    keys: [
                        UsageKeyGroup(
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
            ]
        )
    ]

    #expect(MenuBarLayout.usageListHeight(for: groups) == 184)

    let large = Array(repeating: groups[0], count: 4)
    #expect(MenuBarLayout.usageListHeight(for: large) == 360)
}
