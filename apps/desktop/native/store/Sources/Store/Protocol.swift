import Foundation
import StoreKit
import Darwin

struct StoreFailure: Error {
    let message: String
    var code: String = "E_MAS_ENGINE"
}

struct Request {
    let action: String
    let ids: [UInt64]
    let staging: URL

    init(_ args: [String]) throws {
        guard args.count == 5, ["install", "update"].contains(args[0]),
              args[1] == "--adam-ids", args[3] == "--staging" else {
            throw StoreFailure(message: "Expected install|update --adam-ids 1,2 --staging <dir>")
        }
        let tokens = args[2].split(separator: ",", omittingEmptySubsequences: false)
        let values = tokens.compactMap { UInt64($0) }
        guard !values.isEmpty, values.count <= 100, values.count == tokens.count,
              values.allSatisfy({ $0 > 0 && $0 <= 9_007_199_254_740_991 }),
              Set(values).count == values.count else {
            throw StoreFailure(message: "Invalid or duplicate Adam IDs")
        }
        action = args[0]
        ids = values
        staging = URL(fileURLWithPath: args[4], isDirectory: true).standardizedFileURL
    }
}

func validateStaging(_ url: URL) throws {
    var info = stat()
    guard lstat(url.path, &info) == 0,
          info.st_mode & S_IFMT == S_IFDIR, info.st_mode & 0o777 == 0o700,
          info.st_uid == getuid(), url.resolvingSymlinksInPath().path == url.path else {
        throw StoreFailure(message: "Staging must be an existing user-owned 0700 directory without symlinks")
    }
}

func emit(_ object: [String: Any]) {
    guard var data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]) else {
        exit(70)
    }
    data.append(10)
    FileHandle.standardOutput.write(data)
}

/// Classify only documented StoreKit errors. Private CommerceKit codes remain
/// E_MAS_ENGINE with their original domain/code, rather than guessing account state.
func failureCode(_ error: Error) -> String {
    if let failure = error as? StoreFailure { return failure.code }
    var current = error as NSError
    for _ in 0..<8 {
        if current.domain == SKErrorDomain {
            if current.code == SKError.Code.paymentCancelled.rawValue { return "E_INTERRUPTED" }
            if current.code == SKError.Code.storeProductNotAvailable.rawValue { return "E_MAS_REGION" }
        }
        if current.domain == NSURLErrorDomain && current.code == NSURLErrorCancelled { return "E_INTERRUPTED" }
        guard let underlying = current.userInfo[NSUnderlyingErrorKey] as? NSError else { break }
        current = underlying
    }
    return "E_MAS_ENGINE"
}
func emitError(_ error: Error, id: UInt64 = 0) {
    let ns = error as NSError
    emit(["event": "error", "adamId": id, "code": failureCode(error),
          "domain": ns.domain, "errorCode": ns.code,
          "message": (error as? StoreFailure)?.message ?? ns.localizedDescription])
}

/// Link only regular files; use a fresh name so an existing staged inode is
/// never overwritten. The privileged consumer MUST revalidate these objects.
func retainArtifact(_ source: URL, in staging: URL, id: UInt64) throws -> URL {
    var info = stat()
    guard lstat(source.path, &info) == 0, info.st_mode & S_IFMT == S_IFREG else {
        throw StoreFailure(message: "Artifact is not a regular file")
    }
    let dest = staging.appendingPathComponent("\(id)-\(UUID().uuidString)-\(source.lastPathComponent)")
    guard link(source.path, dest.path) == 0 else {
        throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
    }
    var retained = stat()
    guard lstat(dest.path, &retained) == 0, retained.st_ino == info.st_ino,
          retained.st_dev == info.st_dev, retained.st_mode & S_IFMT == S_IFREG else {
        throw StoreFailure(message: "Artifact changed while retaining")
    }
    return dest
}
