import Foundation

enum NotificationRuntimeSupport {
    static func canUseUserNotifications(bundleURL: URL = Bundle.main.bundleURL) -> Bool {
        bundleURL.pathExtension.caseInsensitiveCompare("app") == .orderedSame
    }
}
