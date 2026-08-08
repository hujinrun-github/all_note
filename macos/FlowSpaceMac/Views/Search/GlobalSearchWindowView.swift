import SwiftUI

struct GlobalSearchWindowView: View {
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore

    var body: some View {
        GlobalSearchView(store: store) { result in
            if result.type == "note" {
                openWindow(value: result.id)
            } else {
                openWindow(id: "workspace")
            }
        }
    }
}
