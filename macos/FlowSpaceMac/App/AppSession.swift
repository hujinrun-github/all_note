import Foundation
import Observation

enum WorkspaceEntitySelection: Codable, Equatable, Hashable, Sendable {
    case task(String)
    case project(String)
    case occurrence(String)
    case note(String)
}

@MainActor
@Observable
final class AppSession {
    enum Phase: Equatable {
        case starting
        case connection
        case login
        case passwordChange
        case checkingCapabilities
        case unsupported(String)
        case ready
    }

    private(set) var phase: Phase = .starting
    private(set) var client: APIClient?
    private(set) var workspaceStore: WorkspaceStore?
    private(set) var currentUser: CurrentUserPayload?
    private(set) var baseURL: URL?
    private(set) var authProviders: Set<String> = []
    private(set) var menuBarNextTask: MenuBarTaskSummary?
    private(set) var menuBarRefreshError: String?
    private(set) var pendingWorkspaceDestination: WorkspaceDestination?
    private(set) var pendingWorkspaceEntitySelection: WorkspaceEntitySelection?
    let notifications: AppNotificationService
    let spotlight: AppSpotlightService
    let connectivity: ServiceConnectivity
    var errorMessage: String?
    var isWorking = false
    var contentImportPresentation: ContentImportPresentation?
    var menuBarExtraEnabled: Bool {
        didSet { defaults.set(menuBarExtraEnabled, forKey: MenuBarPreferences.enabledKey) }
    }

    var isAdmin: Bool { currentUser?.user.role == AccountRole.admin.rawValue }
    var isGitHubLoginAvailable: Bool { authProviders.contains("github") }

    private let defaults: UserDefaults
    private let recentCache: RecentWorkspaceCache
    private let githubAuthentication = GitHubAuthenticationSession()
    private var loadedAuthProviders = false
    private var isRefreshingMenuBar = false
    private static let serviceAddressKey = "serviceAddress"

    init(
        defaults: UserDefaults = .standard,
        notifications: AppNotificationService = .shared,
        spotlight: AppSpotlightService = .shared,
        connectivity: ServiceConnectivity = ServiceConnectivity(),
        recentCache: RecentWorkspaceCache = RecentWorkspaceCache()
    ) {
        if ProcessInfo.processInfo.environment["FLOWSPACE_UI_TESTING"] == "1" {
            let suiteName = "com.flowspace.mac.ui-testing"
            let testingDefaults = UserDefaults(suiteName: suiteName) ?? defaults
            testingDefaults.removePersistentDomain(forName: suiteName)
            self.defaults = testingDefaults
        } else {
            self.defaults = defaults
        }
        self.notifications = notifications
        self.spotlight = spotlight
        self.connectivity = connectivity
        self.recentCache = recentCache
        menuBarExtraEnabled = self.defaults.bool(forKey: MenuBarPreferences.enabledKey)
    }

    func restore() async {
        guard phase == .starting else { return }
        guard let stored = defaults.string(forKey: Self.serviceAddressKey) else {
            phase = .connection
            return
        }

        do {
            let url = try ServerAddress.normalize(stored)
            configure(url)
            guard let client else { return }
            _ = try await client.health()
            let user = try await client.currentUser()
            currentUser = user
            cacheCurrentUser(user)
            if user.mustChangePassword || user.user.mustChangePassword {
                phase = .passwordChange
            } else {
                await verifyCapabilities()
            }
        } catch let error as APIError where error.isUnauthorized {
            phase = .login
        } catch let error as APIError where error.code == "network_error" && restoreCachedWorkspace() {
            return
        } catch {
            errorMessage = readable(error)
            phase = .connection
        }
    }

    func connect(_ rawAddress: String) async {
        isWorking = true
        errorMessage = nil
        defer { isWorking = false }

        do {
            let url = try ServerAddress.normalize(rawAddress)
            connectivity.reset()
            let candidate = makeClient(baseURL: url)
            let health = try await candidate.health()
            guard health.status == "ok" else {
                throw ValidationError("服务尚未就绪")
            }
            defaults.set(url.absoluteString, forKey: Self.serviceAddressKey)
            configure(url, client: candidate)

            do {
                let user = try await candidate.currentUser()
                currentUser = user
                cacheCurrentUser(user)
                if user.mustChangePassword || user.user.mustChangePassword {
                    phase = .passwordChange
                } else {
                    await verifyCapabilities()
                }
            } catch let error as APIError where error.isUnauthorized {
                phase = .login
            }
        } catch {
            errorMessage = readable(error)
        }
    }

    func login(email: String, password: String, rememberMe: Bool) async {
        guard let client else { return }
        isWorking = true
        errorMessage = nil
        defer { isWorking = false }

        do {
            let payload = try await client.login(
                email: email.trimmingCharacters(in: .whitespacesAndNewlines),
                password: password,
                rememberMe: rememberMe
            )
            if payload.user.mustChangePassword {
                phase = .passwordChange
            } else {
                currentUser = try await client.currentUser()
                cacheCurrentUser()
                await verifyCapabilities()
            }
        } catch {
            errorMessage = readable(error)
        }
    }

    func loadAuthProviders() async {
        guard phase == .login, !loadedAuthProviders, let client else { return }
        loadedAuthProviders = true
        authProviders = Set((try? await client.authProviders()) ?? [])
    }

    func loginWithGitHub() async {
        guard let client else { return }
        isWorking = true
        errorMessage = nil
        defer { isWorking = false }

        do {
            let pkce = try NativeOAuthPKCE.generate()
            let startURL = try await client.githubNativeStartURL(
                callbackURL: GitHubNativeOAuthCallback.url,
                codeChallenge: pkce.challenge
            )
            let callbackURL = try await githubAuthentication.authenticate(startURL: startURL)
            let code = try GitHubNativeOAuthCallback.exchangeCode(from: callbackURL)
            try await client.exchangeGitHubNativeCode(code, codeVerifier: pkce.verifier)
            currentUser = try await client.currentUser()
            cacheCurrentUser()
            if currentUser?.mustChangePassword == true || currentUser?.user.mustChangePassword == true {
                phase = .passwordChange
            } else {
                await verifyCapabilities()
            }
        } catch {
            if GitHubAuthenticationSession.isCanceled(error) { return }
            errorMessage = readable(error)
        }
    }

    func resetRequiredPassword(_ password: String) async {
        guard let client else { return }
        isWorking = true
        errorMessage = nil
        defer { isWorking = false }

        do {
            try await client.resetPassword(password)
            currentUser = try await client.currentUser()
            cacheCurrentUser()
            await verifyCapabilities()
        } catch {
            errorMessage = readable(error)
        }
    }

    func retryCapabilityCheck() async {
        await verifyCapabilities()
    }

    func logout() async {
        let workspaceID = currentUser?.workspace.id
        if let baseURL { recentCache.remove(for: baseURL) }
        workspaceStore?.clear()
        if let client { try? await client.logout() }
        if let workspaceID {
            await spotlight.deleteWorkspace(workspaceID)
        }
        currentUser = nil
        authProviders = []
        loadedAuthProviders = false
        clearMenuBarState()
        phase = .login
    }

    func changeService() async {
        await clearCurrentService()
        defaults.removeObject(forKey: Self.serviceAddressKey)
        phase = .connection
    }

    func testService(_ rawAddress: String) async throws -> URL {
        let url = try ServerAddress.normalize(rawAddress)
        let candidate = APIClient(baseURL: url, session: ServiceProbeSession.make())
        let health = try await candidate.health()
        guard health.status == "ok" else {
            throw ValidationError("服务尚未就绪")
        }
        return url
    }

    func switchService(to url: URL) async {
        await clearCurrentService()
        let candidate = makeClient(baseURL: url)
        await candidate.clearCookiesForCurrentService()
        defaults.set(url.absoluteString, forKey: Self.serviceAddressKey)
        configure(url, client: candidate)
        phase = .login
    }

    private func clearCurrentService() async {
        let workspaceID = currentUser?.workspace.id
        if let baseURL { recentCache.remove(for: baseURL) }
        workspaceStore?.clear()
        if let client { await client.clearCookiesForCurrentService() }
        if let workspaceID {
            await spotlight.deleteWorkspace(workspaceID)
        }
        currentUser = nil
        client = nil
        workspaceStore = nil
        baseURL = nil
        connectivity.reset()
        clearMenuBarState()
        errorMessage = nil
    }

    func handleUnauthorized() {
        let workspaceID = currentUser?.workspace.id
        if let baseURL { recentCache.remove(for: baseURL) }
        workspaceStore?.clear()
        currentUser = nil
        authProviders = []
        loadedAuthProviders = false
        clearMenuBarState()
        errorMessage = "登录已过期，请重新登录"
        phase = .login
        if let workspaceID {
            Task { await spotlight.deleteWorkspace(workspaceID) }
        }
    }

    func presentPodcastImport(projectID: String = "") {
        contentImportPresentation = ContentImportPresentation(projectID: projectID)
    }

    func refreshCurrentUser() async throws {
        guard let client else { return }
        currentUser = try await client.currentUser()
        cacheCurrentUser()
    }

    func refreshMenuBarSummary() async {
        guard phase == .ready, let client, !isRefreshingMenuBar else { return }
        isRefreshingMenuBar = true
        defer { isRefreshingMenuBar = false }
        do {
            async let tasks = client.listTasks()
            async let occurrences = client.listOccurrences(WorkspaceStore.query(for: .today))
            let loaded = try await (tasks, occurrences)
            menuBarNextTask = MenuBarTaskSummaryBuilder.next(
                occurrences: loaded.1,
                tasks: loaded.0
            )
            menuBarRefreshError = nil
        } catch is CancellationError {
            return
        } catch let error as APIError where error.isUnauthorized {
            handleUnauthorized()
        } catch {
            menuBarRefreshError = readable(error)
        }
    }

    func retryConnection() async {
        guard let client else { return }
        connectivity.beginChecking()
        do {
            _ = try await client.health()
            if phase == .ready {
                await workspaceStore?.load(scope: .today)
            }
        } catch is CancellationError {
            return
        } catch {
            errorMessage = readable(error)
        }
    }

    func requestWorkspaceDestination(_ destination: WorkspaceDestination) {
        pendingWorkspaceDestination = destination
    }

    func consumeWorkspaceDestination() {
        pendingWorkspaceDestination = nil
    }

    func requestWorkspaceEntitySelection(_ selection: WorkspaceEntitySelection) {
        pendingWorkspaceEntitySelection = selection
    }

    func consumeWorkspaceEntitySelection() {
        pendingWorkspaceEntitySelection = nil
    }

    private func configure(_ url: URL, client: APIClient? = nil) {
        connectivity.beginChecking()
        let configuredClient = client ?? makeClient(baseURL: url)
        baseURL = url
        self.client = configuredClient
        workspaceStore = WorkspaceStore(
            client: configuredClient,
            connectivity: connectivity,
            notifications: notifications,
            spotlight: spotlight,
            recentCache: recentCache,
            serviceURL: url
        )
        authProviders = []
        loadedAuthProviders = false
        clearMenuBarState()
    }

    private func makeClient(baseURL: URL) -> APIClient {
        let connectivity = connectivity
        return APIClient(
            baseURL: baseURL,
            connectionObserver: { event in
                await connectivity.receive(event)
            },
            mutationAllowed: {
                await connectivity.allowsMutations
            }
        )
    }

    private func clearMenuBarState() {
        menuBarNextTask = nil
        menuBarRefreshError = nil
        pendingWorkspaceDestination = nil
        pendingWorkspaceEntitySelection = nil
        isRefreshingMenuBar = false
    }

    private func verifyCapabilities() async {
        guard let client else { return }
        phase = .checkingCapabilities
        errorMessage = nil
        do {
            let capabilities = try await client.capabilities()
            guard capabilities.modelVersion == "v2", capabilities.available else {
                if let baseURL { recentCache.invalidateV2Capabilities(for: baseURL) }
                let reason = capabilities.error?.message ?? "当前工作空间尚未启用 task-domain v2"
                phase = .unsupported(reason)
                return
            }
            if let workspaceID = currentUser?.workspace.id, let workspaceStore, let baseURL {
                recentCache.markV2CapabilitiesVerified(for: baseURL)
                workspaceStore.configureSpotlight(workspaceID: workspaceID)
                Task { await workspaceStore.refreshSpotlightIndex() }
            }
            phase = .ready
        } catch let error as APIError where error.isUnauthorized {
            handleUnauthorized()
        } catch let error as APIError where error.code == "network_error" && restoreCachedWorkspace() {
            return
        } catch {
            phase = .unsupported(readable(error))
        }
    }

    private func cacheCurrentUser(_ user: CurrentUserPayload? = nil) {
        guard let user = user ?? currentUser, let baseURL else { return }
        recentCache.saveIdentity(user, for: baseURL)
    }

    private func restoreCachedWorkspace() -> Bool {
        guard let baseURL,
              let snapshot = recentCache.load(for: baseURL),
              snapshot.v2CapabilitiesVerified,
              let cachedUser = snapshot.currentUser,
              !cachedUser.mustChangePassword,
              !cachedUser.user.mustChangePassword,
              let workspaceStore,
              workspaceStore.restoreRecentContent() else { return false }
        currentUser = cachedUser
        connectivity.restoreLastSuccessfulResponse(at: snapshot.cachedAt)
        workspaceStore.configureSpotlight(workspaceID: cachedUser.workspace.id)
        errorMessage = nil
        phase = .ready
        return true
    }

    private func readable(_ error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
