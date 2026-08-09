import AppKit
import SwiftUI

struct CommandPaletteView: View {
    private enum Entry: Identifiable {
        case command(CommandPaletteCommand)
        case result(SearchResultItem)

        var id: String {
            switch self {
            case .command(let command): "command:\(command.id)"
            case .result(let result): "result:\(result.type):\(result.id)"
            }
        }
    }

    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    @Environment(\.dismissWindow) private var dismissWindow
    @AppStorage("recentOpenedSearchResults") private var recentResultsJSON = "[]"
    @State private var query = ""
    @State private var results: [SearchResultItem] = []
    @State private var selectedID: String?
    @State private var isSearching = false
    @State private var errorMessage: String?
    @FocusState private var searchFocused: Bool

    let store: WorkspaceStore

    private var commandsOnly: Bool {
        query.trimmingCharacters(in: .whitespacesAndNewlines).hasPrefix(">")
    }

    private var commandQuery: String {
        let value = query.trimmingCharacters(in: .whitespacesAndNewlines)
        return commandsOnly ? String(value.dropFirst()).trimmingCharacters(in: .whitespaces) : value
    }

    private var commands: [CommandPaletteCommand] {
        CommandPaletteCommand.available(isAdmin: session.isAdmin).filter { $0.matches(commandQuery) }
    }

    private var recentResults: [SearchResultItem] {
        SearchResultHistory.decode(recentResultsJSON)
    }

    private var visibleResults: [SearchResultItem] {
        let value = query.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.isEmpty { return recentResults }
        return commandsOnly ? [] : results
    }

    private var entries: [Entry] {
        commands.map(Entry.command) + visibleResults.map(Entry.result)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Image(systemName: commandsOnly ? "chevron.right" : "magnifyingglass")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .frame(width: 24)
                TextField("搜索，或输入 > 仅显示命令", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($searchFocused)
                    .onKeyPress(.upArrow) { moveSelection(-1) }
                    .onKeyPress(.downArrow) { moveSelection(1) }
                    .onKeyPress(.return, phases: .down) { press in
                        executeSelected(openInNewWindow: press.modifiers.contains(.command))
                    }
                    .onExitCommand { close() }
                if isSearching {
                    ProgressView().controlSize(.small)
                } else if !query.isEmpty {
                    Button("清除", systemImage: "xmark.circle.fill") { query = "" }
                        .labelStyle(.iconOnly)
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 16)

            Divider()

            if entries.isEmpty {
                ContentUnavailableView.search(text: commandQuery)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(selection: $selectedID) {
                    if !commands.isEmpty {
                        Section(query.isEmpty ? "常用命令" : "命令") {
                            ForEach(commands) { command in
                                row(.command(command)).tag(Entry.command(command).id)
                            }
                        }
                    }
                    if !visibleResults.isEmpty {
                        Section(query.isEmpty ? "最近打开" : "搜索结果") {
                            ForEach(visibleResults) { result in
                                row(.result(result)).tag(Entry.result(result).id)
                            }
                        }
                    }
                }
                .listStyle(.inset)
            }

            Divider()
            HStack(spacing: 14) {
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .lineLimit(1)
                } else {
                    Text("↑↓ 选择")
                    Text("↩ 打开")
                    Text("⌘↩ 新窗口")
                    Text("Esc 关闭")
                }
                Spacer()
                Text("⌘⇧F 完整搜索")
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .frame(width: 660, height: 480)
        .background(.regularMaterial)
        .task {
            try? await Task.sleep(for: .milliseconds(80))
            searchFocused = true
            normalizeSelection()
        }
        .task(id: query) { await runSearch() }
        .onChange(of: entries.map(\.id)) { normalizeSelection() }
        .onExitCommand { close() }
    }

    @ViewBuilder
    private func row(_ entry: Entry) -> some View {
        Button {
            execute(entry, openInNewWindow: false)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: icon(for: entry))
                    .font(.title3)
                    .foregroundStyle(.tint)
                    .frame(width: 26)
                VStack(alignment: .leading, spacing: 3) {
                    Text(title(for: entry)).fontWeight(.medium)
                    Text(subtitle(for: entry))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer()
                if case .result(let result) = entry {
                    Text(typeLabel(result.type))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
    }

    private func runSearch() async {
        let value = query.trimmingCharacters(in: .whitespacesAndNewlines)
        results = []
        errorMessage = nil
        guard !value.isEmpty, !commandsOnly else {
            isSearching = false
            return
        }
        do {
            try await Task.sleep(for: .milliseconds(200))
            guard !Task.isCancelled else { return }
            isSearching = true
            defer { isSearching = false }
            results = try await store.search(value)
        } catch is CancellationError {
            return
        } catch {
            isSearching = false
            errorMessage = error.localizedDescription
        }
    }

    private func moveSelection(_ offset: Int) -> KeyPress.Result {
        guard !entries.isEmpty else { return .ignored }
        let current = selectedID.flatMap { id in entries.firstIndex { $0.id == id } }
        let next = min(max((current ?? (offset > 0 ? -1 : entries.count)) + offset, 0), entries.count - 1)
        selectedID = entries[next].id
        return .handled
    }

    private func normalizeSelection() {
        guard !entries.isEmpty else {
            selectedID = nil
            return
        }
        if !entries.contains(where: { $0.id == selectedID }) {
            selectedID = entries[0].id
        }
    }

    private func executeSelected(openInNewWindow: Bool) -> KeyPress.Result {
        guard let selectedID, let entry = entries.first(where: { $0.id == selectedID }) else { return .ignored }
        execute(entry, openInNewWindow: openInNewWindow)
        return .handled
    }

    private func execute(_ entry: Entry, openInNewWindow: Bool) {
        switch entry {
        case .command(let command):
            execute(command, openInNewWindow: openInNewWindow)
        case .result(let result):
            recentResultsJSON = SearchResultHistory.remembering(result, in: recentResultsJSON)
            open(result, openInNewWindow: openInNewWindow)
        }
        close()
    }

    private func execute(_ command: CommandPaletteCommand, openInNewWindow: Bool) {
        if let destination = command.destination {
            session.requestWorkspaceDestination(destination)
            if openInNewWindow {
                openWindow(id: "workspace")
            } else {
                NSApp.activate()
            }
            return
        }
        switch command {
        case .quickCapture:
            openWindow(id: "quick-capture")
        case .fullSearch:
            openWindow(id: "global-search")
        case .importActivity:
            openWindow(id: "import-activity")
        case .accountAdmin:
            openWindow(id: "account-admin")
        default:
            break
        }
    }

    private func open(_ result: SearchResultItem, openInNewWindow: Bool) {
        switch result.route {
        case .note(let noteID):
            openWindow(value: noteID)
        case .workspace(let destination, let selection):
            if let selection { session.requestWorkspaceEntitySelection(selection) }
            session.requestWorkspaceDestination(destination)
            if openInNewWindow {
                openWindow(id: "workspace")
            } else {
                NSApp.activate()
            }
        }
    }

    private func close() {
        dismissWindow(id: "command-palette")
    }

    private func icon(for entry: Entry) -> String {
        switch entry {
        case .command(let command): command.systemImage
        case .result(let result):
            switch result.type {
            case "task": "checkmark.circle"
            case "project": "folder"
            case "event": "calendar"
            case "note": "note.text"
            default: "doc.text.magnifyingglass"
            }
        }
    }

    private func title(for entry: Entry) -> String {
        switch entry {
        case .command(let command): command.title
        case .result(let result): result.title
        }
    }

    private func subtitle(for entry: Entry) -> String {
        switch entry {
        case .command(let command): command.subtitle
        case .result(let result): result.highlight.isEmpty ? typeLabel(result.type) : result.highlight
        }
    }

    private func typeLabel(_ type: String) -> String {
        switch type {
        case "task": "任务"
        case "project": "项目"
        case "event": "日程"
        case "note": "笔记"
        default: type
        }
    }
}
