// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "OpenNavoStore",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "opennavo-store", targets: ["Store"])],
    targets: [
        .target(name: "PrivateFrameworks"),
        .executableTarget(name: "Store", dependencies: ["PrivateFrameworks"],
            linkerSettings: [.unsafeFlags(["-F", "/System/Library/PrivateFrameworks"])]),
        .testTarget(name: "StoreTests", dependencies: ["Store"]),
    ],
    // The private framework headers have no Swift concurrency annotations.
    // Keep the ObjC boundary in Swift 5 mode and serialize callbacks on main.
    swiftLanguageModes: [.v5]
)
