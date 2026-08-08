import SwiftUI
import WebKit

@MainActor
final class RichEditorController {
    private weak var webView: WKWebView?
    private var noteID = ""
    private var generation = ""
    private var isReady = false
    private(set) var lastEditorMarkdown = ""

    func attach(_ webView: WKWebView, noteID: String, generation: String, markdown: String) {
        self.webView = webView
        self.noteID = noteID
        self.generation = generation
        lastEditorMarkdown = markdown
        isReady = false
    }

    func detach(_ webView: WKWebView) {
        guard self.webView === webView else { return }
        self.webView = nil
        isReady = false
    }

    func editorDidBecomeReady(markdown: String) {
        guard let webView else { return }
        isReady = true
        lastEditorMarkdown = markdown
        call(
            in: webView,
            script: """
            window.flowspaceNative.configure(noteID, generation)
            window.flowspaceNative.setMarkdown(markdown, generation)
            """,
            arguments: ["noteID": noteID, "generation": generation, "markdown": markdown]
        )
    }

    func editorDidChange(markdown: String) {
        lastEditorMarkdown = markdown
    }

    func synchronize(markdown: String) {
        guard isReady, markdown != lastEditorMarkdown, let webView else { return }
        lastEditorMarkdown = markdown
        call(
            in: webView,
            script: "window.flowspaceNative.setMarkdown(markdown, generation)",
            arguments: ["markdown": markdown, "generation": generation]
        )
    }

    func execute(_ command: String, value: String = "") {
        guard isReady, let webView else { return }
        call(
            in: webView,
            script: "window.flowspaceNative.command(command, value)",
            arguments: ["command": command, "value": value]
        )
    }

    func replaceSelection(with text: String, expectedText: String) async -> Bool {
        guard isReady, let webView else { return false }
        do {
            let result = try await webView.callAsyncJavaScript(
                "return window.flowspaceNative.replaceSelection(text, expectedText)",
                arguments: ["text": text, "expectedText": expectedText],
                in: nil,
                contentWorld: .page
            )
            return result as? Bool ?? false
        } catch {
            return false
        }
    }

    func find(_ query: String, backwards: Bool = false) {
        guard isReady, let webView else { return }
        call(
            in: webView,
            script: "window.flowspaceNative.find(query, backwards)",
            arguments: ["query": query, "backwards": backwards]
        )
    }

    func focus() {
        guard isReady, let webView else { return }
        call(in: webView, script: "window.flowspaceNative.focus()", arguments: [:])
    }

    private func call(in webView: WKWebView, script: String, arguments: [String: Any]) {
        Task { @MainActor in
            _ = try? await webView.callAsyncJavaScript(
                script,
                arguments: arguments,
                in: nil,
                contentWorld: .page
            )
        }
    }
}

struct RichNoteEditor: NSViewRepresentable {
    let noteID: String
    let generation: String
    @Binding var markdown: String
    @Binding var selectedText: String
    @Binding var wordCount: Int
    let controller: RichEditorController
    let onChange: () -> Void
    let onSaveRequest: () -> Void
    let onFindRequest: () -> Void
    let onGlobalSearchRequest: () -> Void
    let onLoadFailure: (String) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    func makeNSView(context: Context) -> WKWebView {
        let contentController = WKUserContentController()
        contentController.add(context.coordinator, name: "flowspace")
        let configuration = WKWebViewConfiguration()
        configuration.userContentController = contentController
        configuration.defaultWebpagePreferences.allowsContentJavaScript = true
        configuration.websiteDataStore = .nonPersistent()

        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.setValue(false, forKey: "drawsBackground")
        webView.allowsMagnification = true
        webView.magnification = 1
        controller.attach(webView, noteID: noteID, generation: generation, markdown: markdown)

        if let directory = Bundle.main.resourceURL?.appending(path: "RichEditor", directoryHint: .isDirectory),
           FileManager.default.fileExists(atPath: directory.appending(path: "index.html").path) {
            webView.loadFileURL(directory.appending(path: "index.html"), allowingReadAccessTo: directory)
        } else {
            Task { @MainActor in onLoadFailure("找不到本地富文本编辑器资源") }
        }
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        context.coordinator.parent = self
        controller.synchronize(markdown: markdown)
    }

    static func dismantleNSView(_ webView: WKWebView, coordinator: Coordinator) {
        webView.configuration.userContentController.removeScriptMessageHandler(forName: "flowspace")
        coordinator.parent.controller.detach(webView)
        webView.navigationDelegate = nil
    }

    @MainActor
    final class Coordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
        var parent: RichNoteEditor

        init(parent: RichNoteEditor) {
            self.parent = parent
        }

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.name == "flowspace", let bridgeMessage = RichEditorBridgeMessage(body: message.body) else { return }
            if bridgeMessage.kind == .ready {
                parent.controller.editorDidBecomeReady(markdown: parent.markdown)
                return
            }
            guard bridgeMessage.noteID == parent.noteID, bridgeMessage.generation == parent.generation else { return }

            switch bridgeMessage.kind {
            case .change:
                guard let markdown = bridgeMessage.markdown else { return }
                parent.controller.editorDidChange(markdown: markdown)
                if parent.markdown != markdown {
                    parent.markdown = markdown
                    parent.wordCount = bridgeMessage.wordCount ?? parent.wordCount
                    parent.onChange()
                }
            case .selection:
                parent.selectedText = bridgeMessage.selectedText ?? ""
            case .documentState:
                parent.wordCount = bridgeMessage.wordCount ?? parent.wordCount
            case .save:
                parent.onSaveRequest()
            case .find:
                parent.onFindRequest()
            case .globalSearch:
                parent.onGlobalSearchRequest()
            case .ready:
                break
            }
        }

        func webView(
            _ webView: WKWebView,
            didFailProvisionalNavigation navigation: WKNavigation?,
            withError error: any Error
        ) {
            parent.onLoadFailure("富文本编辑器加载失败：\(error.localizedDescription)")
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation?, withError error: any Error) {
            parent.onLoadFailure("富文本编辑器加载失败：\(error.localizedDescription)")
        }
    }
}

struct RichEditorBridgeMessage: Equatable {
    enum Kind: String {
        case ready
        case change
        case selection
        case documentState
        case save
        case find
        case globalSearch
    }

    let kind: Kind
    let noteID: String
    let generation: String
    let markdown: String?
    let selectedText: String?
    let wordCount: Int?

    init?(body: Any) {
        guard let object = body as? [String: Any],
              let rawType = object["type"] as? String,
              let kind = Kind(rawValue: rawType) else { return nil }
        self.kind = kind
        noteID = object["noteID"] as? String ?? ""
        generation = object["generation"] as? String ?? ""
        markdown = object["markdown"] as? String
        selectedText = object["selectedText"] as? String
        wordCount = object["wordCount"] as? Int
    }
}
