import CoreGraphics

enum MenuBarLayout {
    static let panelWidth: CGFloat = 400

    private static let keyRowHeight: CGFloat = 108
    private static let keyMinHeight: CGFloat = 112
    static let keyMaxHeight: CGFloat = 360
    private static let providerHeaderHeight: CGFloat = 24
    private static let accountHeaderHeight: CGFloat = 58
    static let keyGroupMaxHeight: CGFloat = 420

    private static let serviceLogLineHeight: CGFloat = 18
    private static let serviceLogMinHeight: CGFloat = 72
    static let serviceLogMaxHeight: CGFloat = 180
    private static let serviceProviderHeaderHeight: CGFloat = 24
    private static let serviceAccountRowHeight: CGFloat = 78
    static let serviceAccountGroupsMaxHeight: CGFloat = 280

    private static let usageProviderHeaderHeight: CGFloat = 24
    private static let usageAccountHeaderHeight: CGFloat = 56
    private static let usageKeyRowHeight: CGFloat = 44
    private static let usageModelRowHeight: CGFloat = 24
    private static let usagePadding: CGFloat = 12
    private static let usageMinHeight: CGFloat = 96
    static let usageMaxHeight: CGFloat = 360

    static func keysListHeight(for count: Int) -> CGFloat {
        clampedHeight(
            count: count,
            rowHeight: keyRowHeight,
            minHeight: keyMinHeight,
            maxHeight: keyMaxHeight
        )
    }

    static func keysGroupHeight(for groups: [ProviderKeyGroup]) -> CGFloat {
        guard !groups.isEmpty else {
            return 0
        }

        let providerCount = groups.count
        let accountCount = groups.reduce(0) { partial, group in
            partial + group.accounts.count
        }
        let keyCount = groups.reduce(0) { partial, group in
            partial + group.totalKeys
        }

        let proposed =
            (CGFloat(providerCount) * providerHeaderHeight)
            + (CGFloat(accountCount) * accountHeaderHeight)
            + (CGFloat(keyCount) * keyRowHeight)

        return min(proposed, keyGroupMaxHeight)
    }

    static func serviceLogHeight(for count: Int) -> CGFloat {
        clampedHeight(
            count: count,
            rowHeight: serviceLogLineHeight,
            minHeight: serviceLogMinHeight,
            maxHeight: serviceLogMaxHeight
        )
    }

    static func serviceAccountGroupsHeight(for groups: [ServiceProviderGroup]) -> CGFloat {
        guard !groups.isEmpty else {
            return 0
        }

        let providerCount = groups.count
        let accountCount = groups.reduce(0) { partial, group in
            partial + group.accounts.count
        }
        let proposed =
            (CGFloat(providerCount) * serviceProviderHeaderHeight)
            + (CGFloat(accountCount) * serviceAccountRowHeight)

        return min(proposed, serviceAccountGroupsMaxHeight)
    }

    static func usageListHeight(for groups: [UsageProviderGroup]) -> CGFloat {
        guard !groups.isEmpty else {
            return 0
        }

        let providerCount = groups.count
        let accountCount = groups.reduce(0) { partial, group in
            partial + group.accounts.count
        }
        let keyCount = groups.reduce(0) { partial, group in
            partial + group.accounts.reduce(0) { accountPartial, account in
                accountPartial + account.keys.count
            }
        }
        let modelCount = groups.reduce(0) { partial, group in
            partial + group.accounts.reduce(0) { accountPartial, account in
                accountPartial + account.keys.reduce(0) { keyPartial, key in
                    keyPartial + key.modelCalls.count
                }
            }
        }
        let proposed =
            (CGFloat(providerCount) * usageProviderHeaderHeight)
            + (CGFloat(accountCount) * usageAccountHeaderHeight)
            + (CGFloat(keyCount) * usageKeyRowHeight)
            + (CGFloat(modelCount) * usageModelRowHeight)
            + usagePadding

        return min(max(proposed, usageMinHeight), usageMaxHeight)
    }

    private static func clampedHeight(
        count: Int,
        rowHeight: CGFloat,
        minHeight: CGFloat,
        maxHeight: CGFloat
    ) -> CGFloat {
        guard count > 0 else {
            return 0
        }

        let proposed = CGFloat(count) * rowHeight
        return min(max(proposed, minHeight), maxHeight)
    }
}
