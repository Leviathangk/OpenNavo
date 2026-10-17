import CommerceKit
import StoreFoundation
import Foundation
import Darwin

signal(SIGPIPE, SIG_IGN)
let args = Array(CommandLine.arguments.dropFirst())
if args == ["probe", "--real-read-only"] {
    // Merely resolve classes and methods. Do not instantiate the download queue,
    // enumerate accounts or ask CommerceKit to perform any action.
    let supported = CKPurchaseController.responds(to: NSSelectorFromString("sharedPurchaseController")) &&
        CKDownloadQueue.responds(to: NSSelectorFromString("sharedDownloadQueue")) &&
        SSPurchase.responds(to: NSSelectorFromString("purchaseWithBuyParameters:"))
    emit(["engine": supported ? "ok" : "unsupported", "readOnly": true,
          "uid": getuid(), "os": ProcessInfo.processInfo.operatingSystemVersionString])
    exit(supported ? 0 : 1)
}

do {
    guard getuid() != 0, geteuid() == getuid() else {
        throw StoreFailure(message: "opennavo-store must run as the logged-in user, never root")
    }
    let request = try Request(args)
    try validateStaging(request.staging)
    // Detect a parent that already exited BEFORE touching CommerceKit. A read
    // source's first callback is asynchronous and can otherwise lose this race.
    var descriptor = pollfd(fd: STDIN_FILENO, events: Int16(POLLIN | POLLHUP), revents: 0)
    if poll(&descriptor, 1, 0) > 0 {
        var byte: UInt8 = 0
        if read(STDIN_FILENO, &byte, 1) <= 0 {
            throw StoreFailure(message: "Canceled: stdin already closed")
        }
    }
    var remaining = request.ids
    var active: Download?
    var failed = false
    var canceled = false
    func next() {
        guard !canceled else { exit(1) }
        guard !remaining.isEmpty else { exit(failed ? 1 : 0) }
        let id = remaining.removeFirst()
        active = Download(id: id, staging: request.staging) { success in
            failed = failed || !success
            DispatchQueue.main.async { next() }
        }
        active?.start(update: request.action == "update")
    }
    let input = DispatchSource.makeReadSource(fileDescriptor: STDIN_FILENO, queue: .main)
    input.setEventHandler {
        var bytes = [UInt8](repeating: 0, count: 1024)
        if read(STDIN_FILENO, &bytes, bytes.count) <= 0 {
            canceled = true
            input.cancel()
            active?.cancel()
            exit(1)
        }
    }
    input.resume()
    DispatchQueue.main.async { next() }
    dispatchMain()
} catch {
    emitError(error)
    exit(1)
}
