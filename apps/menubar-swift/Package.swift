// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "CLIProxyMenuBar",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "CLIProxyMenuBar", targets: ["CLIProxyMenuBarApp"])
    ],
    targets: [
        .executableTarget(
            name: "CLIProxyMenuBarApp",
            path: "Sources/CLIProxyMenuBarApp"
        ),
        .testTarget(
            name: "CLIProxyMenuBarAppTests",
            dependencies: ["CLIProxyMenuBarApp"],
            path: "Tests/CLIProxyMenuBarAppTests"
        )
    ]
)
