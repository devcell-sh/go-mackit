// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "mackit-vz",
    platforms: [.macOS(.v14)],
    dependencies: [
        .package(url: "https://github.com/mhdhejazi/Dynamic.git", from: "1.2.0"),
    ],
    targets: [
        .target(
            name: "ObjCExceptionCatch",
            path: "Sources/ObjCExceptionCatch",
            publicHeadersPath: "include"
        ),
        .executableTarget(
            name: "mackit-vz",
            dependencies: ["Dynamic", "ObjCExceptionCatch"],
            path: "Sources/mackit-vz",
            linkerSettings: [
                .linkedFramework("Virtualization"),
            ]
        ),
    ]
)
