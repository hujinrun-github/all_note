import SwiftUI

struct DailySummaryView: View {
    @Environment(\.openWindow) private var openWindow
    let store: WorkspaceStore
    @State private var period: SummaryPeriod = .week

    private var completedCount: Int {
        store.summary?.groups.reduce(0) { $0 + $1.count } ?? 0
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                HStack {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("每日总结")
                            .font(.largeTitle.weight(.semibold))
                        Text(rangeLabel)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Picker("复盘周期", selection: $period) {
                        ForEach(SummaryPeriod.allCases) { period in
                            Text(period.title).tag(period)
                        }
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 220)
                }

                metrics

                HStack(alignment: .top, spacing: 18) {
                    completedPanel
                        .frame(maxWidth: .infinity)
                    VStack(spacing: 18) {
                        attentionPanel
                        recentNotesPanel
                    }
                    .frame(width: 310)
                }
            }
            .padding(22)
        }
        .task(id: period) {
            await store.loadSummary(period: period)
        }
        .overlay {
            if store.isLoading && store.summary == nil {
                ProgressView("正在生成总结…")
                    .padding(30)
                    .background(.regularMaterial, in: .rect(cornerRadius: 14))
            }
        }
    }

    private var metrics: some View {
        HStack(spacing: 12) {
            SummaryMetric(title: "周期完成", value: completedCount, detail: "\(store.summary?.activeDays ?? 0) 个活跃日", color: .green)
            SummaryMetric(title: "涉及项目", value: store.summary?.projectCount ?? 0, detail: period.title, color: .blue)
            SummaryMetric(title: "需要关注", value: store.summaryAttention.count, detail: "今天与逾期", color: .orange)
            SummaryMetric(title: "最近笔记", value: min(store.notes.count, 3), detail: "知识产出", color: .purple)
        }
    }

    private var completedPanel: some View {
        GroupBox {
            if let groups = store.summary?.groups, !groups.isEmpty {
                LazyVStack(alignment: .leading, spacing: 18) {
                    ForEach(groups) { group in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Text(formattedDay(group.date)).font(.headline)
                                Spacer()
                                Text("\(group.count) 项").foregroundStyle(.secondary)
                            }
                            ForEach(group.tasks) { task in
                                HStack(spacing: 10) {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(.green)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(task.title).fontWeight(.medium)
                                        Text(task.project?.name ?? "未归属项目")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if !(task.linkedNotes ?? []).isEmpty {
                                        Label("\(task.linkedNotes?.count ?? 0)", systemImage: "note.text")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                .padding(.vertical, 3)
                            }
                        }
                    }
                }
                .padding(8)
            } else {
                ContentUnavailableView(
                    "这个周期还没有完成记录",
                    systemImage: "checkmark.circle",
                    description: Text("完成任务后会按日期汇总到这里。")
                )
                .frame(minHeight: 260)
            }
        } label: {
            Label("完成记录", systemImage: "checkmark.seal")
                .font(.headline)
        }
    }

    private var attentionPanel: some View {
        GroupBox {
            if store.summaryAttention.isEmpty {
                Text("今天没有未完成事项。")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 80, alignment: .center)
            } else {
                VStack(spacing: 0) {
                    ForEach(store.summaryAttention.prefix(5)) { occurrence in
                        HStack(spacing: 9) {
                            Circle()
                                .fill(isOverdue(occurrence) ? .red : .orange)
                                .frame(width: 7, height: 7)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(occurrence.title ?? store.tasksByID[occurrence.taskID]?.title ?? "未命名任务")
                                    .fontWeight(.medium)
                                    .lineLimit(1)
                                Text(isOverdue(occurrence) ? "已逾期" : "今日待办")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                        }
                        .padding(.vertical, 7)
                    }
                }
            }
        } label: {
            Label("需要关注", systemImage: "exclamationmark.circle")
                .font(.headline)
        }
    }

    private var recentNotesPanel: some View {
        GroupBox {
            if store.notes.isEmpty {
                Text("还没有最近更新的笔记。")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 80, alignment: .center)
            } else {
                VStack(spacing: 0) {
                    ForEach(store.notes.prefix(3)) { note in
                        Button {
                            openWindow(value: note.id)
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(note.title.isEmpty ? "未命名笔记" : note.title)
                                        .fontWeight(.medium)
                                        .lineLimit(1)
                                    Text(Date(timeIntervalSince1970: TimeInterval(note.updatedAt)), style: .relative)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                Image(systemName: "chevron.right")
                                    .font(.caption)
                                    .foregroundStyle(.tertiary)
                            }
                            .contentShape(.rect)
                            .padding(.vertical, 7)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        } label: {
            Label("最近笔记", systemImage: "note.text")
                .font(.headline)
        }
    }

    private var rangeLabel: String {
        let calendar = Calendar.current
        let interval = period == .week
            ? calendar.dateInterval(of: .weekOfYear, for: Date())
            : calendar.dateInterval(of: .month, for: Date())
        guard let interval else { return period.title }
        let end = calendar.date(byAdding: .day, value: -1, to: interval.end) ?? interval.end
        return "\(interval.start.formatted(date: .abbreviated, time: .omitted)) – \(end.formatted(date: .abbreviated, time: .omitted))"
    }

    private func formattedDay(_ value: String) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        guard let date = formatter.date(from: value) else { return value }
        return date.formatted(.dateTime.month(.wide).day().weekday(.abbreviated))
    }

    private func isOverdue(_ occurrence: OccurrenceV2) -> Bool {
        if let start = occurrence.plannedStartAt.flatMap(Date.flowSpaceISO8601) { return start < Date() }
        guard let planned = occurrence.plannedDate else { return false }
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.date(from: planned).map { $0 < Calendar.current.startOfDay(for: Date()) } ?? false
    }
}

private struct SummaryMetric: View {
    let title: String
    let value: Int
    let detail: String
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(title).foregroundStyle(.secondary)
            Text("\(value)")
                .font(.system(size: 30, weight: .semibold, design: .rounded))
                .foregroundStyle(color)
            Text(detail).font(.caption).foregroundStyle(.secondary)
        }
        .padding(15)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: .rect(cornerRadius: 12))
    }
}

