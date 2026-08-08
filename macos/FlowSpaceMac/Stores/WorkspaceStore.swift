import Foundation
import Observation

@MainActor
@Observable
final class WorkspaceStore {
    private(set) var projects: [ProjectV2] = []
    private(set) var tasks: [TaskV2] = []
    private(set) var occurrences: [OccurrenceV2] = []
    private(set) var calendarEntries: [CalendarEntryV2] = []
    private(set) var notes: [FlowNote] = []
    private(set) var roadmapsByProjectID: [String: RoadmapV2] = [:]
    private(set) var loadedRoadmapProjectIDs: Set<String> = []
    private(set) var loadingRoadmapProjectIDs: Set<String> = []
    private(set) var attachmentsByNoteID: [String: [NoteAttachment]] = [:]
    private(set) var contentImports: [ContentImport] = []
    private(set) var isLoadingImports = false
    private(set) var syncTargets: [SyncTarget] = []
    private(set) var noteSyncBindings: [String: NoteSyncBindingResponse] = [:]
    private(set) var isLoadingSync = false
    private(set) var isSyncing = false
    private(set) var runtime: RuntimeSettings?
    private(set) var isLoadingRuntime = false
    private(set) var profile: UserProfile?
    private(set) var profileAvatarData: Data?
    private(set) var isLoadingProfile = false
    private(set) var adminUsers: [AccountUser] = []
    private(set) var adminPagination = AccountPagination(page: 1, pageSize: 20, total: 0)
    private(set) var isLoadingAdminUsers = false
    private(set) var summary: SummaryData?
    private(set) var summaryAttention: [OccurrenceV2] = []
    private(set) var isLoading = false
    private(set) var isMutating = false
    var errorMessage: String?

    private let client: APIClient
    private let notifications: AppNotificationService
    private let spotlight: AppSpotlightService
    private var spotlightWorkspaceID: String?
    private var spotlightGeneration = 0
    private var taskWorkspaceLoadGeneration = 0
    private var observedContentImportStatuses: [String: ContentImportStatus] = [:]

    init(
        client: APIClient,
        notifications: AppNotificationService = .shared,
        spotlight: AppSpotlightService = .shared
    ) {
        self.client = client
        self.notifications = notifications
        self.spotlight = spotlight
    }

    var tasksByID: [String: TaskV2] {
        Dictionary(uniqueKeysWithValues: tasks.map { ($0.id, $0) })
    }

    var projectsByID: [String: ProjectV2] {
        Dictionary(uniqueKeysWithValues: projects.map { ($0.id, $0) })
    }

    var inboxProject: ProjectV2? {
        projects.first { $0.systemRole == "inbox" }
    }

    var defaultProjectID: String {
        inboxProject?.id ?? projects.first(where: { $0.status == .active })?.id ?? projects.first?.id ?? ""
    }

    func configureSpotlight(workspaceID: String) {
        spotlightGeneration += 1
        spotlightWorkspaceID = workspaceID
        spotlight.activateWorkspace(workspaceID)
    }

    func refreshSpotlightIndex() async {
        guard let workspaceID = spotlightWorkspaceID else { return }
        let generation = spotlightGeneration
        do {
            async let loadedProjects = client.listProjects()
            async let loadedTasks = client.listTasks()
            async let loadedNotes = client.listNotes()
            let result = try await (loadedProjects, loadedTasks, loadedNotes)
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            let projectsByID = Dictionary(uniqueKeysWithValues: result.0.map { ($0.id, $0) })
            await spotlight.replaceProjects(result.0, workspaceID: workspaceID)
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            await spotlight.replaceTasks(result.1, projects: projectsByID, workspaceID: workspaceID)
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            await spotlight.replaceNotes(result.2, workspaceID: workspaceID)
        } catch is CancellationError {
            return
        } catch {
            // Spotlight is an enhancement; a temporary indexing failure must not block the workspace.
        }
    }

    func clear() {
        spotlightGeneration += 1
        taskWorkspaceLoadGeneration += 1
        spotlightWorkspaceID = nil
        projects = []
        tasks = []
        occurrences = []
        calendarEntries = []
        notes = []
        roadmapsByProjectID = [:]
        loadedRoadmapProjectIDs = []
        loadingRoadmapProjectIDs = []
        attachmentsByNoteID = [:]
        contentImports = []
        observedContentImportStatuses = [:]
        isLoadingImports = false
        syncTargets = []
        noteSyncBindings = [:]
        isLoadingSync = false
        isSyncing = false
        runtime = nil
        isLoadingRuntime = false
        profile = nil
        profileAvatarData = nil
        isLoadingProfile = false
        adminUsers = []
        adminPagination = AccountPagination(page: 1, pageSize: 20, total: 0)
        isLoadingAdminUsers = false
        summary = nil
        summaryAttention = []
        errorMessage = nil
    }

    func loadProfile() async throws {
        isLoadingProfile = true
        defer { isLoadingProfile = false }
        let loaded = try await client.userProfile()
        profile = loaded
        if loaded.avatarURL != nil {
            do { profileAvatarData = try await client.userAvatar() }
            catch let error as APIError where error.status == 404 { profileAvatarData = nil }
        } else {
            profileAvatarData = nil
        }
    }

    func updateProfile(displayName: String, locale: String, timeZone: String) async throws {
        let name = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        let zone = timeZone.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name.count <= 80 else {
            throw ValidationError("显示名称不能为空且不能超过 80 个字符")
        }
        guard ["zh-CN", "en-US", "ja-JP"].contains(locale) else {
            throw ValidationError("请选择支持的界面语言")
        }
        guard TimeZone(identifier: zone) != nil else {
            throw ValidationError("请输入有效的 IANA 时区，例如 Asia/Shanghai")
        }
        isMutating = true
        defer { isMutating = false }
        profile = try await client.updateUserProfile(UpdateUserProfileInput(
            displayName: name,
            locale: locale,
            timeZone: zone
        ))
    }

    func uploadProfileAvatar(data: Data, mimeType: String) async throws {
        guard !data.isEmpty, data.count <= 2 * 1024 * 1024 else {
            throw ValidationError("头像不能超过 2 MiB")
        }
        guard ["image/jpeg", "image/png", "image/webp"].contains(mimeType) else {
            throw ValidationError("请选择 JPEG、PNG 或 WebP 图片")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.uploadUserAvatar(data: data, mimeType: mimeType)
        profile = try await client.userProfile()
        profileAvatarData = try await client.userAvatar()
    }

    func deleteProfileAvatar() async throws {
        isMutating = true
        defer { isMutating = false }
        try await client.deleteUserAvatar()
        profile = try await client.userProfile()
        profileAvatarData = nil
    }

    func resetOwnPassword(_ password: String) async throws {
        try PasswordPolicy.validate(password)
        isMutating = true
        defer { isMutating = false }
        try await client.resetPassword(password)
    }

    func loadAdminUsers(page: Int, query: String, pageSize: Int = 20) async throws {
        isLoadingAdminUsers = true
        defer { isLoadingAdminUsers = false }
        let result = try await client.listAdminUsers(
            page: page,
            pageSize: pageSize,
            queryText: query.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        adminUsers = result.users
        adminPagination = result.pagination
    }

    func createAdminUser(_ input: CreateAdminUserInput) async throws {
        try PasswordPolicy.validate(input.temporaryPassword)
        guard !input.email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !input.displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入邮箱和显示名称")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.createAdminUser(CreateAdminUserInput(
            email: input.email.trimmingCharacters(in: .whitespacesAndNewlines),
            displayName: input.displayName.trimmingCharacters(in: .whitespacesAndNewlines),
            temporaryPassword: input.temporaryPassword,
            role: input.role
        ))
    }

    func updateAdminUser(_ user: AccountUser, input: UpdateAdminUserInput) async throws {
        guard !input.email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !input.displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入邮箱和显示名称")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.updateAdminUser(
            id: user.id,
            input: UpdateAdminUserInput(
                email: input.email.trimmingCharacters(in: .whitespacesAndNewlines),
                displayName: input.displayName.trimmingCharacters(in: .whitespacesAndNewlines),
                role: input.role
            )
        )
    }

    func resetAdminPassword(for user: AccountUser, temporaryPassword: String) async throws {
        try PasswordPolicy.validate(temporaryPassword)
        isMutating = true
        defer { isMutating = false }
        try await client.resetAdminUserPassword(id: user.id, temporaryPassword: temporaryPassword)
    }

    func setAdminUserStatus(_ user: AccountUser, status: AccountStatus) async throws {
        isMutating = true
        defer { isMutating = false }
        try await client.setAdminUserStatus(id: user.id, status: status)
    }

    func load(scope: TodayScope) async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            async let loadedProjects = client.listProjects()
            async let loadedTasks = client.listTasks()
            async let loadedOccurrences = client.listOccurrences(Self.query(for: scope))
            let result = try await (loadedProjects, loadedTasks, loadedOccurrences)
            projects = result.0
            tasks = result.1
            occurrences = result.2.sorted(by: OccurrenceV2.scheduleAscending)
            scheduleProjectAndTaskIndexing()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func loadAllTasks() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            async let loadedProjects = client.listProjects()
            async let loadedTasks = client.listTasks()
            async let loadedOccurrences = client.listOccurrences()
            let result = try await (loadedProjects, loadedTasks, loadedOccurrences)
            projects = result.0
            tasks = result.1
            occurrences = result.2.sorted(by: OccurrenceV2.scheduleAscending)
            scheduleProjectAndTaskIndexing()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func loadTaskWorkspace(_ view: TaskWorkspaceView, now: Date = Date()) async {
        taskWorkspaceLoadGeneration += 1
        let loadGeneration = taskWorkspaceLoadGeneration
        isLoading = true
        errorMessage = nil
        defer {
            if taskWorkspaceLoadGeneration == loadGeneration { isLoading = false }
        }

        do {
            async let loadedProjects = client.listProjects()
            async let loadedTasks = client.listTasks()
            let base = try await (loadedProjects, loadedTasks)
            let loadedOccurrences: [OccurrenceV2]
            if view == .draft {
                loadedOccurrences = []
            } else {
                let inboxID = base.0.first(where: { $0.systemRole == "inbox" })?.id
                if view == .inbox, inboxID == nil {
                    loadedOccurrences = []
                } else if let query = view.occurrenceQuery(inboxProjectID: inboxID, now: now) {
                    loadedOccurrences = try await client.listOccurrences(query)
                } else {
                    loadedOccurrences = []
                }
            }
            guard taskWorkspaceLoadGeneration == loadGeneration, !Task.isCancelled else { return }
            projects = base.0
            tasks = base.1
            occurrences = loadedOccurrences.sorted(by: OccurrenceV2.scheduleAscending)
            scheduleProjectAndTaskIndexing()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func loadProjects() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            async let loadedProjects = client.listProjects()
            async let loadedTasks = client.listTasks()
            async let loadedOccurrences = client.listOccurrences()
            async let loadedNotes = client.listNotes()
            let result = try await (loadedProjects, loadedTasks, loadedOccurrences, loadedNotes)
            projects = result.0
            tasks = result.1
            occurrences = result.2.sorted(by: OccurrenceV2.scheduleAscending)
            notes = result.3
            scheduleProjectAndTaskIndexing()
            scheduleNoteIndexing()
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func loadNotes(sort: String = "recent") async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            async let loadedProjects = client.listProjects()
            async let loadedNotes = client.listNotes(sort: sort)
            let result = try await (loadedProjects, loadedNotes)
            projects = result.0
            notes = result.1
            scheduleProjectIndexing()
            scheduleNoteIndexing()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func getNote(id: String) async throws -> FlowNote {
        try await client.getNote(id: id)
    }

    func createNote(projectID: String? = nil) async throws -> FlowNote {
        isMutating = true
        defer { isMutating = false }
        let note = try await client.createNote(CreateNoteInput(
            title: "未命名笔记",
            body: "",
            folderID: "__uncategorized",
            tags: "[]",
            projectIDs: projectID.map { [$0] }
        ))
        notes.insert(note, at: 0)
        scheduleNoteIndexing()
        return note
    }

    func saveNote(
        id: String,
        title: String,
        body: String,
        projectIDs: [String]? = nil,
        tags: String? = nil
    ) async throws -> FlowNote {
        let note = try await client.updateNote(
            id: id,
            input: UpdateNoteInput(
                title: title,
                body: body,
                folderID: nil,
                tags: tags,
                projectIDs: projectIDs
            )
        )
        if let index = notes.firstIndex(where: { $0.id == note.id }) {
            notes[index] = note
        } else {
            notes.insert(note, at: 0)
        }
        scheduleNoteIndexing()
        return note
    }

    func deleteNote(id: String) async throws {
        isMutating = true
        defer { isMutating = false }
        try await client.deleteNote(id: id)
        notes.removeAll { $0.id == id }
        attachmentsByNoteID[id] = nil
        scheduleNoteIndexing()
    }

    func loadAttachments(noteID: String) async {
        do {
            attachmentsByNoteID[noteID] = try await client.listNoteAttachments(noteID: noteID)
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func uploadAttachment(noteID: String, fileName: String, mimeType: String, data: Data) async throws {
        isMutating = true
        defer { isMutating = false }
        let attachment = try await client.uploadNoteAttachment(
            noteID: noteID,
            fileName: fileName,
            mimeType: mimeType,
            data: data
        )
        attachmentsByNoteID[noteID, default: []].append(attachment)
    }

    func deleteAttachment(noteID: String, attachment: NoteAttachment) async throws {
        guard attachment.deletable else { throw ValidationError("这个附件由语音笔记管理，不能在这里删除") }
        isMutating = true
        defer { isMutating = false }
        try await client.deleteNoteAttachment(noteID: noteID, attachmentID: attachment.id)
        attachmentsByNoteID[noteID]?.removeAll { $0.id == attachment.id }
    }

    func transcribeVoiceAttachment(noteID: String, attachment: NoteAttachment) async throws -> VoiceTranscriptionResult {
        guard attachment.source == .voiceNote else {
            throw ValidationError("只有语音笔记附件可以转写")
        }
        do {
            let result = try await client.transcribeVoiceNote(clientID: attachment.id)
            if let index = notes.firstIndex(where: { $0.id == noteID }) {
                notes[index].body = result.body
                notes[index].updatedAt = result.updatedAt
                scheduleNoteIndexing()
            }
            await loadAttachments(noteID: noteID)
            return result
        } catch {
            await loadAttachments(noteID: noteID)
            throw error
        }
    }

    func annotateJapanese(_ text: String) async throws -> FuriganaResult {
        try await client.annotateJapanese(text)
    }

    var activeContentImportCount: Int {
        contentImports.count { $0.status == .active }
    }

    func loadContentImports() async {
        guard !isLoadingImports else { return }
        isLoadingImports = true
        defer { isLoadingImports = false }
        do {
            let loaded = try await client.listContentImports()
                .sorted { $0.updatedAt > $1.updatedAt }
            let transitions = loaded.filter { item in
                ContentImportNotificationPolicy.shouldNotify(
                    previous: observedContentImportStatuses[item.id],
                    current: item.status
                )
            }
            contentImports = loaded
            observedContentImportStatuses = Dictionary(
                uniqueKeysWithValues: loaded.map { ($0.id, $0.status) }
            )
            for item in transitions {
                await notifications.notifyContentImportChanged(item)
            }
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func resolvePodcast(sourceURL: String) async throws -> ResolvedPodcastEpisode {
        let trimmed = sourceURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed),
              let scheme = url.scheme?.lowercased(),
              ["http", "https"].contains(scheme),
              url.host != nil else {
            throw ValidationError("请输入有效的小宇宙或 Apple Podcasts 单集链接")
        }
        isMutating = true
        defer { isMutating = false }
        return try await client.resolvePodcast(sourceURL: trimmed)
    }

    func createContentImport(from draft: ContentImportDraft) async throws -> ContentImport {
        let trimmedURL = draft.sourceURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedURL.isEmpty else { throw ValidationError("请输入播客单集链接") }
        isMutating = true
        defer { isMutating = false }
        let item = try await client.createContentImport(CreateContentImportInput(
            sourceURL: trimmedURL,
            summarizeWithAI: draft.summarizeWithAI,
            summaryPrompt: draft.summarizeWithAI ? draft.summaryPrompt.trimmingCharacters(in: .whitespacesAndNewlines) : nil,
            includeTranscript: draft.summarizeWithAI ? draft.includeTranscript : true,
            language: "auto",
            folderID: nil,
            projectIDs: draft.projectID.isEmpty ? [] : [draft.projectID],
            tags: draft.parsedTags
        ))
        replaceContentImport(item)
        return item
    }

    func cancelContentImport(_ item: ContentImport) async throws {
        isMutating = true
        defer { isMutating = false }
        replaceContentImport(try await client.cancelContentImport(id: item.id))
    }

    func retryContentImport(_ item: ContentImport) async throws {
        isMutating = true
        defer { isMutating = false }
        replaceContentImport(try await client.retryContentImport(id: item.id))
    }

    func deleteContentImport(_ item: ContentImport) async throws {
        guard item.canDelete else { throw ValidationError("进行中的导入任务需要先取消") }
        isMutating = true
        defer { isMutating = false }
        try await client.deleteContentImport(id: item.id)
        contentImports.removeAll { $0.id == item.id }
    }

    private func replaceContentImport(_ item: ContentImport) {
        if let index = contentImports.firstIndex(where: { $0.id == item.id }) {
            contentImports[index] = item
        } else {
            contentImports.insert(item, at: 0)
        }
        contentImports.sort { $0.updatedAt > $1.updatedAt }
        observedContentImportStatuses[item.id] = item.status
    }

    func loadSyncTargets() async {
        guard !isLoadingSync else { return }
        isLoadingSync = true
        defer { isLoadingSync = false }
        do {
            syncTargets = try await client.listSyncTargets()
                .sorted { lhs, rhs in
                    if (lhs.isDefault ?? false) != (rhs.isDefault ?? false) { return lhs.isDefault == true }
                    return lhs.name.localizedStandardCompare(rhs.name) == .orderedAscending
                }
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func saveSyncTarget(_ draft: SyncTargetDraft) async throws -> SyncTarget {
        try Self.validateSyncTarget(draft)
        isMutating = true
        defer { isMutating = false }
        let input = try draft.input()
        let saved = try await client.saveSyncTarget(id: draft.targetID, input: input)
        await loadSyncTargetsAfterMutation()
        return saved
    }

    func testSyncTarget(_ draft: SyncTargetDraft) async throws {
        try Self.validateSyncTarget(draft)
        isMutating = true
        defer { isMutating = false }
        try await client.testSyncTarget(input: try draft.input())
    }

    func deleteSyncTarget(_ target: SyncTarget) async throws {
        isMutating = true
        defer { isMutating = false }
        try await client.deleteSyncTarget(id: target.id)
        syncTargets.removeAll { $0.id == target.id }
    }

    func pushSyncTarget(_ target: SyncTarget) async throws -> SyncBatchResult {
        isMutating = true
        isSyncing = true
        defer {
            isMutating = false
            isSyncing = false
        }
        let result = try await client.pushSyncTarget(id: target.id)
        await notifications.notifySyncCompleted(
            targetName: target.name,
            summary: "已同步 \(result.synced) 篇，失败 \(result.failed) 篇。"
        )
        return result
    }

    func pullSyncTarget(_ target: SyncTarget) async throws -> TargetSyncResult {
        isMutating = true
        isSyncing = true
        defer {
            isMutating = false
            isSyncing = false
        }
        let result = try await client.pullSyncTarget(id: target.id)
        await loadNotes()
        await notifications.notifySyncCompleted(
            targetName: target.name,
            summary: "推送 \(result.pushed) 篇，拉取 \(result.pulled) 篇，导入 \(result.imported) 篇，失败 \(result.failed) 篇。"
        )
        return result
    }

    func loadNoteSync(noteID: String) async {
        do {
            async let targets = client.listSyncTargets()
            async let binding = client.getNoteSyncBinding(noteID: noteID)
            let result = try await (targets, binding)
            syncTargets = result.0
            noteSyncBindings[noteID] = result.1
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func bindNote(noteID: String, targetID: String, confirmChange: Bool) async throws {
        let existing = noteSyncBindings[noteID]?.binding
        isMutating = true
        defer { isMutating = false }
        _ = try await client.saveNoteSyncBinding(
            noteID: noteID,
            input: SaveNoteSyncBindingInput(
                targetID: targetID,
                expectedTargetID: existing?.targetID,
                confirmChangedTarget: confirmChange
            )
        )
        noteSyncBindings[noteID] = try await client.getNoteSyncBinding(noteID: noteID)
    }

    func unbindNote(noteID: String) async throws {
        guard let binding = noteSyncBindings[noteID]?.binding else { return }
        isMutating = true
        defer { isMutating = false }
        try await client.deleteNoteSyncBinding(
            noteID: noteID,
            input: DeleteNoteSyncBindingInput(
                expectedTargetID: binding.targetID,
                expectedUpdatedAt: binding.updatedAt
            )
        )
        noteSyncBindings[noteID] = try await client.getNoteSyncBinding(noteID: noteID)
    }

    func syncNote(noteID: String) async throws -> SyncResultItem {
        isMutating = true
        isSyncing = true
        defer {
            isMutating = false
            isSyncing = false
        }
        let targetName = noteSyncBindings[noteID]?.target?.name
            ?? noteSyncBindings[noteID]?.boundTargetName
            ?? "笔记"
        let result = try await client.syncNote(noteID: noteID)
        noteSyncBindings[noteID] = try await client.getNoteSyncBinding(noteID: noteID)
        await notifications.notifySyncCompleted(
            targetName: targetName,
            summary: result.status == "synced" ? "笔记已同步。" : (result.errorMessage ?? "同步状态：\(result.status)")
        )
        return result
    }

    private func loadSyncTargetsAfterMutation() async {
        do { syncTargets = try await client.listSyncTargets() }
        catch { errorMessage = Self.readable(error) }
    }

    private static func validateSyncTarget(_ draft: SyncTargetDraft) throws {
        guard !draft.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入同步目标名称")
        }
        guard !draft.parsedTags.isEmpty else {
            throw ValidationError("至少设置一个同步标签，避免意外同步整库")
        }
        switch draft.type {
        case .obsidian:
            guard !draft.vaultPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                  !draft.baseFolder.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入服务端可访问的 Vault 路径和同步目录")
            }
        case .notion:
            guard !draft.dataSourceID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入 Notion 数据库链接或 Data Source ID")
            }
            guard draft.tokenConfigured || !draft.token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !draft.tokenEnv.isEmpty else {
                throw ValidationError("请输入 Notion Token")
            }
            guard !draft.titleProperty.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入 Notion 标题属性")
            }
        }
    }

    func loadRuntimeSettings() async {
        guard !isLoadingRuntime else { return }
        isLoadingRuntime = true
        defer { isLoadingRuntime = false }
        do {
            runtime = try await client.runtimeSettings()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func testRuntimeProfile(_ draft: RuntimeProfileDraft) async throws -> ProfileTestResult {
        try Self.validateRuntimeProfile(draft)
        isMutating = true
        defer { isMutating = false }
        return try await client.testServiceProfile(draft.testInput())
    }

    func saveAndApplyRuntimeProfile(_ draft: RuntimeProfileDraft) async throws -> VerifiedProfile {
        try Self.validateRuntimeProfile(draft)
        isMutating = true
        defer { isMutating = false }
        let saved = try await client.saveServiceProfile(draft.saveInput())
        let verified = try await client.verifyServiceProfile(kind: draft.kind, versionID: saved.id)

        if draft.kind != .dataStore {
            let currentBinding = runtime?.binding(draft.kind)
            _ = try await client.setServiceBinding(
                kind: draft.kind,
                input: SetServiceBindingInput(
                    mode: .custom,
                    endpointID: verified.endpointID,
                    expectedRevision: currentBinding?.revision ?? 1,
                    expectedRuntimeRevision: runtime?.bindingRevision ?? 0
                )
            )
            runtime = try await client.runtimeSettings()
        }
        return verified
    }

    func changeServiceMode(kind: ServiceKind, mode: ServiceBindingMode) async throws {
        guard let runtime else { throw ValidationError("运行时设置尚未加载") }
        isMutating = true
        defer { isMutating = false }
        let binding = runtime.binding(kind)
        _ = try await client.setServiceBinding(
            kind: kind,
            input: SetServiceBindingInput(
                mode: mode,
                endpointID: nil,
                expectedRevision: binding?.revision ?? 1,
                expectedRuntimeRevision: runtime.bindingRevision
            )
        )
        self.runtime = try await client.runtimeSettings()
    }

    func startCodexSubscription() async throws -> CodexDeviceAuthorization {
        isMutating = true
        defer { isMutating = false }
        return try await client.startCodexSubscription()
    }

    func pollCodexSubscription(_ authorization: CodexDeviceAuthorization) async throws -> CodexPollResult {
        let binding = runtime?.binding(.llmChat)
        isMutating = true
        defer { isMutating = false }
        let result = try await client.pollCodexSubscription(
            flowID: authorization.flowID,
            expectedRevision: binding?.revision ?? 0,
            expectedRuntimeRevision: runtime?.bindingRevision ?? 0
        )
        if result.status == .connected { runtime = try await client.runtimeSettings() }
        return result
    }

    private static func validateRuntimeProfile(_ draft: RuntimeProfileDraft) throws {
        guard !draft.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入配置名称")
        }
        guard !draft.endpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入服务地址")
        }
        switch draft.kind {
        case .dataStore:
            guard !draft.namespace.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入数据库 Schema")
            }
        case .objectS3:
            guard !draft.namespace.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入 Bucket 名称")
            }
            guard !draft.accessKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                  !draft.objectSecretKey.isEmpty else {
                throw ValidationError("请输入 Access Key 和 Secret Key")
            }
        case .llmChat:
            guard !draft.model.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ValidationError("请输入模型名称")
            }
        case .llmTranscription:
            if draft.transcriptionProvider != .wyoming,
               draft.model.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                throw ValidationError("请输入转写模型名称")
            }
        }
    }

    func loadRoadmap(projectID: String, force: Bool = false) async {
        if !force, loadedRoadmapProjectIDs.contains(projectID) { return }
        loadingRoadmapProjectIDs.insert(projectID)
        defer { loadingRoadmapProjectIDs.remove(projectID) }
        do {
            roadmapsByProjectID[projectID] = try await client.getRoadmap(projectID: projectID)
            loadedRoadmapProjectIDs.insert(projectID)
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func createRoadmap(project: ProjectV2) async throws {
        isMutating = true
        defer { isMutating = false }
        let roadmap = try await client.createRoadmap(
            projectID: project.id,
            input: RoadmapCreateInput(title: "\(project.name) 学习路线", description: nil)
        )
        roadmapsByProjectID[project.id] = roadmap
        loadedRoadmapProjectIDs.insert(project.id)
    }

    func generateRoadmap(projectID: String, prompt: String) async throws {
        isMutating = true
        defer { isMutating = false }
        let trimmed = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        let roadmap = try await client.generateRoadmap(
            projectID: projectID,
            prompt: trimmed.isEmpty ? nil : trimmed
        )
        roadmapsByProjectID[projectID] = roadmap
        loadedRoadmapProjectIDs.insert(projectID)
        await refreshTasksAndOccurrences()
    }

    func saveRoadmapNode(projectID: String, roadmapID: String, draft: RoadmapNodeDraft) async throws {
        let trimmed = draft.title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw ValidationError("请输入节点名称") }
        isMutating = true
        defer { isMutating = false }
        let input = RoadmapNodeInput(
            parentID: draft.parentID,
            title: trimmed,
            description: draft.description.trimmingCharacters(in: .whitespacesAndNewlines),
            nodeType: draft.nodeType,
            position: nil,
            expectedRevision: draft.expectedRevision
        )
        if let nodeID = draft.editingNodeID {
            _ = try await client.updateRoadmapNode(roadmapID: roadmapID, nodeID: nodeID, input: input)
        } else {
            _ = try await client.createRoadmapNode(roadmapID: roadmapID, input: input)
        }
        roadmapsByProjectID[projectID] = try await client.getRoadmap(projectID: projectID)
    }

    func deleteRoadmapNode(projectID: String, roadmapID: String, node: RoadmapNodeV2) async throws {
        isMutating = true
        defer { isMutating = false }
        try await client.deleteRoadmapNode(
            roadmapID: roadmapID,
            nodeID: node.id,
            expectedRevision: node.revision
        )
        roadmapsByProjectID[projectID] = try await client.getRoadmap(projectID: projectID)
    }

    func createTask(title: String, projectID: String, roadmapNodeID: String?) async throws {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw ValidationError("请输入任务标题") }
        isMutating = true
        defer { isMutating = false }
        let schedule = ScheduleInput(
            recurrenceType: .none,
            timingType: .unscheduled,
            timezone: TimeZone.current.identifier,
            startsOn: nil,
            endsOn: nil,
            localStartTime: nil,
            durationMinutes: nil,
            rule: nil
        )
        _ = try await client.createTask(CreateTaskInput(
            projectID: projectID,
            roadmapNodeID: roadmapNodeID,
            title: trimmed,
            description: nil,
            priority: 0,
            sortOrder: 0,
            schedule: schedule
        ))
        await refreshTasksAndOccurrences()
        await loadRoadmap(projectID: projectID, force: true)
    }

    private func refreshTasksAndOccurrences() async {
        do {
            async let loadedTasks = client.listTasks()
            async let loadedOccurrences = client.listOccurrences()
            let result = try await (loadedTasks, loadedOccurrences)
            tasks = result.0
            occurrences = result.1.sorted(by: OccurrenceV2.scheduleAscending)
            scheduleTaskIndexing()
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func search(_ query: String) async throws -> [SearchResultItem] {
        async let remoteResults = client.search(query)
        async let loadedProjects = client.listProjects()
        let result = try await (remoteResults, loadedProjects)
        projects = result.1
        scheduleProjectIndexing()
        let keyword = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let projectResults = result.1
            .filter { $0.name.localizedCaseInsensitiveContains(keyword) }
            .map {
                SearchResultItem(
                    type: "project",
                    id: $0.id,
                    title: $0.name,
                    highlight: $0.kind == .learning ? "学习项目" : "标准项目",
                    folderID: nil,
                    done: nil,
                    kind: $0.kind.rawValue,
                    updatedAt: 0
                )
            }
        return result.0 + projectResults
    }

    func loadSummary(period: SummaryPeriod, now: Date = Date()) async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        let calendar = Calendar.current
        let interval: DateInterval?
        switch period {
        case .week:
            interval = calendar.dateInterval(of: .weekOfYear, for: now)
        case .month:
            interval = calendar.dateInterval(of: .month, for: now)
        }
        guard let interval else { return }
        let inclusiveEnd = calendar.date(byAdding: .day, value: -1, to: interval.end) ?? now

        do {
            async let loadedSummary = client.summary(
                from: Self.dateString(interval.start),
                to: Self.dateString(inclusiveEnd)
            )
            async let loadedNotes = client.listNotes()
            async let today = client.listOccurrences(Self.query(for: .today, now: now))
            async let overdue = client.listOccurrences(Self.query(for: .overdue, now: now))
            let result = try await (loadedSummary, loadedNotes, today, overdue)
            summary = result.0
            notes = result.1
            scheduleNoteIndexing()
            var seen = Set<String>()
            summaryAttention = (result.3 + result.2).filter {
                !$0.executionStatus.isTerminal && seen.insert($0.id).inserted
            }
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func loadCalendar(
        containing date: Date,
        mode: CalendarDisplayMode = .week,
        timezone: TimeZone = .current
    ) async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        var calendar = Calendar.current
        calendar.timeZone = timezone
        let component: Calendar.Component = switch mode {
        case .week: .weekOfYear
        case .month: .month
        case .year: .year
        }
        let interval = calendar.dateInterval(of: component, for: date)
        guard let start = interval?.start, let end = interval?.end else { return }
        do {
            async let loadedProjects = client.listProjects()
            async let loadedEntries = client.listCalendarEntries(from: start, to: end, timezone: timezone)
            let result = try await (loadedProjects, loadedEntries)
            projects = result.0
            calendarEntries = result.1
            scheduleProjectIndexing()
        } catch is CancellationError {
            return
        } catch {
            errorMessage = Self.readable(error)
        }
    }

    func createTask(from draft: TaskDraft) async throws {
        guard !draft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ValidationError("请输入任务标题")
        }
        guard !draft.projectID.isEmpty else {
            throw ValidationError("请选择所属项目")
        }
        guard TaskPriorityLevel(rawValue: draft.priority) != nil else {
            throw ValidationError("优先级无效，请重新选择")
        }

        isMutating = true
        defer { isMutating = false }
        let schedule = Self.schedule(from: draft)
        let response = try await client.createTask(CreateTaskInput(
            projectID: draft.projectID,
            roadmapNodeID: draft.roadmapNodeID,
            title: draft.title.trimmingCharacters(in: .whitespacesAndNewlines),
            description: nil,
            priority: draft.priority,
            sortOrder: 0,
            schedule: schedule
        ))
        if let index = tasks.firstIndex(where: { $0.id == response.task.id }) {
            tasks[index] = response.task
        } else {
            tasks.append(response.task)
        }
        let returnedIDs = Set(response.occurrences.map(\.id))
        occurrences.removeAll { returnedIDs.contains($0.id) }
        occurrences.append(contentsOf: response.occurrences)
        occurrences.sort(by: OccurrenceV2.scheduleAscending)
        scheduleTaskIndexing()
    }

    func createProject(
        name: String,
        kind: ProjectKind,
        horizon: ProjectHorizon,
        status: ProjectStatus
    ) async throws -> ProjectV2 {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty else { throw ValidationError("请输入项目名称") }
        guard status == .planning || status == .active else {
            throw ValidationError("新项目只能设为规划中或进行中")
        }
        isMutating = true
        defer { isMutating = false }
        let project = try await client.createProject(CreateProjectInput(
            name: trimmedName,
            kind: kind,
            horizon: horizon,
            status: status
        ))
        projects.append(project)
        projects.sort { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        scheduleProjectIndexing()
        return project
    }

    func updateProject(
        _ project: ProjectV2,
        name: String,
        kind: ProjectKind,
        horizon: ProjectHorizon
    ) async throws -> ProjectV2 {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty else { throw ValidationError("请输入项目名称") }
        isMutating = true
        defer { isMutating = false }
        let updated = try await client.updateProject(
            projectID: project.id,
            input: UpdateProjectInput(
                name: trimmedName,
                kind: kind,
                horizon: horizon,
                expectedProjectRevision: project.revision
            )
        )
        replaceProject(updated)
        return updated
    }

    func executeProjectCommand(
        _ project: ProjectV2,
        command: ProjectLifecycleCommand,
        restoreTo: ProjectStatus? = nil
    ) async throws {
        isMutating = true
        defer { isMutating = false }
        _ = try await client.projectCommand(
            projectID: project.id,
            command: command,
            input: ProjectCommandInput(
                expectedProjectRevision: project.revision,
                restoreTo: command == .restore ? (restoreTo ?? .active) : nil
            )
        )
        projects = try await client.listProjects()
        scheduleProjectAndTaskIndexing()
    }

    func deleteProject(_ project: ProjectV2) async throws {
        guard project.systemRole == nil || project.systemRole?.isEmpty == true else {
            throw ValidationError("系统项目不能删除")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.deleteProject(
            projectID: project.id,
            input: ProjectCommandInput(expectedProjectRevision: project.revision, restoreTo: nil)
        )
        projects.removeAll { $0.id == project.id }
        scheduleProjectAndTaskIndexing()
    }

    func moveTask(_ task: TaskV2, to projectID: String) async throws {
        guard projects.contains(where: { $0.id == projectID }) else {
            throw ValidationError("目标项目不存在")
        }
        isMutating = true
        defer { isMutating = false }
        let updated = try await client.moveTask(
            taskID: task.id,
            input: MoveTaskInput(
                projectID: projectID,
                expectedTaskRevision: task.revision,
                expectedScheduleRevision: task.scheduleRevision
            )
        )
        if let index = tasks.firstIndex(where: { $0.id == updated.id }) {
            tasks[index] = updated
        }
        scheduleTaskIndexing()
    }

    func updateTaskDefinition(
        _ task: TaskV2,
        title: String,
        description: String,
        priority: Int,
        projectID: String
    ) async throws -> TaskV2 {
        let trimmedTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedTitle.isEmpty else { throw ValidationError("请输入任务标题") }
        guard TaskPriorityLevel(rawValue: priority) != nil else {
            throw ValidationError("优先级无效，请重新选择")
        }
        guard projects.contains(where: { $0.id == projectID }) else {
            throw ValidationError("请选择有效的所属项目")
        }
        isMutating = true
        defer { isMutating = false }
        let updated = try await client.updateTaskDefinition(
            taskID: task.id,
            input: UpdateTaskDefinitionInput(
                title: trimmedTitle,
                description: description.trimmingCharacters(in: .whitespacesAndNewlines),
                priority: priority,
                projectID: projectID,
                taskNoteID: nil,
                expectedTaskRevision: task.revision,
                expectedScheduleRevision: task.scheduleRevision
            )
        )
        if let index = tasks.firstIndex(where: { $0.id == updated.id }) {
            tasks[index] = updated
        }
        scheduleTaskIndexing()
        return updated
    }

    func setTaskNote(_ task: TaskV2, noteID: String?) async throws -> TaskV2 {
        let normalizedNoteID = noteID?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        isMutating = true
        defer { isMutating = false }
        let updated = try await client.updateTaskDefinition(
            taskID: task.id,
            input: UpdateTaskDefinitionInput(
                title: nil,
                description: nil,
                priority: nil,
                projectID: nil,
                taskNoteID: normalizedNoteID,
                expectedTaskRevision: task.revision,
                expectedScheduleRevision: task.scheduleRevision
            )
        )
        if let index = tasks.firstIndex(where: { $0.id == updated.id }) {
            tasks[index] = updated
        } else {
            tasks.append(updated)
        }
        scheduleTaskIndexing()
        return updated
    }

    private func replaceProject(_ project: ProjectV2) {
        if let index = projects.firstIndex(where: { $0.id == project.id }) {
            projects[index] = project
        } else {
            projects.append(project)
        }
        projects.sort { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        scheduleProjectAndTaskIndexing()
    }

    func rescheduleOnlyThis(
        _ entry: CalendarEntryV2,
        timing: OccurrenceTimingInput,
        selectedOffsetSeconds: Int?
    ) async throws {
        guard !entry.executionStatus.isTerminal else {
            throw ValidationError("已完成或已取消的执行不能改期，请先重新打开")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.rescheduleOccurrence(
            occurrenceID: entry.occurrenceID,
            input: RescheduleOccurrenceInput(
                expectedTaskRevision: entry.taskRevision,
                expectedScheduleRevision: entry.scheduleRevision,
                expectedOccurrenceRevision: entry.occurrenceRevision,
                timing: timing,
                selectedOffsets: selectedOffsetSeconds.map { [timing.plannedDate: $0] }
            )
        )
    }

    func rescheduleThisAndFollowing(
        _ entry: CalendarEntryV2,
        effectiveFrom: String,
        generateThroughExclusive: String,
        schedule: ScheduleInput,
        selectedOffsetSeconds: Int?
    ) async throws {
        guard entry.recurring else {
            throw ValidationError("单次任务只能修改本次安排")
        }
        guard !entry.executionStatus.isTerminal else {
            throw ValidationError("已完成或已取消的执行不能改期，请先重新打开")
        }
        guard generateThroughExclusive > effectiveFrom else {
            throw ValidationError("生成截止日期必须晚于生效日期")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.rescheduleThisAndFollowing(
            taskID: entry.taskID,
            input: RescheduleThisAndFollowingInput(
                expectedTaskRevision: entry.taskRevision,
                expectedScheduleRevision: entry.scheduleRevision,
                effectiveFrom: effectiveFrom,
                generateThroughExclusive: generateThroughExclusive,
                schedule: schedule,
                selectedOffsets: selectedOffsetSeconds.map { [effectiveFrom: $0] }
            )
        )
    }

    func reopen(_ entry: CalendarEntryV2) async throws {
        isMutating = true
        defer { isMutating = false }
        _ = try await client.occurrenceCommand(
            occurrenceID: entry.occurrenceID,
            command: "reopen",
            revisions: ExpectedRevisions(
                expectedTaskRevision: entry.taskRevision,
                expectedScheduleRevision: entry.scheduleRevision,
                expectedOccurrenceRevisions: [entry.occurrenceID: entry.occurrenceRevision]
            )
        )
    }

    private func scheduleProjectAndTaskIndexing() {
        scheduleProjectIndexing()
        scheduleTaskIndexing()
    }

    private func scheduleProjectIndexing() {
        guard let workspaceID = spotlightWorkspaceID else { return }
        let generation = spotlightGeneration
        let snapshot = projects
        Task {
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            await spotlight.replaceProjects(snapshot, workspaceID: workspaceID)
        }
    }

    private func scheduleTaskIndexing() {
        guard let workspaceID = spotlightWorkspaceID else { return }
        let generation = spotlightGeneration
        let taskSnapshot = tasks
        let projectSnapshot = projectsByID
        Task {
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            await spotlight.replaceTasks(taskSnapshot, projects: projectSnapshot, workspaceID: workspaceID)
        }
    }

    private func scheduleNoteIndexing() {
        guard let workspaceID = spotlightWorkspaceID else { return }
        let generation = spotlightGeneration
        let snapshot = notes
        Task {
            guard spotlightGeneration == generation, spotlightWorkspaceID == workspaceID else { return }
            await spotlight.replaceNotes(snapshot, workspaceID: workspaceID)
        }
    }

    func toggle(_ occurrence: OccurrenceV2) async throws {
        guard let task = tasksByID[occurrence.taskID] else {
            throw ValidationError("找不到对应的任务定义，请刷新后重试")
        }
        let revisions = ExpectedRevisions(
            expectedTaskRevision: occurrence.taskRevision ?? task.revision,
            expectedScheduleRevision: occurrence.scheduleRevision ?? task.scheduleRevision,
            expectedOccurrenceRevisions: [occurrence.id: occurrence.revision]
        )

        isMutating = true
        defer { isMutating = false }
        let command = occurrence.executionStatus == .done ? "reopen" : "complete"
        _ = try await client.occurrenceCommand(
            occurrenceID: occurrence.id,
            command: command,
            revisions: revisions
        )
        if command == "complete" {
            await notifications.notifyTaskCompleted(title: occurrence.title ?? task.title)
        }
    }

    func start(_ occurrence: OccurrenceV2) async throws {
        guard let task = tasksByID[occurrence.taskID] else {
            throw ValidationError("找不到对应的任务定义，请刷新后重试")
        }
        let revisions = ExpectedRevisions(
            expectedTaskRevision: occurrence.taskRevision ?? task.revision,
            expectedScheduleRevision: occurrence.scheduleRevision ?? task.scheduleRevision,
            expectedOccurrenceRevisions: [occurrence.id: occurrence.revision]
        )
        isMutating = true
        defer { isMutating = false }
        _ = try await client.occurrenceCommand(
            occurrenceID: occurrence.id,
            command: "start",
            revisions: revisions
        )
    }

    func block(_ occurrence: OccurrenceV2, reason: String, nextAction: String) async throws {
        guard let task = tasksByID[occurrence.taskID] else {
            throw ValidationError("找不到对应的任务定义，请刷新后重试")
        }
        let trimmedReason = reason.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedNextAction = nextAction.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedReason.isEmpty, !trimmedNextAction.isEmpty else {
            throw ValidationError("请同时填写阻塞原因和下一步行动")
        }
        guard occurrence.executionStatus == .open || occurrence.executionStatus == .active else {
            throw ValidationError("当前执行状态不能设为阻塞")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.blockOccurrence(
            occurrenceID: occurrence.id,
            input: BlockOccurrenceInput(
                expectedTaskRevision: occurrence.taskRevision ?? task.revision,
                expectedScheduleRevision: occurrence.scheduleRevision ?? task.scheduleRevision,
                expectedOccurrenceRevisions: [occurrence.id: occurrence.revision],
                blockedReason: trimmedReason,
                nextAction: trimmedNextAction
            )
        )
    }

    func unblock(_ occurrence: OccurrenceV2) async throws {
        guard let task = tasksByID[occurrence.taskID] else {
            throw ValidationError("找不到对应的任务定义，请刷新后重试")
        }
        guard occurrence.executionStatus == .blocked else {
            throw ValidationError("只有已阻塞的执行可以解除阻塞")
        }
        isMutating = true
        defer { isMutating = false }
        _ = try await client.occurrenceCommand(
            occurrenceID: occurrence.id,
            command: "unblock",
            revisions: ExpectedRevisions(
                expectedTaskRevision: occurrence.taskRevision ?? task.revision,
                expectedScheduleRevision: occurrence.scheduleRevision ?? task.scheduleRevision,
                expectedOccurrenceRevisions: [occurrence.id: occurrence.revision]
            )
        )
    }

    func executeTaskLifecycle(_ task: TaskV2, command: TaskLifecycleCommand) async throws {
        isMutating = true
        defer { isMutating = false }
        let occurrenceRevisions = try await occurrenceRevisions(
            taskID: task.id,
            required: command == .cancel || command == .archive
        )
        _ = try await client.taskLifecycleCommand(
            taskID: task.id,
            command: command,
            revisions: ExpectedRevisions(
                expectedTaskRevision: task.revision,
                expectedScheduleRevision: task.scheduleRevision,
                expectedOccurrenceRevisions: occurrenceRevisions
            )
        )
    }

    func deleteTaskDefinition(_ task: TaskV2) async throws {
        isMutating = true
        defer { isMutating = false }
        let revisions = try await occurrenceRevisions(taskID: task.id, required: true)
        _ = try await client.deleteTask(
            taskID: task.id,
            revisions: ExpectedRevisions(
                expectedTaskRevision: task.revision,
                expectedScheduleRevision: task.scheduleRevision,
                expectedOccurrenceRevisions: revisions
            )
        )
    }

    private func occurrenceRevisions(taskID: String, required: Bool) async throws -> [String: Int] {
        guard required else { return [:] }
        let query = [
            URLQueryItem(name: "scope", value: "all"),
            URLQueryItem(name: "task_id", value: taskID),
            URLQueryItem(name: "timezone", value: TimeZone.current.identifier),
        ]
        return Dictionary(uniqueKeysWithValues: try await client.listOccurrences(query).map { ($0.id, $0.revision) })
    }

    static func query(for scope: TodayScope, now: Date = Date()) -> [URLQueryItem] {
        let calendar = Calendar.current
        let timezone = TimeZone.current.identifier
        var query = [URLQueryItem(name: "timezone", value: timezone)]

        switch scope {
        case .today:
            let start = calendar.startOfDay(for: now)
            let end = calendar.date(byAdding: .day, value: 1, to: start) ?? now
            query += range(start, end)
            query.append(URLQueryItem(name: "scope", value: "today"))
        case .week:
            if let interval = calendar.dateInterval(of: .weekOfYear, for: now) {
                query += range(interval.start, interval.end)
            }
        case .month:
            if let interval = calendar.dateInterval(of: .month, for: now) {
                query += range(interval.start, interval.end)
            }
        case .overdue:
            query.append(URLQueryItem(name: "scope", value: "overdue"))
            query.append(URLQueryItem(name: "from", value: APIClient.iso8601String(now)))
        case .completed:
            let start = calendar.date(byAdding: .day, value: -30, to: calendar.startOfDay(for: now)) ?? now
            let end = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: now)) ?? now
            query += range(start, end)
            query.append(URLQueryItem(name: "scope", value: "completed"))
        }
        return query
    }

    private static func range(_ start: Date, _ end: Date) -> [URLQueryItem] {
        [
            URLQueryItem(name: "from", value: APIClient.iso8601String(start)),
            URLQueryItem(name: "to", value: APIClient.iso8601String(end)),
        ]
    }

    private static func schedule(from draft: TaskDraft) -> ScheduleInput {
        let dateFormatter = DateFormatter()
        dateFormatter.calendar = Calendar(identifier: .gregorian)
        dateFormatter.locale = Locale(identifier: "en_US_POSIX")
        dateFormatter.timeZone = .current
        dateFormatter.dateFormat = "yyyy-MM-dd"

        let timeFormatter = DateFormatter()
        timeFormatter.calendar = dateFormatter.calendar
        timeFormatter.locale = dateFormatter.locale
        timeFormatter.timeZone = .current
        timeFormatter.dateFormat = "HH:mm"

        return ScheduleInput(
            recurrenceType: draft.recurrenceType,
            timingType: draft.timingType,
            timezone: TimeZone.current.identifier,
            startsOn: draft.timingType == .unscheduled ? nil : dateFormatter.string(from: draft.date),
            endsOn: nil,
            localStartTime: draft.timingType == .timeBlock ? timeFormatter.string(from: draft.date) : nil,
            durationMinutes: draft.timingType == .timeBlock ? draft.durationMinutes : nil,
            rule: draft.recurrenceType == .none ? nil : RecurrenceRule(interval: 1, weekdays: nil, monthDays: nil)
        )
    }

    private static func dateString(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    private static func readable(_ error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
