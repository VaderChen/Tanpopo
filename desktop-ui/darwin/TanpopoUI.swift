import AppKit
import Foundation
import WebKit

private struct LaunchOptions {
    let url: URL
    let title: String
    let iconURL: URL?
    let resident: Bool
    let version: String
    let apiURL: URL?
    let githubURL: URL?

    static func parse(arguments: [String]) throws -> Self {
        var urlValue: String?
        var title = "Tanpopo"
        var iconURL: URL?
        var resident = false
        var version = "dev"
        var apiURL: URL?
        var githubURL: URL?
        var index = 1
        while index < arguments.count {
            let option = arguments[index]
            guard index + 1 < arguments.count else {
                throw LaunchError.missingValue(option)
            }
            let value = arguments[index + 1]
            switch option {
            case "--url":
                urlValue = value
            case "--title":
                title = value.trimmingCharacters(in: .whitespacesAndNewlines)
            case "--icon":
                iconURL = URL(fileURLWithPath: value)
            case "--resident":
                switch value.lowercased() {
                case "1", "true", "yes", "on": resident = true
                case "0", "false", "no", "off": resident = false
                default: throw LaunchError.invalidBoolean(option, value)
                }
            case "--version":
                version = value.trimmingCharacters(in: .whitespacesAndNewlines)
            case "--api-url":
                apiURL = try optionalWebURL(value, option: option)
            case "--github-url":
                githubURL = try optionalWebURL(value, option: option)
            default:
                throw LaunchError.unknownOption(option)
            }
            index += 2
        }

        guard let urlValue,
              let url = URL(string: urlValue),
              ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
              url.host != nil else {
            throw LaunchError.invalidURL(urlValue ?? "")
        }
        return Self(
            url: url,
            title: title.isEmpty ? "Tanpopo" : title,
            iconURL: iconURL,
            resident: resident,
            version: version.isEmpty ? "dev" : version,
            apiURL: normalizedOpenAIBaseURL(apiURL),
            githubURL: githubURL
        )
    }

    private static func optionalWebURL(_ value: String, option: String) throws -> URL? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        guard let url = URL(string: trimmed),
              ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
              url.host != nil else {
            throw LaunchError.invalidURL("\(option)=\(value)")
        }
        return url
    }
}

private func normalizedOpenAIBaseURL(_ url: URL?) -> URL? {
    guard let url,
          var components = URLComponents(url: url, resolvingAgainstBaseURL: false),
          ["http", "https"].contains(components.scheme?.lowercased() ?? ""),
          components.host != nil else {
        return nil
    }
    let root = components.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    components.path = root.hasSuffix("v1") ? "/\(root)" : (root.isEmpty ? "/v1" : "/\(root)/v1")
    components.query = nil
    components.fragment = nil
    return components.url
}

private enum LaunchError: LocalizedError {
    case missingValue(String)
    case unknownOption(String)
    case invalidURL(String)
    case invalidBoolean(String, String)

    var errorDescription: String? {
        switch self {
        case .missingValue(let option):
            return "啟動參數缺少值：\(option)"
        case .unknownOption(let option):
            return "不支援的啟動參數：\(option)"
        case .invalidURL(let value):
            return "UI 網址格式錯誤：\(value)"
        case .invalidBoolean(let option, let value):
            return "啟動參數 \(option) 的布林值無效：\(value)"
        }
    }
}

@MainActor
private final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate,
    WKUIDelegate, WKNavigationDelegate, WKScriptMessageHandler
{
    private static let nativeMessageHandler = "tanpopoNative"

    private let options: LaunchOptions
    private var window: NSWindow?
    private var webView: WKWebView?
    private var statusItem: NSStatusItem?
    private var residentMode: Bool
    private var runtimeAPIURL: URL?
    private var isQuitting = false

    init(options: LaunchOptions) {
        self.options = options
        residentMode = options.resident
        runtimeAPIURL = options.apiURL
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let iconURL = options.iconURL,
           let icon = NSImage(contentsOf: iconURL) {
            NSApplication.shared.applicationIconImage = icon
        }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        configuration.userContentController.add(
            self,
            name: Self.nativeMessageHandler
        )
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.allowsMagnification = true
        webView.uiDelegate = self
        webView.navigationDelegate = self

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1280, height: 860),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = options.title
        window.minSize = NSSize(width: 960, height: 640)
        window.contentView = webView
        window.delegate = self
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApplication.shared.activate(ignoringOtherApps: true)
        self.window = window
        self.webView = webView
        updateStatusItem()

        let request = URLRequest(
            url: options.url,
            cachePolicy: .reloadIgnoringLocalCacheData,
            timeoutInterval: 30
        )
        webView.load(request)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        !residentMode
    }

    func applicationShouldHandleReopen(
        _ sender: NSApplication,
        hasVisibleWindows flag: Bool
    ) -> Bool {
        if !flag {
            showWindow(nil)
        }
        return true
    }

    func applicationWillTerminate(_ notification: Notification) {
        webView?.configuration.userContentController.removeScriptMessageHandler(
            forName: Self.nativeMessageHandler
        )
        removeStatusItem()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard residentMode, !isQuitting else { return true }
        sender.orderOut(nil)
        NSApplication.shared.setActivationPolicy(.accessory)
        return false
    }

    func userContentController(
        _ userContentController: WKUserContentController,
        didReceive message: WKScriptMessage
    ) {
        guard message.name == Self.nativeMessageHandler,
              message.frameInfo.isMainFrame,
              message.frameInfo.securityOrigin.host == options.url.host,
              let object = message.body as? [String: Any],
              let type = object["type"] as? String else {
            return
        }
        switch type {
        case "resident-mode":
            guard let enabled = object["enabled"] as? Bool else { return }
            residentMode = enabled
            updateStatusItem()
        case "open-model-directory":
            guard let rawPath = object["path"] as? String else { return }
            openModelDirectory(rawPath)
        case "runtime-api-url":
            guard let rawURL = object["url"] as? String else { return }
            runtimeAPIURL = normalizedOpenAIBaseURL(URL(string: rawURL))
            refreshStatusMenu()
        default:
            return
        }
    }

    private func openModelDirectory(_ rawPath: String) {
        let path = rawPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard path.hasPrefix("/") else { return }
        let directory = URL(fileURLWithPath: path, isDirectory: true).standardizedFileURL
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: directory.path, isDirectory: &isDirectory),
              isDirectory.boolValue else {
            return
        }
        NSWorkspace.shared.open(directory)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        guard navigationAction.navigationType == .linkActivated,
              navigationAction.targetFrame?.isMainFrame == true,
              let url = navigationAction.request.url,
              isExternalWebURL(url) else {
            decisionHandler(.allow)
            return
        }
        NSWorkspace.shared.open(url)
        decisionHandler(.cancel)
    }

    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        guard navigationAction.targetFrame == nil,
              let url = navigationAction.request.url else {
            return nil
        }
        if isExternalWebURL(url) {
            NSWorkspace.shared.open(url)
        } else {
            webView.load(URLRequest(url: url))
        }
        return nil
    }

    private func isExternalWebURL(_ url: URL) -> Bool {
        guard ["http", "https"].contains(url.scheme?.lowercased() ?? "") else {
            return false
        }
        return url.scheme?.lowercased() != options.url.scheme?.lowercased()
            || url.host?.lowercased() != options.url.host?.lowercased()
            || normalizedPort(url) != normalizedPort(options.url)
    }

    private func normalizedPort(_ url: URL) -> Int? {
        if let port = url.port {
            return port
        }
        switch url.scheme?.lowercased() {
        case "http": return 80
        case "https": return 443
        default: return nil
        }
    }

    private func updateStatusItem() {
        if residentMode {
            installStatusItem()
        } else {
            removeStatusItem()
        }
    }

    private func installStatusItem() {
        guard statusItem == nil else { return }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            button.image = makeStatusIcon()
            button.imagePosition = .imageOnly
            button.toolTip = options.title
        }

        statusItem = item
        refreshStatusMenu()
    }

    private func refreshStatusMenu() {
        guard let statusItem else { return }
        let menu = NSMenu()
        let showItem = NSMenuItem(
            title: "顯示 \(options.title)",
            action: #selector(showWindow(_:)),
            keyEquivalent: ""
        )
        showItem.target = self
        menu.addItem(showItem)
        menu.addItem(.separator())

        let versionItem = NSMenuItem(title: "版本 \(options.version)", action: nil, keyEquivalent: "")
        versionItem.isEnabled = false
        menu.addItem(versionItem)

        let apiTitle = runtimeAPIURL?.absoluteString ?? "尚未提供"
        let copyAPIItem = NSMenuItem(
            title: "複製 API URL：\(apiTitle)",
            action: #selector(copyAPIURL(_:)),
            keyEquivalent: ""
        )
        copyAPIItem.target = self
        copyAPIItem.isEnabled = runtimeAPIURL != nil
        menu.addItem(copyAPIItem)

        let githubItem = NSMenuItem(
            title: "開啟 GitHub 專案",
            action: #selector(openGitHub(_:)),
            keyEquivalent: ""
        )
        githubItem.target = self
        githubItem.isEnabled = options.githubURL != nil
        menu.addItem(githubItem)
        menu.addItem(.separator())

        let quitItem = NSMenuItem(
            title: "結束 \(options.title)",
            action: #selector(quitApplication(_:)),
            keyEquivalent: "q"
        )
        quitItem.target = self
        menu.addItem(quitItem)
        statusItem.menu = menu
    }

    private func makeStatusIcon() -> NSImage {
        let icon = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { _ in
            NSColor.black.setStroke()
            NSColor.black.setFill()
            let center = NSPoint(x: 9, y: 10.5)
            let ray = NSBezierPath()
            ray.lineWidth = 1.25
            ray.lineCapStyle = .round
            for index in 0..<10 {
                let angle = (Double(index) * 2 * Double.pi / 10) + Double.pi / 2
                ray.move(to: center)
                ray.line(to: NSPoint(
                    x: center.x + CGFloat(cos(angle) * 5.3),
                    y: center.y + CGFloat(sin(angle) * 5.3)
                ))
            }
            ray.stroke()

            let centerDot = NSBezierPath(ovalIn: NSRect(x: 7.6, y: 9.1, width: 2.8, height: 2.8))
            centerDot.fill()
            for index in 0..<10 {
                let angle = (Double(index) * 2 * Double.pi / 10) + Double.pi / 2
                let dot = NSPoint(
                    x: center.x + CGFloat(cos(angle) * 7.0),
                    y: center.y + CGFloat(sin(angle) * 7.0)
                )
                NSBezierPath(ovalIn: NSRect(x: dot.x - 0.7, y: dot.y - 0.7, width: 1.4, height: 1.4)).fill()
            }

            let stem = NSBezierPath()
            stem.lineWidth = 1.25
            stem.lineCapStyle = .round
            stem.move(to: NSPoint(x: 9, y: 9.5))
            stem.curve(
                to: NSPoint(x: 10.5, y: 0.8),
                controlPoint1: NSPoint(x: 8.7, y: 6.4),
                controlPoint2: NSPoint(x: 9.1, y: 3.2)
            )
            stem.stroke()
            return true
        }
        icon.isTemplate = true
        return icon
    }

    private func removeStatusItem() {
        guard let statusItem else { return }
        NSStatusBar.system.removeStatusItem(statusItem)
        self.statusItem = nil
    }

    @objc private func showWindow(_ sender: Any?) {
        NSApplication.shared.setActivationPolicy(.regular)
        window?.makeKeyAndOrderFront(nil)
        NSApplication.shared.activate(ignoringOtherApps: true)
    }

    @objc private func copyAPIURL(_ sender: Any?) {
        guard let value = runtimeAPIURL?.absoluteString else { return }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(value, forType: .string)
    }

    @objc private func openGitHub(_ sender: Any?) {
        guard let url = options.githubURL else { return }
        NSWorkspace.shared.open(url)
    }

    @objc private func quitApplication(_ sender: Any?) {
        isQuitting = true
        NSApplication.shared.terminate(nil)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping () -> Void
    ) {
        let alert = NSAlert()
        alert.alertStyle = .informational
        alert.messageText = options.title
        alert.informativeText = message
        alert.addButton(withTitle: "確定")
        if let parentWindow = webView.window {
            alert.beginSheetModal(for: parentWindow) { _ in completionHandler() }
            return
        }
        alert.runModal()
        completionHandler()
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (Bool) -> Void
    ) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "請確認操作"
        alert.informativeText = message
        alert.addButton(withTitle: "確定")
        alert.addButton(withTitle: "取消")
        if let parentWindow = webView.window {
            alert.beginSheetModal(for: parentWindow) { response in
                completionHandler(response == .alertFirstButtonReturn)
            }
            return
        }
        completionHandler(alert.runModal() == .alertFirstButtonReturn)
    }
}

@main
private struct TanpopoUI {
    @MainActor
    static func main() {
        do {
            let options = try LaunchOptions.parse(arguments: CommandLine.arguments)
            let application = NSApplication.shared
            application.setActivationPolicy(.regular)
            let delegate = AppDelegate(options: options)
            application.delegate = delegate
            application.run()
        } catch {
            FileHandle.standardError.write(Data("\(error.localizedDescription)\n".utf8))
            Foundation.exit(EXIT_FAILURE)
        }
    }
}
