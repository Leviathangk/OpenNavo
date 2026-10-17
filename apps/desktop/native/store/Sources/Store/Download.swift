// Adapted from mas v7.0.0 AppStoreAction+download.swift, MIT.
// Copyright (c) 2015 Andrew Naylor, Ross Goldberg. See ../../LICENSE.mas.
// OpenNavo changes: user-only process; JSONL; explicit staging; no installer,
// sudo, receipt copying or registration. All callbacks serialize on main.
import CommerceKit
import StoreFoundation
import Foundation

final class Download: NSObject, CKDownloadQueueObserver {
    let id: UInt64
    let staging: URL
    var observer: String?
    var pkg: URL?
    var receipt: URL?
    var terminal = false
    let finish: (Bool) -> Void

    init(id: UInt64, staging: URL, finish: @escaping (Bool) -> Void) {
        self.id = id
        self.staging = staging
        self.finish = finish
    }

    func start(update: Bool) {
        // Authorization/purchase always occurs in this logged-in user process.
        // Never emit or persist Apple account identifiers.
        ISServiceProxy.genericShared().accountService.primaryAccount { [self] account in
            DispatchQueue.main.async {
                guard !self.terminal else { return }
                guard account != nil else {
                    self.fail(StoreFailure(message: "Sign in to the App Store", code: "E_MAS_NOT_SIGNED_IN"))
                    return
                }
                self.purchase(update: update)
            }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 30) { [self] in
            if !self.terminal && self.observer == nil { self.fail(StoreFailure(message: "Account service timed out")) }
        }
    }

    private func purchase(update: Bool) {
        observer = CKDownloadQueue.shared().add(self)
        emit(["event": "phase", "adamId": id, "phase": "purchasing", "percent": 0])
        let purchase = SSPurchase(buyParameters:
            "productType=C&price=0&pg=default&appExtVrsId=0&pricingParameters=STDRDL&salableAdamId=\(id)")
        purchase.isRedownload = true
        purchase.isUpdate = update
        purchase.itemIdentifier = id
        let metadata = SSDownloadMetadata(kind: "software")
        metadata.itemIdentifier = id
        purchase.downloadMetadata = metadata
        CKPurchaseController.shared().perform(purchase, withOptions: 0) { [self] _, _, error, response in
            DispatchQueue.main.async {
                if let error { self.fail(error) }
                else if response?.downloads?.isEmpty != false {
                    self.fail(StoreFailure(message: "No downloads initiated"))
                }
            }
        }
    }

    func cancel() {
        guard !terminal else { return }
        CKPurchaseController.shared().cancelPurchase(withProductID: NSNumber(value: id))
        if let download = CKDownloadQueue.shared().download(forItemIdentifier: id) {
            CKDownloadQueue.shared().cancelDownload(download, promptToConfirm: false, askToDelete: false)
        }
        fail(StoreFailure(message: "Canceled: parent closed stdin", code: "E_INTERRUPTED"))
    }

    func end(_ success: Bool) {
        guard !terminal else { return }
        terminal = true
        if let observer { CKDownloadQueue.shared().removeObserver(observer) }
        finish(success)
    }

    func fail(_ error: Error) {
        guard !terminal else { return }
        // A staging/protocol failure must not leave a download running after
        // the parent has stopped observing it. Touch only this requested ID.
        if let download = CKDownloadQueue.shared().download(forItemIdentifier: id),
           download.status?.isCancelled != true, download.status?.isFailed != true {
            CKDownloadQueue.shared().cancelDownload(download, promptToConfirm: false, askToDelete: false)
        }
        emitError(error, id: id)
        end(false)
    }

    func retainFiles() throws {
        let folder = URL(fileURLWithPath: "\(CKDownloadDirectory(nil))/\(id)", isDirectory: true)
        guard FileManager.default.fileExists(atPath: folder.path) else { return }
        let files = try FileManager.default.contentsOfDirectory(at: folder,
            includingPropertiesForKeys: [.contentModificationDateKey])
        let newest = try files.filter { $0.pathExtension == "pkg" }.sorted {
            try ($0.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate ?? .distantPast) >
                ($1.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate ?? .distantPast)
        }.first
        for (source, existing, isPackage) in [(newest, pkg, true),
                (files.first { $0.lastPathComponent == "receipt" }, receipt, false)] {
            guard let source else { continue }
            if let existing {
                let a = try FileManager.default.attributesOfItem(atPath: source.path)
                let b = try FileManager.default.attributesOfItem(atPath: existing.path)
                if (a[.systemFileNumber] as? NSNumber) == (b[.systemFileNumber] as? NSNumber) { continue }
            }
            let kept = try retainArtifact(source, in: staging, id: id)
            if isPackage { pkg = kept } else { receipt = kept }
        }
    }

    func handle(_ download: SSDownload, removed: Bool) {
        guard !terminal, download.metadata?.itemIdentifier == id, let status = download.status else { return }
        do { try retainFiles() } catch { fail(error); return }
        if removed {
            if let error = status.error as NSError? {
                if error.domain == "PKInstallErrorDomain", error.code == 201,
                   let pkg, let receipt, let appPath = download.installPath, appPath.hasSuffix(".app") {
                    emit(["event": "needsPrivilegedInstall", "adamId": id, "pkg": pkg.path,
                          "receipt": receipt.path, "appPath": appPath])
                    end(true)
                } else { fail(error) }
            } else if status.isFailed || status.isCancelled {
                fail(StoreFailure(message: "Download failed or canceled"))
            } else {
                emit(["event": "phase", "adamId": id, "phase": "done", "percent": 100])
                end(true)
            }
            return
        }
        if status.isCancelled || status.isFailed { return }
        let phase: String
        switch status.activePhase?.phaseType {
        case 0: phase = "downloading"
        case 1: phase = "installing"
        case 5: phase = "downloaded"
        default: phase = "purchasing"
        }
        let percent = status.phasePercentComplete.isFinite ? max(0, min(100, status.phasePercentComplete * 100)) : 0
        emit(["event": "phase", "adamId": id, "phase": phase, "percent": percent])
    }

    func downloadQueue(_ queue: CKDownloadQueue, changedWithAddition download: SSDownload) {
        DispatchQueue.main.async { self.handle(download, removed: false) }
    }
    func downloadQueue(_ queue: CKDownloadQueue, statusChangedFor download: SSDownload) {
        DispatchQueue.main.async { self.handle(download, removed: false) }
    }
    func downloadQueue(_ queue: CKDownloadQueue, changedWithRemoval download: SSDownload) {
        DispatchQueue.main.async { self.handle(download, removed: true) }
    }
}
