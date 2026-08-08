import SwiftUI

struct ContentImportActivityView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    @Environment(\.scenePhase) private var scenePhase
    let store: WorkspaceStore
    @State private var filter: ContentImportFilter = .all
    @State private var deletingItem: ContentImport?

    private var visibleItems: [ContentImport] {
        store.contentImports.filter { item in
            switch filter {
            case .all: true
            case .active: item.status == .active
            case .failed: item.status == .failed || item.status == .needsReview
            case .completed: item.status == .completed
            }
        }
    }

    private var presentation: Binding<ContentImportPresentation?> {
        Binding(
            get: { session.contentImportPresentation },
            set: { session.contentImportPresentation = $0 }
        )
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("导入任务").font(.largeTitle.weight(.semibold))
                    Text("播客解析、转写和整理会在服务端后台继续运行。")
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Picker("筛选", selection: $filter) {
                    ForEach(ContentImportFilter.allCases) { item in Text(item.title).tag(item) }
                }
                .pickerStyle(.segmented)
                .frame(width: 330)
                Button("导入播客", systemImage: "plus") {
                    session.presentPodcastImport()
                }
                .buttonStyle(.borderedProminent)
            }
            .padding(22)

            Divider()

            if store.isLoadingImports && store.contentImports.isEmpty {
                ProgressView("正在读取导入任务…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if visibleItems.isEmpty {
                ContentUnavailableView {
                    Label(filter == .all ? "还没有导入任务" : "没有符合条件的任务", systemImage: "podcasts")
                } description: {
                    Text("粘贴公开的小宇宙或 Apple Podcasts 单集链接开始导入。")
                } actions: {
                    Button("导入播客") { session.presentPodcastImport() }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(visibleItems) { item in
                    ContentImportRow(
                        item: item,
                        projectName: projectName(for: item),
                        isMutating: store.isMutating,
                        openNote: { noteID in openWindow(value: noteID) },
                        cancel: { await cancel(item) },
                        retry: { await retry(item) },
                        delete: { deletingItem = item }
                    )
                    .padding(.vertical, 7)
                }
                .listStyle(.inset)
                .refreshable { await store.loadContentImports() }
            }
        }
        .toolbar {
            Button("刷新", systemImage: "arrow.clockwise") {
                Task { await store.loadContentImports() }
            }
            .disabled(store.isLoadingImports)
        }
        .sheet(item: presentation) { request in
            PodcastImportSheet(request: request, store: store)
        }
        .confirmationDialog(
            "删除这条导入记录？",
            isPresented: Binding(
                get: { deletingItem != nil },
                set: { if !$0 { deletingItem = nil } }
            )
        ) {
            Button("删除记录和逐字稿", role: .destructive) {
                guard let item = deletingItem else { return }
                Task { await delete(item) }
            }
        } message: {
            Text(deletingItem?.resultNoteID == nil
                 ? "导入记录和已保存的逐字稿会被删除。"
                 : "只删除导入记录和逐字稿，已经生成的笔记会保留。")
        }
        .task(id: "\(scenePhase)-\(store.activeContentImportCount)") {
            await pollWhileNeeded()
        }
        .overlay(alignment: .bottom) {
            if let error = store.errorMessage {
                ErrorBanner(message: error) { store.errorMessage = nil }.padding()
            }
        }
    }

    private func pollWhileNeeded() async {
        if store.projects.isEmpty { await store.loadProjects() }
        await store.loadContentImports()
        while !Task.isCancelled, store.activeContentImportCount > 0 {
            do {
                try await Task.sleep(for: .seconds(scenePhase == .active ? 5 : 20))
            } catch is CancellationError {
                return
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            await store.loadContentImports()
        }
    }

    private func projectName(for item: ContentImport) -> String {
        let names = item.projectIDs.compactMap { store.projectsByID[$0]?.name }
        return names.isEmpty ? "未归属" : names.joined(separator: "、")
    }

    private func cancel(_ item: ContentImport) async {
        do { try await store.cancelContentImport(item) }
        catch { store.errorMessage = error.localizedDescription }
    }

    private func retry(_ item: ContentImport) async {
        do { try await store.retryContentImport(item) }
        catch { store.errorMessage = error.localizedDescription }
    }

    private func delete(_ item: ContentImport) async {
        do {
            try await store.deleteContentImport(item)
            deletingItem = nil
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }
}

private struct ContentImportRow: View {
    let item: ContentImport
    let projectName: String
    let isMutating: Bool
    let openNote: (String) -> Void
    let cancel: () async -> Void
    let retry: () async -> Void
    let delete: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: item.status.systemImage)
                    .font(.title2)
                    .foregroundStyle(statusColor)
                    .symbolEffect(.rotate, options: .repeat(.continuous), isActive: item.status == .active)
                    .frame(width: 28)
                VStack(alignment: .leading, spacing: 4) {
                    Text(item.title.nilIfBlank ?? fallbackTitle).font(.headline)
                    HStack {
                        Text(item.podcastTitle.nilIfBlank ?? sourceName)
                        Text("·")
                        Text(projectName)
                        Text("·")
                        Text(Date(timeIntervalSince1970: TimeInterval(item.updatedAt)), style: .relative)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                Spacer()
                Text(item.status.title)
                    .font(.caption.weight(.semibold))
                    .padding(.horizontal, 9)
                    .padding(.vertical, 5)
                    .background(statusColor.opacity(0.12), in: .capsule)
                    .foregroundStyle(statusColor)
            }

            if item.status == .active {
                VStack(alignment: .leading, spacing: 5) {
                    ProgressView(value: Double(item.progress), total: 100)
                    HStack {
                        Text(stageLabel(item.stage))
                        Spacer()
                        Text("\(item.progress)%")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }

            if let message = item.errorMessage.nilIfBlank {
                Label(message, systemImage: "exclamationmark.triangle")
                    .font(.callout)
                    .foregroundStyle(.red)
                    .padding(9)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.red.opacity(0.07), in: .rect(cornerRadius: 8))
            }

            HStack {
                Label(item.summarizeWithAI ? "逐字稿 + AI 整理" : "仅完整逐字稿", systemImage: item.summarizeWithAI ? "sparkles" : "text.document")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                if item.status == .active {
                    Button("取消") { Task { await cancel() } }.disabled(isMutating)
                }
                if (item.status == .failed || item.status == .needsReview), item.retryable != false {
                    Button(item.isTextAIFailure ? "重试 AI 整理" : "重试", systemImage: "arrow.clockwise") {
                        Task { await retry() }
                    }
                    .disabled(isMutating)
                }
                if item.status == .completed, let noteID = item.resultNoteID {
                    if item.resultNoteAvailable != false {
                        Button("打开笔记", systemImage: "arrow.up.right.square") { openNote(noteID) }
                            .buttonStyle(.borderedProminent)
                    } else {
                        Text("笔记已删除").font(.caption).foregroundStyle(.secondary)
                    }
                }
                if item.canDelete {
                    Button("删除", systemImage: "trash", role: .destructive, action: delete)
                        .disabled(isMutating)
                }
            }
        }
    }

    private var fallbackTitle: String { item.status == .active ? "正在识别播客单集" : "播客导入任务" }
    private var sourceName: String {
        switch item.sourceType {
        case "apple": "Apple Podcasts"
        case "xiaoyuzhou": "小宇宙"
        default: "等待解析来源"
        }
    }
    private var statusColor: Color {
        switch item.status {
        case .active: .blue
        case .completed: .green
        case .failed, .needsReview: .orange
        case .canceled: .secondary
        }
    }

    private func stageLabel(_ stage: String) -> String {
        switch stage {
        case "queued": "等待开始"
        case "resolving": "正在解析链接"
        case "acquiring": "正在获取或转写逐字稿"
        case "summarizing": "正在用 AI 整理"
        case "publishing": "正在保存笔记"
        case "completed": "处理完成"
        default: "处理中"
        }
    }
}

private extension Optional where Wrapped == String {
    var nilIfBlank: String? {
        guard let self else { return nil }
        return self.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : self
    }
}
