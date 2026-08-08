import Foundation

struct APIError: LocalizedError, Equatable, Sendable {
    let status: Int
    let code: String
    let message: String
    let retryable: Bool
    let details: APIErrorDetails?

    init(
        status: Int,
        code: String,
        message: String,
        retryable: Bool,
        details: APIErrorDetails? = nil
    ) {
        self.status = status
        self.code = code
        self.message = message
        self.retryable = retryable
        self.details = details
    }

    var errorDescription: String? { message }
    var isUnauthorized: Bool { status == 401 }
    var isRevisionConflict: Bool { status == 409 && code == "revision_conflict" }
}

struct APIErrorDetails: Codable, Equatable, Sendable {
    let offsetCandidates: [ScheduleOffsetCandidate]?
}

private struct APIEnvelope<Value: Decodable>: Decodable {
    let data: Value
}

private struct PaginatedAPIEnvelope<Value: Decodable>: Decodable {
    let data: Value
    let pagination: AccountPagination?
}

private struct APIErrorEnvelope: Decodable {
    struct Payload: Decodable {
        let code: String?
        let message: String?
        let retryable: Bool?
        let details: APIErrorDetails?
    }

    let error: Payload?
}

private struct ProjectListPayload: Decodable { let projects: [ProjectV2] }
private struct ProjectPayload: Decodable { let project: ProjectV2 }
private struct TaskListPayload: Decodable { let tasks: [TaskV2] }
private struct OccurrenceListPayload: Decodable { let occurrences: [OccurrenceV2] }
private struct CalendarListPayload: Decodable { let entries: [CalendarEntryV2] }
private struct NoteListPayload: Decodable { let notes: [FlowNote] }
private struct NotePayload: Decodable { let note: FlowNote }
private struct SearchPayload: Decodable { let items: [SearchResultItem] }
private struct RoadmapPayload: Decodable { let roadmap: RoadmapV2 }
private struct RoadmapNodePayload: Decodable { let node: RoadmapNodeV2 }
private struct AttachmentListPayload: Decodable { let attachments: [NoteAttachment] }
private struct AttachmentPayload: Decodable { let attachment: NoteAttachment }
private struct VoiceTranscriptionPayload: Decodable { let voiceNote: VoiceTranscriptionResult }
private struct PodcastEpisodePayload: Decodable { let episode: ResolvedPodcastEpisode }
private struct ContentImportListPayload: Decodable { let imports: [ContentImport] }
private struct ContentImportPayload: Decodable {
    let item: ContentImport
    enum CodingKeys: String, CodingKey { case item = "import" }
}
private struct SyncTargetListPayload: Decodable { let targets: [SyncTarget] }
private struct SyncTargetPayload: Decodable { let target: SyncTarget }
private struct SyncBatchPayload: Decodable { let result: SyncBatchResult }
private struct TargetSyncPayload: Decodable { let result: TargetSyncResult }
private struct SyncItemPayload: Decodable { let item: SyncResultItem }
private struct AdminUserListPayload: Decodable { let users: [AccountUser] }
private struct AdminUserPayload: Decodable { let user: AccountUser }
private struct AuthProviderPayload: Decodable { let providers: [String] }

actor APIClient {
    let baseURL: URL
    private let session: URLSession
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder

    init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
        decoder = .flowSpace()
        encoder = .flowSpace()
    }

    func health() async throws -> HealthResponse {
        try await requestDirect(path: "/api/health")
    }

    func login(email: String, password: String, rememberMe: Bool) async throws -> LoginPayload {
        struct Body: Encodable { let email: String; let password: String; let rememberMe: Bool }
        return try await requestData(
            path: "/api/auth/login",
            method: "POST",
            body: Body(email: email, password: password, rememberMe: rememberMe)
        )
    }

    func authProviders() async throws -> [String] {
        let payload: AuthProviderPayload = try await requestData(path: "/api/auth/providers")
        return payload.providers
    }

    func githubNativeStartURL(callbackURL: URL, codeChallenge: String) throws -> URL {
        try endpoint(
            path: "/api/auth/github/native/start",
            query: [
                URLQueryItem(name: "callback", value: callbackURL.absoluteString),
                URLQueryItem(name: "code_challenge", value: codeChallenge),
                URLQueryItem(name: "code_challenge_method", value: "S256"),
            ]
        )
    }

    func exchangeGitHubNativeCode(_ code: String, codeVerifier: String) async throws {
        struct Body: Encodable { let code: String; let codeVerifier: String }
        try await requestNoContent(
            path: "/api/auth/github/native/exchange",
            method: "POST",
            body: Body(code: code, codeVerifier: codeVerifier)
        )
    }

    func currentUser() async throws -> CurrentUserPayload {
        try await requestData(path: "/api/auth/me")
    }

    func resetPassword(_ newPassword: String) async throws {
        struct Body: Encodable { let newPassword: String }
        try await requestNoContent(
            path: "/api/auth/reset-password",
            method: "POST",
            body: Body(newPassword: newPassword)
        )
    }

    func userProfile() async throws -> UserProfile {
        try await requestData(path: "/api/settings/profile")
    }

    func updateUserProfile(_ input: UpdateUserProfileInput) async throws -> UserProfile {
        try await requestData(path: "/api/settings/profile", method: "PATCH", body: input)
    }

    func uploadUserAvatar(data: Data, mimeType: String) async throws -> AvatarUploadResult {
        let (responseData, response) = try await perform(
            path: "/api/settings/profile/avatar",
            method: "PUT",
            body: data,
            headers: ["Content-Type": mimeType],
            timeout: 60
        )
        try validate(response: response, data: responseData)
        return try decode(APIEnvelope<AvatarUploadResult>.self, from: responseData).data
    }

    func userAvatar() async throws -> Data {
        let (data, response) = try await perform(
            path: "/api/settings/profile/avatar",
            method: "GET",
            body: nil,
            headers: ["Accept": "image/*"]
        )
        try validate(response: response, data: data)
        return data
    }

    func deleteUserAvatar() async throws {
        try await requestNoContent(
            path: "/api/settings/profile/avatar",
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func listAdminUsers(page: Int, pageSize: Int, queryText: String) async throws -> AdminUserPage {
        let (data, response) = try await perform(
            path: "/api/admin/users",
            query: [
                URLQueryItem(name: "page", value: String(page)),
                URLQueryItem(name: "page_size", value: String(pageSize)),
                URLQueryItem(name: "q", value: queryText),
            ],
            method: "GET",
            body: nil
        )
        try validate(response: response, data: data)
        let envelope = try decode(PaginatedAPIEnvelope<AdminUserListPayload>.self, from: data)
        return AdminUserPage(
            users: envelope.data.users,
            pagination: envelope.pagination ?? AccountPagination(
                page: page,
                pageSize: pageSize,
                total: envelope.data.users.count
            )
        )
    }

    func createAdminUser(_ input: CreateAdminUserInput) async throws -> AccountUser {
        let payload: AdminUserPayload = try await requestData(
            path: "/api/admin/users",
            method: "POST",
            body: input
        )
        return payload.user
    }

    func updateAdminUser(id: String, input: UpdateAdminUserInput) async throws -> AccountUser {
        let payload: AdminUserPayload = try await requestData(
            path: "/api/admin/users/\(id.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.user
    }

    func resetAdminUserPassword(id: String, temporaryPassword: String) async throws {
        struct Body: Encodable { let temporaryPassword: String }
        try await requestNoContent(
            path: "/api/admin/users/\(id.urlPathEncoded)/reset-password",
            method: "POST",
            body: Body(temporaryPassword: temporaryPassword)
        )
    }

    func setAdminUserStatus(id: String, status: AccountStatus) async throws {
        let action = status == .active ? "enable" : "disable"
        try await requestNoContent(
            path: "/api/admin/users/\(id.urlPathEncoded)/\(action)",
            method: "POST",
            body: Optional<String>.none
        )
    }

    func logout() async throws {
        try await requestNoContent(path: "/api/auth/logout", method: "POST", body: Optional<String>.none)
    }

    func clearCookiesForCurrentService() {
        let storage = session.configuration.httpCookieStorage ?? HTTPCookieStorage.shared
        for cookie in storage.cookies(for: baseURL) ?? [] {
            storage.deleteCookie(cookie)
        }
    }

    func capabilities() async throws -> TaskDomainCapabilities {
        try await requestDirect(path: "/api/task-domain/capabilities")
    }

    func listProjects() async throws -> [ProjectV2] {
        let payload: ProjectListPayload = try await requestData(path: "/api/projects")
        return payload.projects
    }

    func createProject(_ input: CreateProjectInput) async throws -> ProjectV2 {
        let payload: ProjectPayload = try await requestData(path: "/api/projects", method: "POST", body: input)
        return payload.project
    }

    func updateProject(projectID: String, input: UpdateProjectInput) async throws -> ProjectV2 {
        let payload: ProjectPayload = try await requestData(
            path: "/api/projects/\(projectID.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.project
    }

    func projectCommand(
        projectID: String,
        command: ProjectLifecycleCommand,
        input: ProjectCommandInput
    ) async throws -> ProjectCommandResponse {
        try await requestData(
            path: "/api/projects/\(projectID.urlPathEncoded)/\(command.rawValue)",
            method: "POST",
            body: input
        )
    }

    func deleteProject(projectID: String, input: ProjectCommandInput) async throws -> ProjectCommandResponse {
        try await requestData(
            path: "/api/projects/\(projectID.urlPathEncoded)",
            method: "DELETE",
            body: input
        )
    }

    func moveTask(taskID: String, input: MoveTaskInput) async throws -> TaskV2 {
        struct Payload: Decodable { let task: TaskV2 }
        let payload: Payload = try await requestData(
            path: "/api/tasks/\(taskID.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.task
    }

    func updateTaskDefinition(taskID: String, input: UpdateTaskDefinitionInput) async throws -> TaskV2 {
        struct Payload: Decodable { let task: TaskV2 }
        let payload: Payload = try await requestData(
            path: "/api/tasks/\(taskID.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.task
    }

    func listTasks(
        projectID: String? = nil,
        lifecycleStatus: TaskLifecycleStatus? = nil
    ) async throws -> [TaskV2] {
        var query: [URLQueryItem] = []
        if let projectID { query.append(URLQueryItem(name: "project_id", value: projectID)) }
        if let lifecycleStatus {
            query.append(URLQueryItem(name: "lifecycle_status", value: lifecycleStatus.rawValue))
        }
        let payload: TaskListPayload = try await requestData(path: "/api/tasks", query: query)
        return payload.tasks
    }

    func listOccurrences(_ query: [URLQueryItem] = []) async throws -> [OccurrenceV2] {
        let payload: OccurrenceListPayload = try await requestData(path: "/api/task-occurrences", query: query)
        return payload.occurrences
    }

    func listCalendarEntries(from: Date, to: Date, timezone: TimeZone) async throws -> [CalendarEntryV2] {
        let query = [
            URLQueryItem(name: "from", value: APIClient.iso8601String(from)),
            URLQueryItem(name: "to", value: APIClient.iso8601String(to)),
            URLQueryItem(name: "timezone", value: timezone.identifier),
        ]
        let payload: CalendarListPayload = try await requestData(path: "/api/calendar/entries", query: query)
        return payload.entries
    }

    func createTask(_ input: CreateTaskInput) async throws -> CreateTaskResponse {
        try await requestData(path: "/api/tasks", method: "POST", body: input)
    }

    func rescheduleOccurrence(
        occurrenceID: String,
        input: RescheduleOccurrenceInput
    ) async throws -> ScheduleCommandResponse {
        try await requestData(
            path: "/api/task-occurrences/\(occurrenceID.urlPathEncoded)/schedule/only-this",
            method: "PATCH",
            body: input
        )
    }

    func rescheduleThisAndFollowing(
        taskID: String,
        input: RescheduleThisAndFollowingInput
    ) async throws -> ScheduleCommandResponse {
        try await requestData(
            path: "/api/tasks/\(taskID.urlPathEncoded)/schedule/this-and-following",
            method: "POST",
            body: input
        )
    }

    func getRoadmap(projectID: String) async throws -> RoadmapV2? {
        do {
            let payload: RoadmapPayload = try await requestData(
                path: "/api/projects/\(projectID.urlPathEncoded)/roadmap"
            )
            return payload.roadmap
        } catch let error as APIError where error.status == 404 {
            return nil
        }
    }

    func createRoadmap(projectID: String, input: RoadmapCreateInput) async throws -> RoadmapV2 {
        let payload: RoadmapPayload = try await requestData(
            path: "/api/projects/\(projectID.urlPathEncoded)/roadmap",
            method: "POST",
            body: input
        )
        return payload.roadmap
    }

    func generateRoadmap(projectID: String, prompt: String?) async throws -> RoadmapV2 {
        let body = RoadmapGenerateInput(prompt: prompt)
        let encoded = try encoder.encode(body)
        let (data, response) = try await perform(
            path: "/api/projects/\(projectID.urlPathEncoded)/roadmap/generate",
            method: "POST",
            body: encoded,
            timeout: 140
        )
        try validate(response: response, data: data)
        return try decode(APIEnvelope<RoadmapPayload>.self, from: data).data.roadmap
    }

    func createRoadmapNode(roadmapID: String, input: RoadmapNodeInput) async throws -> RoadmapNodeV2 {
        let payload: RoadmapNodePayload = try await requestData(
            path: "/api/roadmaps/\(roadmapID.urlPathEncoded)/nodes",
            method: "POST",
            body: input
        )
        return payload.node
    }

    func updateRoadmapNode(roadmapID: String, nodeID: String, input: RoadmapNodeInput) async throws -> RoadmapNodeV2 {
        let payload: RoadmapNodePayload = try await requestData(
            path: "/api/roadmaps/\(roadmapID.urlPathEncoded)/nodes/\(nodeID.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.node
    }

    func deleteRoadmapNode(roadmapID: String, nodeID: String, expectedRevision: Int) async throws {
        try await requestNoContent(
            path: "/api/roadmaps/\(roadmapID.urlPathEncoded)/nodes/\(nodeID.urlPathEncoded)",
            query: [URLQueryItem(name: "expected_revision", value: String(expectedRevision))],
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func listNotes(sort: String = "recent", projectID: String? = nil) async throws -> [FlowNote] {
        var query = [
            URLQueryItem(name: "sort", value: sort),
            URLQueryItem(name: "page", value: "1"),
            URLQueryItem(name: "page_size", value: "100"),
        ]
        if let projectID { query.append(URLQueryItem(name: "project_id", value: projectID)) }
        let payload: NoteListPayload = try await requestData(path: "/api/notes", query: query)
        return payload.notes
    }

    func getNote(id: String) async throws -> FlowNote {
        let payload: NotePayload = try await requestData(path: "/api/notes/\(id.urlPathEncoded)")
        return payload.note
    }

    func createNote(_ input: CreateNoteInput) async throws -> FlowNote {
        let payload: NotePayload = try await requestData(path: "/api/notes", method: "POST", body: input)
        return payload.note
    }

    func updateNote(id: String, input: UpdateNoteInput) async throws -> FlowNote {
        let payload: NotePayload = try await requestData(
            path: "/api/notes/\(id.urlPathEncoded)",
            method: "PATCH",
            body: input
        )
        return payload.note
    }

    func deleteNote(id: String) async throws {
        try await requestNoContent(
            path: "/api/notes/\(id.urlPathEncoded)",
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func listNoteAttachments(noteID: String) async throws -> [NoteAttachment] {
        let payload: AttachmentListPayload = try await requestData(
            path: "/api/notes/\(noteID.urlPathEncoded)/attachments"
        )
        return payload.attachments
    }

    func uploadNoteAttachment(
        noteID: String,
        fileName: String,
        mimeType: String,
        data: Data
    ) async throws -> NoteAttachment {
        let encodedName = fileName.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? fileName
        let (responseData, response) = try await perform(
            path: "/api/notes/\(noteID.urlPathEncoded)/attachments",
            method: "POST",
            body: data,
            headers: [
                "Content-Type": mimeType.isEmpty ? "application/octet-stream" : mimeType,
                "X-File-Name": encodedName,
            ],
            timeout: 120
        )
        try validate(response: response, data: responseData)
        return try decode(APIEnvelope<AttachmentPayload>.self, from: responseData).data.attachment
    }

    func deleteNoteAttachment(noteID: String, attachmentID: String) async throws {
        try await requestNoContent(
            path: "/api/notes/\(noteID.urlPathEncoded)/attachments/\(attachmentID.urlPathEncoded)",
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func transcribeVoiceNote(clientID: String, language: String = "") async throws -> VoiceTranscriptionResult {
        struct Body: Encodable { let language: String }
        let payload: VoiceTranscriptionPayload = try await requestData(
            path: "/api/voice-notes/\(clientID.urlPathEncoded)/transcription",
            method: "POST",
            body: Body(language: language),
            timeout: 20 * 60
        )
        return payload.voiceNote
    }

    func annotateJapanese(_ text: String) async throws -> FuriganaResult {
        struct Body: Encodable { let text: String }
        return try await requestData(path: "/api/japanese/furigana", method: "POST", body: Body(text: text))
    }

    func resolvePodcast(sourceURL: String) async throws -> ResolvedPodcastEpisode {
        struct Body: Encodable { let sourceURL: String }
        let payload: PodcastEpisodePayload = try await requestData(
            path: "/api/content-imports/resolve",
            method: "POST",
            body: Body(sourceURL: sourceURL)
        )
        return payload.episode
    }

    func createContentImport(_ input: CreateContentImportInput) async throws -> ContentImport {
        let payload: ContentImportPayload = try await requestData(
            path: "/api/content-imports",
            method: "POST",
            body: input,
            headers: ["Idempotency-Key": UUID().uuidString]
        )
        return payload.item
    }

    func listContentImports(status: String = "all") async throws -> [ContentImport] {
        let payload: ContentImportListPayload = try await requestData(
            path: "/api/content-imports",
            query: [
                URLQueryItem(name: "status", value: status),
                URLQueryItem(name: "page", value: "1"),
                URLQueryItem(name: "page_size", value: "100"),
            ]
        )
        return payload.imports
    }

    func cancelContentImport(id: String) async throws -> ContentImport {
        let payload: ContentImportPayload = try await requestData(
            path: "/api/content-imports/\(id.urlPathEncoded)/cancel",
            method: "POST"
        )
        return payload.item
    }

    func retryContentImport(id: String) async throws -> ContentImport {
        let payload: ContentImportPayload = try await requestData(
            path: "/api/content-imports/\(id.urlPathEncoded)/retry",
            method: "POST",
            headers: ["Idempotency-Key": UUID().uuidString]
        )
        return payload.item
    }

    func deleteContentImport(id: String) async throws {
        try await requestNoContent(
            path: "/api/content-imports/\(id.urlPathEncoded)",
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func listSyncTargets() async throws -> [SyncTarget] {
        let payload: SyncTargetListPayload = try await requestData(path: "/api/sync/targets")
        return payload.targets
    }

    func saveSyncTarget(id: String?, input: SaveSyncTargetInput) async throws -> SyncTarget {
        let payload: SyncTargetPayload
        if let id {
            payload = try await requestData(
                path: "/api/sync/targets/\(id.urlPathEncoded)",
                method: "PATCH",
                body: input
            )
        } else {
            payload = try await requestData(path: "/api/sync/targets", method: "POST", body: input)
        }
        return payload.target
    }

    func deleteSyncTarget(id: String) async throws {
        try await requestNoContent(
            path: "/api/sync/targets/\(id.urlPathEncoded)",
            method: "DELETE",
            body: Optional<String>.none
        )
    }

    func testSyncTarget(input: SaveSyncTargetInput) async throws {
        let typePath = input.type == .notion ? "notion" : "obsidian"
        try await requestNoContent(
            path: "/api/sync/\(typePath)/test",
            method: "POST",
            body: input
        )
    }

    func pushSyncTarget(id: String) async throws -> SyncBatchResult {
        let payload: SyncBatchPayload = try await requestData(
            path: "/api/sync/targets/\(id.urlPathEncoded)/push",
            method: "POST"
        )
        return payload.result
    }

    func pullSyncTarget(id: String) async throws -> TargetSyncResult {
        let payload: TargetSyncPayload = try await requestData(
            path: "/api/sync/targets/\(id.urlPathEncoded)/pull",
            method: "POST"
        )
        return payload.result
    }

    func getNoteSyncBinding(noteID: String) async throws -> NoteSyncBindingResponse {
        try await requestData(path: "/api/notes/\(noteID.urlPathEncoded)/sync-binding")
    }

    func saveNoteSyncBinding(
        noteID: String,
        input: SaveNoteSyncBindingInput
    ) async throws -> SaveNoteSyncBindingResponse {
        try await requestData(
            path: "/api/notes/\(noteID.urlPathEncoded)/sync-binding",
            method: "PUT",
            body: input
        )
    }

    func deleteNoteSyncBinding(noteID: String, input: DeleteNoteSyncBindingInput) async throws {
        try await requestNoContent(
            path: "/api/notes/\(noteID.urlPathEncoded)/sync-binding",
            method: "DELETE",
            body: input
        )
    }

    func syncNote(noteID: String) async throws -> SyncResultItem {
        let payload: SyncItemPayload = try await requestData(
            path: "/api/sync/notes/\(noteID.urlPathEncoded)",
            method: "POST"
        )
        return payload.item
    }

    func runtimeSettings() async throws -> RuntimeSettings {
        try await requestData(path: "/api/settings/runtime")
    }

    func testServiceProfile(_ input: ProfileTestInput) async throws -> ProfileTestResult {
        try await requestData(path: "/api/settings/profiles/test", method: "POST", body: input)
    }

    func saveServiceProfile(_ input: ProfileDraftInput) async throws -> SavedProfile {
        try await requestData(path: "/api/settings/profiles", method: "POST", body: input)
    }

    func verifyServiceProfile(kind: ServiceKind, versionID: String) async throws -> VerifiedProfile {
        struct Empty: Encodable {}
        return try await requestData(
            path: "/api/settings/profiles/\(kind.rawValue.urlPathEncoded)/\(versionID.urlPathEncoded)/verify",
            method: "POST",
            body: Empty()
        )
    }

    func setServiceBinding(kind: ServiceKind, input: SetServiceBindingInput) async throws -> ServiceBinding {
        try await requestData(
            path: "/api/settings/bindings/\(kind.rawValue.urlPathEncoded)",
            method: "PUT",
            body: input
        )
    }

    func startCodexSubscription() async throws -> CodexDeviceAuthorization {
        struct Empty: Encodable {}
        return try await requestData(
            path: "/api/settings/ai/codex/device/start",
            method: "POST",
            body: Empty()
        )
    }

    func pollCodexSubscription(
        flowID: String,
        expectedRevision: Int64,
        expectedRuntimeRevision: Int64
    ) async throws -> CodexPollResult {
        struct Body: Encodable {
            let expectedRevision: Int64
            let expectedRuntimeRevision: Int64
        }
        return try await requestData(
            path: "/api/settings/ai/codex/device/\(flowID.urlPathEncoded)/poll",
            method: "POST",
            body: Body(
                expectedRevision: expectedRevision,
                expectedRuntimeRevision: expectedRuntimeRevision
            )
        )
    }

    func search(_ queryText: String) async throws -> [SearchResultItem] {
        let payload: SearchPayload = try await requestData(
            path: "/api/search",
            query: [
                URLQueryItem(name: "q", value: queryText),
                URLQueryItem(name: "page", value: "1"),
                URLQueryItem(name: "page_size", value: "50"),
            ]
        )
        return payload.items
    }

    func summary(from: String, to: String) async throws -> SummaryData {
        try await requestData(
            path: "/api/summary",
            query: [
                URLQueryItem(name: "from", value: from),
                URLQueryItem(name: "to", value: to),
                URLQueryItem(name: "page", value: "1"),
                URLQueryItem(name: "page_size", value: "100"),
            ]
        )
    }

    func occurrenceCommand(
        occurrenceID: String,
        command: String,
        revisions: ExpectedRevisions
    ) async throws -> TaskCommandResponse {
        try await requestData(
            path: "/api/task-occurrences/\(occurrenceID.urlPathEncoded)/\(command)",
            method: "POST",
            body: revisions
        )
    }

    func blockOccurrence(
        occurrenceID: String,
        input: BlockOccurrenceInput
    ) async throws -> TaskCommandResponse {
        try await requestData(
            path: "/api/task-occurrences/\(occurrenceID.urlPathEncoded)/block",
            method: "POST",
            body: input
        )
    }

    func taskLifecycleCommand(
        taskID: String,
        command: TaskLifecycleCommand,
        revisions: ExpectedRevisions
    ) async throws -> TaskCommandResponse {
        try await requestData(
            path: "/api/tasks/\(taskID.urlPathEncoded)/\(command.rawValue)",
            method: "POST",
            body: revisions
        )
    }

    func deleteTask(taskID: String, revisions: ExpectedRevisions) async throws -> TaskDeleteResponse {
        try await requestData(
            path: "/api/tasks/\(taskID.urlPathEncoded)",
            method: "DELETE",
            body: revisions
        )
    }

    private func requestData<Value: Decodable>(
        path: String,
        query: [URLQueryItem] = []
    ) async throws -> Value {
        let (data, response) = try await perform(path: path, query: query, method: "GET", body: nil)
        try validate(response: response, data: data)
        return try decode(APIEnvelope<Value>.self, from: data).data
    }

    private func requestData<Value: Decodable, Body: Encodable>(
        path: String,
        method: String,
        body: Body,
        headers: [String: String] = [:],
        timeout: TimeInterval = 20
    ) async throws -> Value {
        let encoded = try encoder.encode(body)
        let (data, response) = try await perform(
            path: path,
            method: method,
            body: encoded,
            headers: headers,
            timeout: timeout
        )
        try validate(response: response, data: data)
        return try decode(APIEnvelope<Value>.self, from: data).data
    }

    private func requestData<Value: Decodable>(
        path: String,
        method: String,
        headers: [String: String] = [:]
    ) async throws -> Value {
        let (data, response) = try await perform(
            path: path,
            method: method,
            body: nil,
            headers: headers
        )
        try validate(response: response, data: data)
        return try decode(APIEnvelope<Value>.self, from: data).data
    }

    private func requestDirect<Value: Decodable>(path: String) async throws -> Value {
        let (data, response) = try await perform(path: path, method: "GET", body: nil)
        try validate(response: response, data: data)
        return try decode(Value.self, from: data)
    }

    private func requestNoContent<Body: Encodable>(
        path: String,
        query: [URLQueryItem] = [],
        method: String,
        body: Body?
    ) async throws {
        let encoded = try body.map { try encoder.encode($0) }
        let (data, response) = try await perform(path: path, query: query, method: method, body: encoded)
        try validate(response: response, data: data)
    }

    private func perform(
        path: String,
        query: [URLQueryItem] = [],
        method: String,
        body: Data?,
        headers: [String: String] = [:],
        timeout: TimeInterval = 20
    ) async throws -> (Data, HTTPURLResponse) {
        let url = try endpoint(path: path, query: query)
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.timeoutInterval = timeout
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if body != nil { request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        for (field, value) in headers { request.setValue(value, forHTTPHeaderField: field) }

        do {
            let (data, response) = try await session.data(for: request)
            guard let httpResponse = response as? HTTPURLResponse else {
                throw APIError(status: 0, code: "invalid_response", message: "服务返回了无效响应", retryable: true)
            }
            return (data, httpResponse)
        } catch let error as APIError {
            throw error
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            throw APIError(status: 0, code: "network_error", message: error.localizedDescription, retryable: true)
        }
    }

    private func endpoint(path: String, query: [URLQueryItem]) throws -> URL {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else {
            throw APIError(status: 0, code: "invalid_url", message: "服务地址无效", retryable: false)
        }
        let basePath = components.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components.path = (basePath.isEmpty ? "" : "/\(basePath)") + "/" + path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components.queryItems = query.isEmpty ? nil : query
        guard let url = components.url else {
            throw APIError(status: 0, code: "invalid_url", message: "无法构造请求地址", retryable: false)
        }
        return url
    }

    private func validate(response: HTTPURLResponse, data: Data) throws {
        guard (200..<300).contains(response.statusCode) else {
            let envelope = try? decoder.decode(APIErrorEnvelope.self, from: data)
            throw APIError(
                status: response.statusCode,
                code: envelope?.error?.code ?? "unknown_error",
                message: envelope?.error?.message ?? HTTPURLResponse.localizedString(forStatusCode: response.statusCode),
                retryable: envelope?.error?.retryable ?? (response.statusCode >= 500),
                details: envelope?.error?.details
            )
        }
    }

    private func decode<Value: Decodable>(_ type: Value.Type, from data: Data) throws -> Value {
        do {
            return try decoder.decode(type, from: data)
        } catch {
            throw APIError(status: 0, code: "invalid_response", message: "无法解析服务响应：\(error.localizedDescription)", retryable: false)
        }
    }

    nonisolated static func iso8601String(_ date: Date) -> String {
        date.ISO8601Format()
    }
}

private extension String {
    var urlPathEncoded: String {
        addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? self
    }
}
