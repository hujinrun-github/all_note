import Foundation
import Observation
import Sparkle

struct AppUpdateConfiguration: Equatable, Sendable {
    enum State: Equatable, Sendable {
        case configured(feedURL: URL)
        case unconfigured
        case invalid(String)
    }

    let state: State

    init(infoDictionary: [String: Any]) {
        let feedValue = Self.trimmedString(infoDictionary["SUFeedURL"])
        let publicKey = Self.trimmedString(infoDictionary["SUPublicEDKey"])

        guard !feedValue.isEmpty || !publicKey.isEmpty else {
            state = .unconfigured
            return
        }
        guard let feedURL = URL(string: feedValue),
              feedURL.scheme?.lowercased() == "https",
              feedURL.host != nil else {
            state = .invalid("更新源必须是有效的 HTTPS 地址。")
            return
        }
        guard let keyData = Data(base64Encoded: publicKey), keyData.count == 32 else {
            state = .invalid("更新签名公钥必须是有效的 Ed25519 公钥。")
            return
        }

        state = .configured(feedURL: feedURL)
    }

    var isConfigured: Bool {
        if case .configured = state { return true }
        return false
    }

    var feedURL: URL? {
        if case .configured(let feedURL) = state { return feedURL }
        return nil
    }

    private static func trimmedString(_ value: Any?) -> String {
        (value as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }
}

@MainActor
@Observable
final class AppUpdateService {
    let configuration: AppUpdateConfiguration
    private(set) var canCheckForUpdates = false
    private(set) var automaticallyChecksForUpdates = false
    private(set) var automaticallyDownloadsUpdates = false

    @ObservationIgnored private let updaterController: SPUStandardUpdaterController

    init(bundle: Bundle = .main) {
        configuration = AppUpdateConfiguration(infoDictionary: bundle.infoDictionary ?? [:])
        updaterController = SPUStandardUpdaterController(
            startingUpdater: false,
            updaterDelegate: nil,
            userDriverDelegate: nil
        )

        guard configuration.isConfigured else { return }

        updaterController.startUpdater()
        let updater = updaterController.updater
        automaticallyChecksForUpdates = updater.automaticallyChecksForUpdates
        automaticallyDownloadsUpdates = updater.automaticallyDownloadsUpdates
        canCheckForUpdates = updater.canCheckForUpdates
    }

    var versionDescription: String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "—"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
        return "\(version)（\(build)）"
    }

    func checkForUpdates() {
        guard configuration.isConfigured, updaterController.updater.canCheckForUpdates else { return }
        updaterController.checkForUpdates(nil)
    }

    func setAutomaticallyChecksForUpdates(_ enabled: Bool) {
        guard configuration.isConfigured else { return }
        updaterController.updater.automaticallyChecksForUpdates = enabled
        automaticallyChecksForUpdates = enabled
        automaticallyDownloadsUpdates = updaterController.updater.automaticallyDownloadsUpdates
    }

    func setAutomaticallyDownloadsUpdates(_ enabled: Bool) {
        guard configuration.isConfigured, automaticallyChecksForUpdates else { return }
        updaterController.updater.automaticallyDownloadsUpdates = enabled
        automaticallyDownloadsUpdates = updaterController.updater.automaticallyDownloadsUpdates
    }
}
