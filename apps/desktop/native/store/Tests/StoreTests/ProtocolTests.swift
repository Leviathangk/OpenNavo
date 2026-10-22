import Foundation
import XCTest
import StoreKit
@testable import Store

final class ProtocolTests: XCTestCase {
    func testTypedErrorsKeepUnknownPrivateCodes() {
        XCTAssertEqual(failureCode(StoreFailure(message: "sign in", code: "E_MAS_NOT_SIGNED_IN")), "E_MAS_NOT_SIGNED_IN")
        XCTAssertEqual(failureCode(NSError(domain: SKErrorDomain, code: SKError.Code.paymentCancelled.rawValue)), "E_INTERRUPTED")
        XCTAssertEqual(failureCode(NSError(domain: "wrapper", code: 1, userInfo: [NSUnderlyingErrorKey: NSError(domain: SKErrorDomain, code: SKError.Code.storeProductNotAvailable.rawValue)])), "E_MAS_REGION")
        XCTAssertEqual(failureCode(NSError(domain: "unverified-private-domain", code: 201)), "E_MAS_ENGINE")
    }

    func testInstallPathSurvivesTheRemovalCallbackThatClearsIt() {
        var path = ObservedInstallPath()
        path.observe(nil)
        XCTAssertNil(path.value)
        // Failed-status callbacks carry the path; the removal callback reports nil.
        path.observe("/Applications/Fixture.app")
        path.observe("/Applications/Fixture.app")
        path.observe(nil)
        XCTAssertEqual(path.value, "/Applications/Fixture.app")
        for ignored in ["", "Fixture.app", "/Applications/Fixture", "/Applications/Folder"] {
            path.observe(ignored)
            XCTAssertEqual(path.value, "/Applications/Fixture.app", ignored)
        }
        path.observe("/Users/example/Applications/Moved.app")
        XCTAssertEqual(path.value, "/Users/example/Applications/Moved.app")
    }

    func testRequestRejectsInvalidIdsAndCommands() throws {
        for ids in ["", "0", "-1", "1,", "1,1", "x", "9007199254740992"] {
            XCTAssertThrowsError(try Request(["install", "--adam-ids", ids, "--staging", "/tmp/test"]))
        }
        XCTAssertThrowsError(try Request(["uninstall", "--adam-ids", "1", "--staging", "/tmp/test"]))
        XCTAssertEqual(try Request(["update", "--adam-ids", "1,2", "--staging", "/tmp/test"]).ids, [1, 2])
    }

    func testStagingAndHardlinksUseTemporaryFilesOnly() throws {
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appendingPathComponent(UUID().uuidString).resolvingSymlinksInPath()
        try fm.createDirectory(at: root, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        defer { try? fm.removeItem(at: root) }
        try validateStaging(root)
        let file = root.appendingPathComponent("source.pkg")
        try Data("fixture".utf8).write(to: file)
        let retained = try retainArtifact(file, in: root, id: 42)
        try fm.removeItem(at: file)
        XCTAssertEqual(try Data(contentsOf: retained), Data("fixture".utf8))
        let symlink = root.appendingPathComponent("link.pkg")
        try fm.createSymbolicLink(at: symlink, withDestinationURL: retained)
        XCTAssertThrowsError(try retainArtifact(symlink, in: root, id: 42))
        XCTAssertThrowsError(try retainArtifact(root, in: root, id: 42))
        try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: root.path)
        XCTAssertThrowsError(try validateStaging(root))
    }
}
