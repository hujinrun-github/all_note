import Foundation
import Observation

enum ServiceConnectionEvent: Sendable {
    case reachable(Date)
    case unreachable(message: String, at: Date)
}

@MainActor
@Observable
final class ServiceConnectivity {
    enum State: Equatable {
        case checking
        case online(lastResponseAt: Date)
        case offline(message: String, since: Date)
    }

    private(set) var state: State = .checking
    private(set) var lastSuccessfulResponseAt: Date?
    private(set) var isReadOnly = false
    private var offlineSince: Date?

    var isOffline: Bool { isReadOnly }

    var allowsMutations: Bool { !isReadOnly }

    func beginChecking() {
        state = .checking
    }

    func reset() {
        state = .checking
        lastSuccessfulResponseAt = nil
        isReadOnly = false
        offlineSince = nil
    }

    func restoreLastSuccessfulResponse(at date: Date?) {
        guard lastSuccessfulResponseAt == nil else { return }
        lastSuccessfulResponseAt = date
    }

    func receive(_ event: ServiceConnectionEvent) {
        switch event {
        case .reachable(let date):
            lastSuccessfulResponseAt = date
            isReadOnly = false
            offlineSince = nil
            state = .online(lastResponseAt: date)
        case .unreachable(let message, let date):
            let since = offlineSince ?? date
            offlineSince = since
            isReadOnly = true
            state = .offline(message: message, since: since)
        }
    }
}
