import SwiftUI

struct TodayView: View {
    let store: WorkspaceStore
    @Binding var selectedOccurrenceID: String
    @State private var scope: TodayScope = .today

    private var collections: [OccurrenceCollection] {
        OccurrenceCollection.group(store.occurrences, tasks: store.tasksByID)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .task(id: scope) {
            await store.load(scope: scope)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(scope.title)
                        .font(.largeTitle.weight(.semibold))
                    Text(Date.now.formatted(date: .complete, time: .omitted))
                        .foregroundStyle(.secondary)
                }
                Spacer()
                let openCount = store.occurrences.count { !$0.executionStatus.isTerminal }
                Text("\(openCount) 项待完成")
                    .font(.headline)
                    .foregroundStyle(.secondary)
            }

            Picker("范围", selection: $scope) {
                ForEach(TodayScope.allCases) { scope in
                    Text(scope.title).tag(scope)
                }
            }
            .pickerStyle(.segmented)
            .accessibilityIdentifier("today-scope")
        }
        .padding(22)
    }

    @ViewBuilder
    private var content: some View {
        if store.isLoading && store.occurrences.isEmpty {
            ProgressView("正在加载任务…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if store.occurrences.isEmpty {
            ContentUnavailableView(
                scope == .completed ? "暂无完成记录" : "这个范围没有任务",
                systemImage: scope == .completed ? "checkmark.circle" : "sun.max",
                description: Text(scope == .today ? "今天可以从快速捕获开始。" : "切换范围或刷新后再看看。")
            )
        } else {
            List {
                Section(scope == .today ? "今日安排" : "按任务集合") {
                    ForEach(collections) { collection in
                        if collection.occurrences.count > 1 {
                            OccurrenceCollectionRow(
                                collection: collection,
                                projects: store.projectsByID,
                                selectedOccurrenceID: selectedOccurrenceID,
                                isMutating: store.isMutating,
                                select: { selectedOccurrenceID = $0.id },
                                toggle: toggle
                            )
                        } else if let occurrence = collection.occurrences.first {
                            OccurrenceRow(
                                occurrence: occurrence,
                                task: collection.task,
                                project: store.projectsByID[occurrence.projectID ?? collection.task?.projectID ?? ""],
                                selected: occurrence.id == selectedOccurrenceID,
                                isMutating: store.isMutating,
                                select: { selectedOccurrenceID = occurrence.id },
                                toggle: { await toggle(occurrence) }
                            )
                        }
                    }
                }
            }
            .listStyle(.inset)
        }
    }

    private func toggle(_ occurrence: OccurrenceV2) async {
        do {
            try await store.toggle(occurrence)
            await store.load(scope: scope)
        } catch {
            store.errorMessage = error.localizedDescription
            if let apiError = error as? APIError, apiError.isRevisionConflict {
                await store.load(scope: scope)
            }
        }
    }
}

