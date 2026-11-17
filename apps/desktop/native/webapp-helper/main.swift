import AppKit
import WebKit

private struct Config: Decodable {
    let name: String
    let url: String
    let width: Int?
    let height: Int?
}

/// The hidden title bar leaves no draggable strip: the web view covers the
/// whole content area and swallows the mouse events that would normally move
/// the window. A transparent strip above it restores that behaviour.
private final class TitleBarDragView: NSView {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool {
        true
    }

    override func mouseDown(with event: NSEvent) {
        if event.clickCount == 2 {
            window?.performZoom(nil)
            return
        }
        window?.performDrag(with: event)
    }
}

private final class WebAppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    private let config: Config
    private var window: NSWindow?
    private var webView: WKWebView?
    private var destinations: [ObjectIdentifier: URL] = [:]

    init(config: Config) {
        self.config = config
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let width = CGFloat(config.width ?? 1200)
        let height = CGFloat(config.height ?? 800)
        let frame = NSRect(x: 0, y: 0, width: width, height: height)

        let window = NSWindow(
            contentRect: frame,
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = config.name
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.titlebarSeparatorStyle = .none
        window.isMovableByWindowBackground = false
        window.collectionBehavior.insert(.fullScreenPrimary)
        window.minSize = NSSize(width: 480, height: 360)
        window.center()

        let container = NSView(frame: frame)
        let webView = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        webView.translatesAutoresizingMaskIntoConstraints = false
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.allowsBackForwardNavigationGestures = true
        container.addSubview(webView)
        NSLayoutConstraint.activate([
            webView.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            webView.topAnchor.constraint(equalTo: container.topAnchor),
            webView.bottomAnchor.constraint(equalTo: container.bottomAnchor),
        ])
        let dragView = TitleBarDragView(frame: .zero)
        dragView.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(dragView)
        NSLayoutConstraint.activate([
            dragView.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            dragView.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            dragView.topAnchor.constraint(equalTo: container.topAnchor),
            dragView.heightAnchor.constraint(equalToConstant: 28),
        ])
        window.contentView = container

        self.window = window
        self.webView = webView

        installMenu()
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(webView)
        NSApp.activate(ignoringOtherApps: true)

        if let url = URL(string: config.url) {
            webView.load(URLRequest(url: url))
        } else {
            NSLog("OpenNavo web app has an invalid URL: %@", config.url)
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        NSLog("OpenNavo web app failed: %@", error.localizedDescription)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        NSLog("OpenNavo web app provisional load failed: %@", error.localizedDescription)
    }

    // MARK: - Navigation

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        if navigationAction.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        guard let url = navigationAction.request.url else {
            decisionHandler(.allow)
            return
        }
        if navigationAction.targetFrame == nil {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        if let scheme = url.scheme?.lowercased(),
           !["http", "https", "about", "data", "blob", "file"].contains(scheme) {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void
    ) {
        decisionHandler(navigationResponse.canShowMIMEType ? .allow : .download)
    }

    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        if let url = navigationAction.request.url {
            NSWorkspace.shared.open(url)
        }
        return nil
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    // MARK: - Downloads

    func download(
        _ download: WKDownload,
        decideDestinationUsing response: URLResponse,
        suggestedFilename: String,
        completionHandler: @escaping (URL?) -> Void
    ) {
        guard let downloads = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first else {
            completionHandler(nil)
            return
        }
        let destination = Self.uniqueDestination(in: downloads, filename: suggestedFilename)
        destinations[ObjectIdentifier(download)] = destination
        completionHandler(destination)
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let destination = destinations.removeValue(forKey: ObjectIdentifier(download)) else { return }
        NSWorkspace.shared.activateFileViewerSelecting([destination])
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        destinations.removeValue(forKey: ObjectIdentifier(download))
    }

    private static func uniqueDestination(in directory: URL, filename: String) -> URL {
        let candidate = directory.appendingPathComponent(filename)
        guard FileManager.default.fileExists(atPath: candidate.path) else { return candidate }
        let base = (filename as NSString).deletingPathExtension
        let ext = (filename as NSString).pathExtension
        for index in 2...999 {
            let name = ext.isEmpty ? "\(base) \(index)" : "\(base) \(index).\(ext)"
            let next = directory.appendingPathComponent(name)
            if !FileManager.default.fileExists(atPath: next.path) { return next }
        }
        return candidate
    }

    // MARK: - Menu

    private func installMenu() {
        let mainMenu = NSMenu()

        let appItem = NSMenuItem()
        mainMenu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(
            withTitle: "Quit \(config.name)",
            action: #selector(NSApplication.terminate(_:)),
            keyEquivalent: "q"
        )
        appItem.submenu = appMenu

        let fileItem = NSMenuItem()
        mainMenu.addItem(fileItem)
        let fileMenu = NSMenu(title: "File")
        fileMenu.addItem(
            withTitle: "Close",
            action: #selector(NSWindow.performClose(_:)),
            keyEquivalent: "w"
        )
        fileMenu.addItem(
            withTitle: "Minimize",
            action: #selector(NSWindow.performMiniaturize(_:)),
            keyEquivalent: "m"
        )
        fileItem.submenu = fileMenu

        let editItem = NSMenuItem()
        mainMenu.addItem(editItem)
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(NSMenuItem.separator())
        editMenu.addItem(withTitle: "Cut", action: Selector(("cut:")), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: Selector(("copy:")), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: Selector(("paste:")), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: Selector(("selectAll:")), keyEquivalent: "a")
        editItem.submenu = editMenu

        let viewItem = NSMenuItem()
        mainMenu.addItem(viewItem)
        let viewMenu = NSMenu(title: "View")
        viewMenu.addItem(menuItem("Reload", #selector(reloadPage), "r"))
        viewMenu.addItem(menuItem("Actual Size", #selector(actualSize), "0"))
        viewMenu.addItem(menuItem("Zoom In", #selector(zoomIn), "+"))
        viewMenu.addItem(menuItem("Zoom Out", #selector(zoomOut), "-"))
        viewItem.submenu = viewMenu

        NSApp.mainMenu = mainMenu
    }

    private func menuItem(_ title: String, _ action: Selector, _ key: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.target = self
        return item
    }

    @objc private func reloadPage() {
        webView?.reload()
    }

    @objc private func actualSize() {
        webView?.pageZoom = 1
    }

    @objc private func zoomIn() {
        webView?.pageZoom = min(3, (webView?.pageZoom ?? 1) + 0.1)
    }

    @objc private func zoomOut() {
        webView?.pageZoom = max(0.5, (webView?.pageZoom ?? 1) - 0.1)
    }
}

private func parseHexColor(_ value: String) -> NSColor? {
    var hex = value.trimmingCharacters(in: .whitespacesAndNewlines)
    if hex.hasPrefix("#") { hex.removeFirst() }
    guard hex.count == 6, let number = UInt32(hex, radix: 16) else { return nil }
    return NSColor(
        srgbRed: CGFloat((number >> 16) & 0xff) / 255,
        green: CGFloat((number >> 8) & 0xff) / 255,
        blue: CGFloat(number & 0xff) / 255,
        alpha: 1
    )
}

private func squircle(_ rect: CGRect, exponent: CGFloat = 5) -> NSBezierPath {
    let path = NSBezierPath()
    let steps = 720
    for index in 0...steps {
        let angle = CGFloat(index) / CGFloat(steps) * 2 * .pi
        let cosine = cos(angle)
        let sine = sin(angle)
        let x = rect.midX + rect.width / 2 * copysign(pow(abs(cosine), 2 / exponent), cosine)
        let y = rect.midY + rect.height / 2 * copysign(pow(abs(sine), 2 / exponent), sine)
        if index == 0 {
            path.move(to: NSPoint(x: x, y: y))
        } else {
            path.line(to: NSPoint(x: x, y: y))
        }
    }
    path.close()
    return path
}

private func renderIcon(
    source: NSImage?,
    mode: String,
    background: NSColor,
    letter: String?,
    base: NSColor?,
    output: String
) {
    let size = 1024
    guard let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: size,
        pixelsHigh: size,
        bitsPerSample: 8,
        samplesPerPixel: 4,
        hasAlpha: true,
        isPlanar: false,
        colorSpaceName: .deviceRGB,
        bytesPerRow: 0,
        bitsPerPixel: 0
    ) else { exit(2) }
    bitmap.size = NSSize(width: size, height: size)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    guard let context = NSGraphicsContext.current else { exit(2) }
    let cg = context.cgContext
    let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
    let path = squircle(tile)

    // The shadow belongs to the tile, so fill the tile before drawing into it.
    cg.saveGState()
    cg.setShadow(
        offset: CGSize(width: 0, height: -10),
        blur: 30,
        color: NSColor.black.withAlphaComponent(0.28).cgColor
    )
    background.setFill()
    path.fill()
    cg.restoreGState()

    cg.saveGState()
    path.addClip()
    if let base {
        NSGradient(
            starting: base.withAlphaComponent(0.98),
            ending: base.blended(withFraction: 0.26, of: .black) ?? base
        )?.draw(in: path, angle: -90)
    }
    if let source, source.size.width > 0, source.size.height > 0 {
        let tileRect = NSRect(x: 100, y: 100, width: 824, height: 824)
        let target: NSRect
        if mode == "full" {
            let scale = max(824 / source.size.width, 824 / source.size.height)
            let width = source.size.width * scale
            let height = source.size.height * scale
            target = NSRect(
                x: tileRect.midX - width / 2,
                y: tileRect.midY - height / 2,
                width: width,
                height: height
            )
        } else {
            let available = tileRect.insetBy(dx: 165, dy: 165)
            let scale = min(available.width / source.size.width, available.height / source.size.height)
            let width = source.size.width * scale
            let height = source.size.height * scale
            target = NSRect(
                x: available.midX - width / 2,
                y: available.midY - height / 2,
                width: width,
                height: height
            )
        }
        source.draw(
            in: target,
            from: NSRect(origin: .zero, size: source.size),
            operation: .sourceOver,
            fraction: 1
        )
    }
    cg.restoreGState()

    let highlight = squircle(tile.insetBy(dx: 8, dy: 8))
    NSColor.white.withAlphaComponent(0.12).setStroke()
    highlight.lineWidth = 2
    highlight.stroke()

    if let letter {
        let baseFont = NSFont.systemFont(ofSize: 400, weight: .semibold)
        let descriptor = baseFont.fontDescriptor.withDesign(.rounded) ?? baseFont.fontDescriptor
        let font = NSFont(descriptor: descriptor, size: 400) ?? baseFont
        let paragraph = NSMutableParagraphStyle()
        paragraph.alignment = .center
        let attributes: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: NSColor.white.withAlphaComponent(0.97),
            .paragraphStyle: paragraph,
            .kern: -8,
        ]
        let text = letter as NSString
        let textSize = text.size(withAttributes: attributes)
        let rect = NSRect(
            x: 0,
            y: (CGFloat(size) - textSize.height) / 2 + 6,
            width: CGFloat(size),
            height: textSize.height
        )
        text.draw(in: rect, withAttributes: attributes)
    }

    NSGraphicsContext.restoreGraphicsState()
    guard let data = bitmap.representation(using: .png, properties: [:]) else { exit(3) }
    do {
        try data.write(to: URL(fileURLWithPath: output))
    } catch {
        fputs("OpenNavo icon could not be written: \(error)\n", stderr)
        exit(4)
    }
}

private func configPath() -> String {
    let arguments = CommandLine.arguments
    if let index = arguments.firstIndex(of: "--config"), arguments.count > index + 1 {
        return arguments[index + 1]
    }
    if let bundled = Bundle.main.path(forResource: "config", ofType: "json") {
        return bundled
    }
    return "config.json"
}

if let index = CommandLine.arguments.firstIndex(of: "--make-icon") {
    guard CommandLine.arguments.count > index + 3 else {
        fputs("Usage: opennavo-webapp-helper --make-icon <letter> <RRGGBB> <output.png>\n", stderr)
        exit(2)
    }
    _ = NSApplication.shared
    renderIcon(
        source: nil,
        mode: "letter",
        background: .white,
        letter: CommandLine.arguments[index + 1],
        base: parseHexColor(CommandLine.arguments[index + 2]) ?? .systemBlue,
        output: CommandLine.arguments[index + 3]
    )
    exit(0)
}

if let index = CommandLine.arguments.firstIndex(of: "--render-icon") {
    guard CommandLine.arguments.count > index + 4 else {
        fputs("Usage: opennavo-webapp-helper --render-icon <source.png> <output.png> <tile|full> <RRGGBB>\n", stderr)
        exit(2)
    }
    guard let source = NSImage(contentsOfFile: CommandLine.arguments[index + 1]) else {
        fputs("OpenNavo icon source could not be read\n", stderr)
        exit(3)
    }
    _ = NSApplication.shared
    renderIcon(
        source: source,
        mode: CommandLine.arguments[index + 3],
        background: parseHexColor(CommandLine.arguments[index + 4]) ?? .white,
        letter: nil,
        base: nil,
        output: CommandLine.arguments[index + 2]
    )
    exit(0)
}

do {
    let data = try Data(contentsOf: URL(fileURLWithPath: configPath()))
    let config = try JSONDecoder().decode(Config.self, from: data)
    let app = NSApplication.shared
    let delegate = WebAppDelegate(config: config)
    app.delegate = delegate
    app.setActivationPolicy(.regular)
    app.run()
} catch {
    fputs("OpenNavo web app could not start: \(error)\n", stderr)
    exit(1)
}
