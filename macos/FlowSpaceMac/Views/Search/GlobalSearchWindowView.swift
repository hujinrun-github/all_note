import SwiftUI

struct GlobalSearchWindowView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore

    var body: some View {
        GlobalSearchView(store: store) { result in
            switch result.route {
            case .note(let noteID):
                openWindow(value: noteID)
            case .workspace(let destination, let selection):
                if let selection { session.requestWorkspaceEntitySelection(selection) }
                session.requestWorkspaceDestination(destination)
                openWindow(id: "workspace")
            }
        }
    }
}
