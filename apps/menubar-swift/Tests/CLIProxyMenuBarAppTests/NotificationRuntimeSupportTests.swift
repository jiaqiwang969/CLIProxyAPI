import Foundation
import Testing
@testable import CLIProxyMenuBarApp

@Test func notificationsRequireAppBundle() {
    let debugBinaryURL = URL(fileURLWithPath: "/tmp/CLIProxyMenuBar")
    #expect(NotificationRuntimeSupport.canUseUserNotifications(bundleURL: debugBinaryURL) == false)
}

@Test func notificationsAllowAppBundle() {
    let appBundleURL = URL(fileURLWithPath: "/Applications/CLIProxyMenuBar.app")
    #expect(NotificationRuntimeSupport.canUseUserNotifications(bundleURL: appBundleURL) == true)
}
