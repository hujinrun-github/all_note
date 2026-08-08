import SwiftUI

struct GlobalSearchView: View {
    enum Scope: String, CaseIterable, Identifiable {
        case all
        case task
        case project
        case event
        case note

        var id: String { rawValue }
        var title: String {
            switch self {
            case .all: "全部"
            case .task: "任务"
            case .project: "项目"
            case .event: "日程"
            case .note: "笔记"
            }
        }
    }

    @Environment(\.dismiss) private var dismiss
    @AppStorage("recentSearches") private var recentSearchesJSON = "[]"
    let store: WorkspaceStore
    let onSelect: (SearchResultItem) -> Void
    @State private var query = ""
    @State private var scope: Scope = .all
    @State private var results: [SearchResultItem] = []
    @State private var isSearching = false
    @State private var errorMessage: String?

    private var filteredResults: [SearchResultItem] {
        scope == .all ? results : results.filter { $0.type == scope.rawValue }
    }

    private var recentSearches: [String] {
        guard let data = recentSearchesJSON.data(using: .utf8),
              let values = try? JSONDecoder().decode([String].self, from: data) else { return [] }
        return values
    }

    var body: some View {
        NavigationStack {
            Group {
                if query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    recentContent
                } else if isSearching && results.isEmpty {
                    ProgressView("正在搜索…")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let errorMessage {
                    ContentUnavailableView("搜索失败", systemImage: "exclamationmark.magnifyingglass", description: Text(errorMessage))
                } else if filteredResults.isEmpty {
                    ContentUnavailableView.search(text: query)
                } else {
                    List(filteredResults) { result in
                        Button {
                            select(result)
                        } label: {
                            HStack(spacing: 12) {
                                Image(systemName: icon(for: result.type))
                                    .font(.title3)
                                    .foregroundStyle(.tint)
                                    .frame(width: 26)
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(result.title).fontWeight(.medium)
                                    Text(result.highlight.isEmpty ? typeLabel(result.type) : result.highlight)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(2)
                                }
                                Spacer()
                                Text(typeLabel(result.type))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .navigationTitle("全局搜索")
            .searchable(text: $query, placement: .toolbar, prompt: "搜索任务、项目、日程和笔记")
            .searchScopes($scope) {
                ForEach(Scope.allCases) { scope in
                    Text(scope.title).tag(scope)
                }
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("关闭") { dismiss() }
                }
            }
            .task(id: query) {
                await runSearch()
            }
        }
        .frame(width: 720, height: 560)
    }

    private var recentContent: some View {
        List {
            if recentSearches.isEmpty {
                ContentUnavailableView(
                    "搜索 FlowSpace",
                    systemImage: "magnifyingglass",
                    description: Text("输入关键词搜索任务、项目、日程和笔记。")
                )
            } else {
                Section("最近搜索") {
                    ForEach(recentSearches, id: \.self) { value in
                        Button {
                            query = value
                        } label: {
                            Label(value, systemImage: "clock.arrow.circlepath")
                        }
                        .buttonStyle(.plain)
                    }
                    Button("清除最近搜索", role: .destructive) {
                        recentSearchesJSON = "[]"
                    }
                }
            }
        }
    }

    private func runSearch() async {
        let value = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else {
            results = []
            errorMessage = nil
            return
        }
        do {
            try await Task.sleep(for: .milliseconds(220))
            guard !Task.isCancelled else { return }
            isSearching = true
            defer { isSearching = false }
            errorMessage = nil
            results = try await store.search(value)
        } catch is CancellationError {
            return
        } catch {
            isSearching = false
            errorMessage = error.localizedDescription
        }
    }

    private func select(_ result: SearchResultItem) {
        remember(query)
        onSelect(result)
        dismiss()
    }

    private func remember(_ value: String) {
        let clean = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty else { return }
        let updated = [clean] + recentSearches.filter { $0 != clean }
        if let data = try? JSONEncoder().encode(Array(updated.prefix(8))),
           let json = String(data: data, encoding: .utf8) {
            recentSearchesJSON = json
        }
    }

    private func icon(for type: String) -> String {
        switch type {
        case "task": "checkmark.circle"
        case "project": "folder"
        case "event": "calendar"
        case "note": "note.text"
        default: "doc.text.magnifyingglass"
        }
    }

    private func typeLabel(_ type: String) -> String {
        Scope(rawValue: type)?.title ?? type
    }
}

