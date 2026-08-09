import SwiftUI
import UniformTypeIdentifiers

struct NotesLibraryView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore
    @SceneStorage("notes.selectedNote") private var selectedNoteID = ""
    @SceneStorage("notes.selectedProject") private var selectedProjectID = ""
    @SceneStorage("notes.selectedTag") private var selectedTag = ""
    @SceneStorage("notes.query") private var query = ""
    @SceneStorage("notes.sort") private var sort = "recent"
    @State private var pendingDelete: FlowNote?
    @State private var libraryNotice: String?

    private var filteredNotes: [FlowNote] {
        store.notes.filter { note in
            let projectMatches = selectedProjectID.isEmpty || note.projects.contains { $0.id == selectedProjectID }
            let tagMatches = selectedTag.isEmpty || note.parsedTags.contains(selectedTag)
            let keyword = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            let searchMatches = keyword.isEmpty || "\(note.title) \(note.plainTextPreview)".lowercased().contains(keyword)
            return projectMatches && tagMatches && searchMatches
        }
    }

    private var selectedNote: FlowNote? {
        filteredNotes.first { $0.id == selectedNoteID } ?? filteredNotes.first
    }

    private var tagCounts: [(String, Int)] {
        var counts: [String: Int] = [:]
        store.notes.forEach { note in
            note.parsedTags.forEach { counts[$0, default: 0] += 1 }
        }
        return counts.sorted { $0.value > $1.value }
    }

    var body: some View {
        HSplitView {
            filters
                .frame(minWidth: 170, idealWidth: 200, maxWidth: 240)
            noteList
                .frame(minWidth: 280, idealWidth: 340)
            preview
                .frame(minWidth: 320, idealWidth: 440)
        }
        .task(id: sort) {
            await store.loadNotes(sort: sort)
            preserveSelection()
            applyPendingNoteSelection()
        }
        .onChange(of: session.pendingWorkspaceEntitySelection) {
            applyPendingNoteSelection()
        }
        .alert("永久删除这篇笔记？", isPresented: deletePresented, presenting: pendingDelete) { note in
            Button("取消", role: .cancel) { pendingDelete = nil }
            Button("永久删除", role: .destructive) {
                Task { await delete(note) }
            }
        } message: { note in
            Text("“\(note.title)”将被永久删除，此操作无法撤销。")
        }
        .overlay(alignment: .bottom) {
            if let libraryNotice {
                Label(libraryNotice, systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(.regularMaterial, in: .capsule)
                    .shadow(radius: 6, y: 2)
                    .padding(.bottom, 18)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
    }

    private var filters: some View {
        List {
            Section("资料库") {
                Button {
                    selectedProjectID = ""
                    selectedTag = ""
                } label: {
                    Label("全部笔记", systemImage: "books.vertical")
                }
                .buttonStyle(.plain)

                ForEach(store.projects.filter { $0.status != .archived }) { project in
                    Button {
                        selectedProjectID = project.id
                        selectedTag = ""
                    } label: {
                        HStack {
                            Label(project.name, systemImage: "folder")
                            Spacer()
                            Text("\(store.notes.count { $0.projects.contains { $0.id == project.id } })")
                                .foregroundStyle(.secondary)
                        }
                    }
                    .buttonStyle(.plain)
                    .dropDestination(for: String.self) { noteIDs, _ in
                        return associateNotes(noteIDs, with: project.id)
                    }
                }
            }

            if !tagCounts.isEmpty {
                Section("标签") {
                    ForEach(tagCounts, id: \.0) { tag, count in
                        Button {
                            selectedTag = tag
                            selectedProjectID = ""
                        } label: {
                            HStack {
                                Label(tag, systemImage: "tag")
                                Spacer()
                                Text("\(count)").foregroundStyle(.secondary)
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .listStyle(.sidebar)
    }

    private var noteList: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("排序", selection: $sort) {
                    Text("最近更新").tag("recent")
                    Text("标题排序").tag("az")
                }
                .labelsHidden()
                Spacer()
                Button("导入播客", systemImage: "podcasts") {
                    session.presentPodcastImport(projectID: selectedProjectID)
                    openWindow(id: "import-activity")
                }
                .labelStyle(.iconOnly)
                Button("新建笔记", systemImage: "square.and.pencil") {
                    Task { await createNote() }
                }
                .labelStyle(.iconOnly)
                .keyboardShortcut("n", modifiers: .command)
                .disabled(store.mutationsDisabled)
            }
            .padding(10)

            List(filteredNotes, selection: $selectedNoteID) { note in
                Button {
                    selectedNoteID = note.id
                } label: {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Text(note.title.isEmpty ? "未命名笔记" : note.title)
                                .fontWeight(.medium)
                                .lineLimit(1)
                            Spacer()
                            Text(Date(timeIntervalSince1970: TimeInterval(note.updatedAt)), style: .relative)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Text(note.plainTextPreview.isEmpty ? "暂无正文内容" : note.plainTextPreview)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                        HStack(spacing: 6) {
                            Text(note.projects.first?.name ?? "未归属")
                            ForEach(note.parsedTags.prefix(2), id: \.self) { tag in
                                Text("#\(tag)")
                            }
                        }
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                    }
                    .contentShape(.rect)
                    .padding(.vertical, 5)
                }
                .buttonStyle(.plain)
                .tag(note.id)
                .draggable(note.id) {
                    Label(note.title.isEmpty ? "未命名笔记" : note.title, systemImage: "note.text")
                        .padding(8)
                        .background(.regularMaterial, in: .rect(cornerRadius: 8))
                }
                .simultaneousGesture(TapGesture(count: 2).onEnded { openWindow(value: note.id) })
                .contextMenu {
                    Button("在新窗口打开") { openWindow(value: note.id) }
                    Divider()
                    Button("永久删除", role: .destructive) { pendingDelete = note }
                        .disabled(store.mutationsDisabled)
                }
            }
            .listStyle(.inset)
            .searchable(text: $query, prompt: "搜索标题或正文")
            .onKeyPress(.return) {
                guard let selectedNote else { return .ignored }
                openWindow(value: selectedNote.id)
                return .handled
            }
            .overlay {
                if store.isLoading && store.notes.isEmpty {
                    ProgressView("正在加载笔记…")
                } else if filteredNotes.isEmpty {
                    ContentUnavailableView.search(text: query)
                }
            }
        }
    }

    @ViewBuilder
    private var preview: some View {
        if let note = selectedNote {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(note.title.isEmpty ? "未命名笔记" : note.title)
                                .font(.largeTitle.weight(.semibold))
                            Text(Date(timeIntervalSince1970: TimeInterval(note.updatedAt)).formatted(date: .long, time: .shortened))
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("打开编辑", systemImage: "arrow.up.right.square") {
                            openWindow(value: note.id)
                        }
                        .keyboardShortcut("o", modifiers: .command)
                    }

                    Divider()
                    if note.body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        Text("这篇笔记还没有正文。")
                            .foregroundStyle(.secondary)
                    } else if let markdown = try? AttributedString(markdown: note.body) {
                        Text(markdown)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    } else {
                        Text(note.body).textSelection(.enabled)
                    }

                    Divider()
                    LabeledContent("项目", value: note.projects.map(\.name).joined(separator: "、").nilIfEmpty ?? "未归属")
                    LabeledContent("标签", value: note.parsedTags.map { "#\($0)" }.joined(separator: " ").nilIfEmpty ?? "无标签")
                    LabeledContent("正文字符", value: "\(note.plainTextPreview.count)")

                    Button("打开编辑") { openWindow(value: note.id) }
                        .buttonStyle(.borderedProminent)
                }
                .padding(22)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .dropDestination(for: URL.self) { urls, _ in
                return uploadDroppedFiles(urls, to: note.id)
            }
        } else {
            ContentUnavailableView("选择一篇笔记", systemImage: "note.text")
        }
    }

    private var deletePresented: Binding<Bool> {
        Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } })
    }

    private func preserveSelection() {
        guard !store.notes.contains(where: { $0.id == selectedNoteID }) else { return }
        selectedNoteID = store.notes.first?.id ?? ""
    }

    private func applyPendingNoteSelection() {
        guard case .note(let noteID) = session.pendingWorkspaceEntitySelection,
              !store.isLoading else { return }
        if store.notes.contains(where: { $0.id == noteID }) {
            query = ""
            selectedProjectID = ""
            selectedTag = ""
            selectedNoteID = noteID
        }
        session.consumeWorkspaceEntitySelection()
    }

    private func createNote() async {
        do {
            let inheritedTags = selectedTag.isEmpty ? [] : [selectedTag]
            let note = try await store.createNote(
                projectID: selectedProjectID.nilIfEmpty,
                tags: inheritedTags
            )
            selectedNoteID = note.id
            openWindow(value: note.id)
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }

    private func delete(_ note: FlowNote) async {
        do {
            try await store.deleteNote(id: note.id)
            pendingDelete = nil
            preserveSelection()
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }

    private func associateNotes(_ noteIDs: [String], with projectID: String) -> Bool {
        let validIDs = noteIDs.filter { noteID in
            store.notes.contains { $0.id == noteID && !$0.projects.contains { $0.id == projectID } }
        }
        guard !validIDs.isEmpty, !store.mutationsDisabled else { return false }
        Task {
            do {
                for noteID in validIDs {
                    _ = try await store.addProjectToNote(noteID: noteID, projectID: projectID)
                }
                showNotice(validIDs.count == 1 ? "已关联到项目" : "已关联 \(validIDs.count) 篇笔记")
            } catch {
                store.errorMessage = error.localizedDescription
            }
        }
        return true
    }

    private func uploadDroppedFiles(_ urls: [URL], to noteID: String) -> Bool {
        let fileURLs = urls.filter(\.isFileURL)
        guard !fileURLs.isEmpty, !store.mutationsDisabled else { return false }
        Task { await upload(fileURLs, to: noteID) }
        return true
    }

    private func upload(_ urls: [URL], to noteID: String) async {
        await store.loadAttachments(noteID: noteID)
        let existingCount = store.attachmentsByNoteID[noteID]?.count ?? 0
        let acceptedURLs = Array(urls.prefix(max(0, 20 - existingCount)))
        guard !acceptedURLs.isEmpty else {
            store.errorMessage = "每篇笔记最多添加 20 个附件。"
            return
        }

        do {
            for url in acceptedURLs {
                let payload = try attachmentPayload(for: url)
                try await store.uploadAttachment(
                    noteID: noteID,
                    fileName: url.lastPathComponent,
                    mimeType: payload.mimeType,
                    data: payload.data
                )
            }
            showNotice(acceptedURLs.count == 1 ? "附件已上传" : "已上传 \(acceptedURLs.count) 个附件")
        } catch {
            store.errorMessage = error.localizedDescription
        }
    }

    private func attachmentPayload(for url: URL) throws -> (mimeType: String, data: Data) {
        let didAccess = url.startAccessingSecurityScopedResource()
        defer { if didAccess { url.stopAccessingSecurityScopedResource() } }
        let values = try url.resourceValues(forKeys: [.fileSizeKey, .contentTypeKey])
        if let size = values.fileSize, size > 200 * 1_024 * 1_024 {
            throw ValidationError("“\(url.lastPathComponent)”超过 200 MB")
        }
        let contentType = values.contentType ?? UTType(filenameExtension: url.pathExtension)
        return (
            contentType?.preferredMIMEType ?? "application/octet-stream",
            try Data(contentsOf: url, options: .mappedIfSafe)
        )
    }

    private func showNotice(_ message: String) {
        withAnimation { libraryNotice = message }
        Task {
            try? await Task.sleep(for: .seconds(3))
            guard libraryNotice == message else { return }
            withAnimation { libraryNotice = nil }
        }
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
