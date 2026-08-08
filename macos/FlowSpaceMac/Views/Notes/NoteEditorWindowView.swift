import AppKit
import SwiftUI

struct NoteEditorWindowView: View {
    enum SaveState: Equatable {
        case loading
        case saved
        case dirty
        case saving
        case failed(String)
    }

    private struct CloseFailure: Identifiable {
        let id = UUID()
        let message: String
    }

    let noteID: String
    let store: WorkspaceStore
    @Environment(\.openWindow) private var openWindow
    @Environment(\.dismissWindow) private var dismissWindow
    @State private var title = ""
    @State private var markdownBody = ""
    @State private var selectedEditorText = ""
    @State private var wordCount = 0
    @State private var editorController = RichEditorController()
    @State private var editorGeneration = UUID().uuidString
    @State private var editorError: String?
    @State private var showFindBar = false
    @State private var findQuery = ""
    @FocusState private var findFieldFocused: Bool
    @State private var selectedProjectIDs: Set<String> = []
    @State private var tagsText = ""
    @State private var createdAt: Int64 = 0
    @State private var updatedAt: Int64 = 0
    @State private var state: SaveState = .loading
    @State private var editGeneration = 0
    @State private var didLoad = false
    @State private var showAttachments = false
    @State private var isAnnotating = false
    @State private var annotationNotice: String?
    @State private var closeFailure: CloseFailure?

    var body: some View {
        VStack(spacing: 0) {
            if state == .loading {
                ProgressView("正在打开笔记…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                TextField("笔记标题", text: $title)
                    .font(.largeTitle.weight(.semibold))
                    .textFieldStyle(.plain)
                    .padding(.horizontal, 24)
                    .padding(.top, 22)
                    .onChange(of: title) { markDirty() }

                HStack {
                    Label(projectSummary, systemImage: "folder")
                        .foregroundStyle(.secondary)
                    Spacer()
                    saveStatus
                }
                .padding(.horizontal, 24)
                .padding(.vertical, 12)

                Divider()

                if showFindBar {
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                        TextField("查找当前笔记", text: $findQuery)
                            .textFieldStyle(.roundedBorder)
                            .focused($findFieldFocused)
                            .onSubmit { editorController.find(findQuery) }
                        Button("上一个", systemImage: "chevron.up") {
                            editorController.find(findQuery, backwards: true)
                        }
                        .labelStyle(.iconOnly)
                        Button("下一个", systemImage: "chevron.down") {
                            editorController.find(findQuery)
                        }
                        .labelStyle(.iconOnly)
                        Button("关闭", systemImage: "xmark") {
                            showFindBar = false
                            editorController.focus()
                        }
                        .labelStyle(.iconOnly)
                    }
                    .padding(.horizontal, 20)
                    .padding(.vertical, 8)
                    .background(.bar)
                    .onAppear { findFieldFocused = true }
                }

                ZStack(alignment: .top) {
                    RichNoteEditor(
                        noteID: noteID,
                        generation: editorGeneration,
                        markdown: $markdownBody,
                        selectedText: $selectedEditorText,
                        wordCount: $wordCount,
                        controller: editorController,
                        onChange: markDirty,
                        onSaveRequest: { Task { _ = await save() } },
                        onFindRequest: { showFindBar = true },
                        onGlobalSearchRequest: { openWindow(id: "global-search") },
                        onLoadFailure: { editorError = $0 }
                    )
                    .accessibilityLabel("富文本正文")

                    if let editorError {
                        Label(editorError, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                            .padding(12)
                            .background(.regularMaterial, in: .rect(cornerRadius: 10))
                            .padding(.top, 12)
                    }
                }
            }
        }
        .navigationTitle(title.isEmpty ? "未命名笔记" : title)
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button("粗体", systemImage: "bold") { editorController.execute("bold") }
                    .labelStyle(.iconOnly)
                    .keyboardShortcut("b", modifiers: .command)
                Button("斜体", systemImage: "italic") { editorController.execute("italic") }
                    .labelStyle(.iconOnly)
                    .keyboardShortcut("i", modifiers: .command)

                Button("保存", systemImage: "square.and.arrow.down") {
                    Task { _ = await save() }
                }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(state == .loading || state == .saving || state == .saved)

                Menu("编辑格式", systemImage: "textformat") {
                    Button("一级标题") { editorController.execute("heading1") }
                    Button("二级标题") { editorController.execute("heading2") }
                    Button("三级标题") { editorController.execute("heading3") }
                    Divider()
                    Button("项目符号") { editorController.execute("bulletList") }
                    Button("编号列表") { editorController.execute("orderedList") }
                    Button("引用") { editorController.execute("blockquote") }
                    Button("代码块") { editorController.execute("codeBlock") }
                    Button("分割线") { editorController.execute("horizontalRule") }
                    Divider()
                    Button("删除线") { editorController.execute("strike") }
                        .keyboardShortcut("x", modifiers: [.command, .shift])
                }

                Button("添加假名", systemImage: "character.book.closed") {
                    Task { await addFuriganaToSelection() }
                }
                .disabled(isAnnotating || state == .loading)
                .help("为编辑器中选中的日文添加假名")

                Button("检查器", systemImage: "sidebar.right") {
                    showAttachments.toggle()
                }
                .help("管理项目、任务、标签、同步和附件")

                Button("查找", systemImage: "doc.text.magnifyingglass") {
                    showFindBar = true
                }
                .labelStyle(.iconOnly)
                .keyboardShortcut("f", modifiers: .command)

                Button("关闭", systemImage: "xmark") {
                    Task { await attemptClose() }
                }
                .labelStyle(.iconOnly)
                .help("保存并关闭笔记")
            }
        }
        .overlay(alignment: .bottom) {
            if let annotationNotice {
                Label(annotationNotice, systemImage: "character.book.closed")
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(.regularMaterial, in: .capsule)
                    .shadow(radius: 6, y: 2)
                    .padding(.bottom, 18)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .inspector(isPresented: $showAttachments) {
            VStack(spacing: 0) {
                ScrollView {
                    VStack(spacing: 16) {
                        NoteMetadataInspector(
                            noteID: noteID,
                            store: store,
                            selectedProjectIDs: $selectedProjectIDs,
                            tagsText: $tagsText,
                            wordCount: wordCount,
                            createdAt: createdAt,
                            updatedAt: updatedAt,
                            markDirty: markDirty
                        )
                        Divider()
                        NoteSyncCard(noteID: noteID, store: store)
                            .padding(.horizontal, 16)
                            .padding(.bottom, 16)
                    }
                }
                .frame(maxHeight: 560)
                Divider()
                NoteAttachmentsInspector(
                    noteID: noteID,
                    currentBody: markdownBody,
                    store: store,
                    prepareForTranscription: prepareForTranscription,
                    onTranscribed: applyTranscription
                )
            }
                .inspectorColumnWidth(min: 270, ideal: 320, max: 400)
        }
        .task { await load() }
        .task {
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: .seconds(5))
                } catch is CancellationError {
                    return
                } catch {
                    state = .failed(error.localizedDescription)
                    return
                }
                if state == .dirty {
                    _ = await save()
                }
            }
        }
        .windowDismissBehavior(didLoad && state != .saved ? .disabled : .enabled)
        .alert("笔记尚未保存", isPresented: closeFailureIsPresented) {
            Button("重试保存") {
                Task { await attemptClose() }
            }
            Button("复制本地内容并关闭", role: .destructive) {
                copyLocalContentAndClose()
            }
            Button("取消关闭", role: .cancel) {}
        } message: {
            Text("\(closeFailure?.message ?? "当前修改未能确认保存。")\n\n可以重试，或先把当前内容复制到剪贴板后关闭。")
        }
    }

    @ViewBuilder
    private var saveStatus: some View {
        switch state {
        case .loading:
            ProgressView().controlSize(.small)
        case .saved:
            HStack(spacing: 10) {
                Text("\(wordCount) 字").foregroundStyle(.secondary)
                Label("已保存", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
            }
        case .dirty:
            Label("等待保存", systemImage: "circle.dotted").foregroundStyle(.secondary)
        case .saving:
            Label("保存中", systemImage: "arrow.triangle.2.circlepath").foregroundStyle(.secondary)
        case .failed(let message):
            HStack(spacing: 8) {
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                    .lineLimit(1)
                Button("重试") { Task { _ = await save() } }
                    .controlSize(.small)
            }
        }
    }

    private var projectSummary: String {
        let names = store.projects
            .filter { selectedProjectIDs.contains($0.id) }
            .map(\.name)
        if names.isEmpty { return "未归属项目" }
        if names.count == 1 { return names[0] }
        return "\(names[0]) 等 \(names.count) 个项目"
    }

    private var closeFailureIsPresented: Binding<Bool> {
        Binding(
            get: { closeFailure != nil },
            set: { isPresented in
                if !isPresented { closeFailure = nil }
            }
        )
    }

    private func load() async {
        do {
            if store.projects.isEmpty { await store.loadProjects() }
            let note = try await store.getNote(id: noteID)
            title = note.title
            markdownBody = note.body
            selectedProjectIDs = Set(note.projects.map(\.id))
            tagsText = note.parsedTags.joined(separator: ", ")
            createdAt = note.createdAt
            updatedAt = note.updatedAt
            didLoad = true
            state = .saved
        } catch {
            state = .failed(error.localizedDescription)
        }
    }

    private func markDirty() {
        guard didLoad else { return }
        state = .dirty
        editGeneration += 1
    }

    @discardableResult
    private func save() async -> Bool {
        guard didLoad else { return false }
        if state == .saving {
            while state == .saving {
                do {
                    try await Task.sleep(for: .milliseconds(50))
                } catch {
                    return false
                }
            }
            if state == .dirty { return await save() }
            return state == .saved
        }
        let savingGeneration = editGeneration
        state = .saving
        do {
            let saved = try await store.saveNote(
                id: noteID,
                title: title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "未命名笔记" : title,
                body: markdownBody,
                projectIDs: selectedProjectIDs.sorted(),
                tags: FlowNote.tagsJSON(FlowNote.normalizedTags(from: tagsText))
            )
            updatedAt = saved.updatedAt
            state = editGeneration == savingGeneration ? .saved : .dirty
            return state == .saved
        } catch is CancellationError {
            state = .dirty
            return false
        } catch {
            state = .failed(error.localizedDescription)
            return false
        }
    }

    private func addFuriganaToSelection() async {
        let selectedText = selectedEditorText
        guard !selectedText.isEmpty else {
            showAnnotationNotice("请先选择需要标注的日文")
            return
        }
        guard !selectedText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            showAnnotationNotice("选择内容不能为空")
            return
        }

        isAnnotating = true
        defer { isAnnotating = false }
        do {
            let result = try await store.annotateJapanese(selectedText)
            guard await editorController.replaceSelection(with: result.markdown, expectedText: selectedText) else {
                showAnnotationNotice("正文已发生变化，请重新选择后再试")
                return
            }
            selectedEditorText = ""
            showAnnotationNotice(result.source == "ai" ? "已使用 AI 添加假名" : "已使用本地词典添加假名")
        } catch {
            showAnnotationNotice(error.localizedDescription)
        }
    }

    private func prepareForTranscription() async -> Bool {
        if state == .dirty || state.isFailure {
            _ = await save()
        }
        return state == .saved
    }

    private func applyTranscription(_ result: VoiceTranscriptionResult) {
        markdownBody = result.body
        editorController.synchronize(markdown: result.body)
        editGeneration += 1
        updatedAt = result.updatedAt
        state = .saved
    }

    private func attemptClose() async {
        if !didLoad || state == .saved {
            closeWindow()
            return
        }
        if await save() {
            closeWindow()
        } else {
            let message: String
            if case .failed(let failureMessage) = state {
                message = failureMessage
            } else {
                message = "当前修改未能确认保存。"
            }
            closeFailure = CloseFailure(message: message)
        }
    }

    private func copyLocalContentAndClose() {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString("\(title)\n\n\(markdownBody)", forType: .string)
        closeWindow()
    }

    private func closeWindow() {
        withTransaction(\.dismissBehavior, .destructive) {
            dismissWindow(value: noteID)
        }
    }

    private func showAnnotationNotice(_ message: String) {
        withAnimation { annotationNotice = message }
        Task {
            try? await Task.sleep(for: .seconds(3))
            guard annotationNotice == message else { return }
            withAnimation { annotationNotice = nil }
        }
    }
}

private extension NoteEditorWindowView.SaveState {
    var isFailure: Bool {
        if case .failed = self { return true }
        return false
    }
}
