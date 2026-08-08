import SwiftUI

struct CalendarWorkspaceView: View {
    let store: WorkspaceStore
    @State private var anchorDate = Date()
    @State private var taskDraft: TaskDraft?
    @State private var selectedEntry: CalendarEntryV2?
    @AppStorage("defaultCalendarView") private var modeRaw = CalendarDisplayMode.week.rawValue
    @AppStorage("calendarTimeZoneID") private var timezoneID = TimeZone.current.identifier

    private let hours = Array(7...20)
    private let calendar = Calendar.current

    private var mode: CalendarDisplayMode {
        get { CalendarDisplayMode(rawValue: modeRaw) ?? .week }
        nonmutating set { modeRaw = newValue.rawValue }
    }

    private var timezone: TimeZone { TimeZone(identifier: timezoneID) ?? .current }

    private var loadKey: String {
        "\(mode.rawValue)-\(timezoneID)-\(anchorDate.timeIntervalSince1970)"
    }

    private var weekStart: Date {
        calendar.dateInterval(of: .weekOfYear, for: anchorDate)?.start ?? calendar.startOfDay(for: anchorDate)
    }

    private var days: [Date] {
        (0..<7).compactMap { calendar.date(byAdding: .day, value: $0, to: weekStart) }
    }

    var body: some View {
        VStack(spacing: 0) {
            calendarToolbar
            Divider()
            switch mode {
            case .week:
                dayHeader
                Divider()
                ScrollView([.vertical, .horizontal]) {
                    LazyVStack(spacing: 0) {
                        allDayRow
                        ForEach(hours, id: \.self) { hour in
                            HourRow(
                                hour: hour,
                                days: days,
                                entries: store.calendarEntries,
                                selectEmpty: openDraft,
                                selectEntry: { selectedEntry = $0 }
                            )
                        }
                    }
                    .frame(minWidth: 840)
                }
            case .month:
                MonthCalendarGrid(
                    anchorDate: anchorDate,
                    entries: store.calendarEntries,
                    selectDate: openDateDraft,
                    selectEntry: { selectedEntry = $0 }
                )
            case .year:
                YearCalendarGrid(anchorDate: anchorDate) { month in
                    anchorDate = month
                    mode = .month
                }
            }
        }
        .task(id: loadKey) {
            await store.loadCalendar(containing: anchorDate, mode: mode, timezone: timezone)
        }
        .sheet(item: $taskDraft) { draft in
            TaskEditorSheet(draft: draft, store: store) {
                await store.loadCalendar(containing: anchorDate, mode: mode, timezone: timezone)
            }
        }
        .sheet(item: $selectedEntry) { entry in
            CalendarEntryEditorSheet(entry: entry, timezone: timezone, store: store) {
                await store.loadCalendar(containing: anchorDate, mode: mode, timezone: timezone)
            }
            .environment(\.timeZone, timezone)
        }
    }

    private var calendarToolbar: some View {
        HStack {
            Button("上一个范围", systemImage: "chevron.left") {
                anchorDate = shifted(-1)
            }
            .labelStyle(.iconOnly)
            Button("今天") { anchorDate = Date() }
            Button("下一个范围", systemImage: "chevron.right") {
                anchorDate = shifted(1)
            }
            .labelStyle(.iconOnly)
            Spacer()
            Text(title)
                .font(.title3.weight(.semibold))
            Spacer()
            Picker("视图", selection: Binding(get: { mode }, set: { mode = $0 })) {
                ForEach(CalendarDisplayMode.allCases) { mode in Text(mode.title).tag(mode) }
            }
            .pickerStyle(.segmented)
            .frame(width: 150)
            Picker("时区", selection: $timezoneID) {
                ForEach(timezoneOptions, id: \.self) { value in Text(value).tag(value) }
            }
            .frame(maxWidth: 190)
        }
        .padding(14)
    }

    private var dayHeader: some View {
        HStack(spacing: 0) {
            Color.clear.frame(width: 64)
            ForEach(days, id: \.self) { day in
                VStack(spacing: 3) {
                    Text(day.formatted(.dateTime.weekday(.abbreviated)))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(day.formatted(.dateTime.month().day()))
                        .fontWeight(calendar.isDateInToday(day) ? .bold : .medium)
                        .foregroundStyle(calendar.isDateInToday(day) ? Color.accentColor : .primary)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 9)
                .background(calendar.isDateInToday(day) ? Color.accentColor.opacity(0.08) : .clear)
            }
        }
        .frame(minWidth: 840)
    }

    private var allDayRow: some View {
        HStack(spacing: 0) {
            Text("全天")
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(width: 64)
            ForEach(days, id: \.self) { day in
                let entries = entries(on: day).filter { $0.timingType == .date }
                ZStack(alignment: .topLeading) {
                    Button {
                        var draft = TaskDraft.scheduled(at: day, projectID: store.defaultProjectID)
                        draft.timingType = .date
                        taskDraft = draft
                    } label: {
                        Color.clear
                            .frame(maxWidth: .infinity, minHeight: 52)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("在 \(day.formatted(date: .long, time: .omitted)) 新建全天日程")

                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(entries.prefix(3)) { entry in
                            Button {
                                selectedEntry = entry
                            } label: {
                                Text(entry.taskTitle)
                                    .font(.caption)
                                    .lineLimit(1)
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 2)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .background(Color.accentColor.opacity(0.12), in: .rect(cornerRadius: 4))
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("编辑日程：\(entry.taskTitle)")
                        }
                    }
                    .frame(maxWidth: .infinity, minHeight: 52, alignment: .topLeading)
                    .padding(4)
                }
                .overlay(alignment: .trailing) { Divider() }
            }
        }
        .overlay(alignment: .bottom) { Divider() }
    }

    private var title: String {
        if mode == .month { return anchorDate.formatted(.dateTime.year().month(.wide)) }
        if mode == .year { return anchorDate.formatted(.dateTime.year()) }
        guard let last = days.last else { return "" }
        return "\(weekStart.formatted(.dateTime.year().month().day()))–\(last.formatted(.dateTime.month().day()))"
    }

    private var timezoneOptions: [String] {
        Array(Set([TimeZone.current.identifier, "Asia/Shanghai", "UTC", "America/Los_Angeles", "Europe/London"])).sorted()
    }

    private func shifted(_ amount: Int) -> Date {
        let component: Calendar.Component = switch mode {
        case .week: .weekOfYear
        case .month: .month
        case .year: .year
        }
        return calendar.date(byAdding: component, value: amount, to: anchorDate) ?? anchorDate
    }

    private func entries(on day: Date) -> [CalendarEntryV2] {
        store.calendarEntries.filter { entry in
            if let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) {
                return calendar.isDate(start, inSameDayAs: day)
            }
            guard let value = entry.plannedDate else { return false }
            let formatter = DateFormatter()
            formatter.dateFormat = "yyyy-MM-dd"
            formatter.calendar = calendar
            return formatter.date(from: value).map { calendar.isDate($0, inSameDayAs: day) } ?? false
        }
    }

    private func openDraft(day: Date, hour: Int) {
        guard let date = calendar.date(bySettingHour: hour, minute: 0, second: 0, of: day) else { return }
        taskDraft = .scheduled(at: date, projectID: store.defaultProjectID)
    }

    private func openDateDraft(_ date: Date) {
        var draft = TaskDraft.scheduled(at: date, projectID: store.defaultProjectID)
        draft.timingType = .date
        taskDraft = draft
    }
}

private struct HourRow: View {
    let hour: Int
    let days: [Date]
    let entries: [CalendarEntryV2]
    let selectEmpty: (Date, Int) -> Void
    let selectEntry: (CalendarEntryV2) -> Void
    private let calendar = Calendar.current

    var body: some View {
        HStack(spacing: 0) {
            Text(String(format: "%02d:00", hour))
                .font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: 64, alignment: .center)

            ForEach(days, id: \.self) { day in
                ZStack(alignment: .topLeading) {
                    Button {
                        selectEmpty(day, hour)
                    } label: {
                        Color.clear
                            .frame(maxWidth: .infinity, minHeight: 62)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("在 \(day.formatted(date: .abbreviated, time: .omitted)) \(hour) 点新建日程")

                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(entriesForCell(day: day)) { entry in
                            Button {
                                selectEntry(entry)
                            } label: {
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(entry.taskTitle)
                                        .font(.caption.weight(.semibold))
                                        .lineLimit(1)
                                    if let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) {
                                        Text(start.formatted(date: .omitted, time: .shortened))
                                            .font(.caption2.monospacedDigit())
                                    }
                                }
                                .foregroundStyle(.primary)
                                .padding(5)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(Color.accentColor.opacity(0.14), in: .rect(cornerRadius: 6))
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("编辑日程：\(entry.taskTitle)")
                        }
                    }
                    .frame(maxWidth: .infinity, minHeight: 62, alignment: .topLeading)
                    .padding(3)
                }
                .overlay(alignment: .trailing) { Divider() }
            }
        }
        .overlay(alignment: .bottom) { Divider() }
    }

    private func entriesForCell(day: Date) -> [CalendarEntryV2] {
        entries.filter { entry in
            guard entry.timingType == .timeBlock,
                  let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) else { return false }
            return calendar.isDate(start, inSameDayAs: day) && calendar.component(.hour, from: start) == hour
        }
    }
}

private struct MonthCalendarGrid: View {
    let anchorDate: Date
    let entries: [CalendarEntryV2]
    let selectDate: (Date) -> Void
    let selectEntry: (CalendarEntryV2) -> Void
    private let calendar = Calendar.current
    private let columns = Array(repeating: GridItem(.flexible(), spacing: 0), count: 7)

    private var monthInterval: DateInterval? {
        calendar.dateInterval(of: .month, for: anchorDate)
    }

    private var gridDates: [Date?] {
        guard let interval = monthInterval,
              let dayRange = calendar.range(of: .day, in: .month, for: anchorDate) else { return [] }
        let weekday = calendar.component(.weekday, from: interval.start)
        let leading = (weekday - calendar.firstWeekday + 7) % 7
        var values = Array<Date?>(repeating: nil, count: leading)
        values += dayRange.compactMap { day in
            calendar.date(bySetting: .day, value: day, of: interval.start)
        }.map(Optional.some)
        while values.count % 7 != 0 { values.append(nil) }
        return values
    }

    var body: some View {
        VStack(spacing: 0) {
            LazyVGrid(columns: columns, spacing: 0) {
                ForEach(weekdaySymbols, id: \.self) { symbol in
                    Text(symbol)
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 8)
                }
            }
            Divider()
            ScrollView {
                LazyVGrid(columns: columns, spacing: 0) {
                    ForEach(Array(gridDates.enumerated()), id: \.offset) { _, date in
                        if let date {
                            ZStack(alignment: .topLeading) {
                                Button {
                                    selectDate(date)
                                } label: {
                                    Color.clear
                                        .frame(maxWidth: .infinity, minHeight: 96)
                                        .contentShape(.rect)
                                }
                                .buttonStyle(.plain)
                                .accessibilityLabel("\(date.formatted(date: .long, time: .omitted))，新建日程")

                                VStack(alignment: .leading, spacing: 5) {
                                    Text(date.formatted(.dateTime.day()))
                                        .fontWeight(calendar.isDateInToday(date) ? .bold : .regular)
                                        .foregroundStyle(calendar.isDateInToday(date) ? Color.accentColor : .primary)
                                    ForEach(entries(on: date).prefix(3)) { entry in
                                        Button {
                                            selectEntry(entry)
                                        } label: {
                                            Text(entry.taskTitle)
                                                .font(.caption2)
                                                .lineLimit(1)
                                                .padding(.horizontal, 4)
                                                .padding(.vertical, 2)
                                                .frame(maxWidth: .infinity, alignment: .leading)
                                                .background(Color.accentColor.opacity(0.12), in: .rect(cornerRadius: 4))
                                        }
                                        .buttonStyle(.plain)
                                        .accessibilityLabel("编辑日程：\(entry.taskTitle)")
                                    }
                                    Spacer(minLength: 0)
                                }
                                .padding(7)
                                .frame(maxWidth: .infinity, minHeight: 96, alignment: .topLeading)
                            }
                            .overlay { Rectangle().stroke(Color.secondary.opacity(0.2), lineWidth: 0.5) }
                        } else {
                            Color.clear
                                .frame(minHeight: 96)
                                .overlay { Rectangle().stroke(Color.secondary.opacity(0.12), lineWidth: 0.5) }
                        }
                    }
                }
            }
        }
    }

    private var weekdaySymbols: [String] {
        let symbols = calendar.shortWeekdaySymbols
        let offset = calendar.firstWeekday - 1
        return Array(symbols[offset...] + symbols[..<offset])
    }

    private func entries(on date: Date) -> [CalendarEntryV2] {
        entries.filter { entry in
            if let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) {
                return calendar.isDate(start, inSameDayAs: date)
            }
            guard let raw = entry.plannedDate else { return false }
            let formatter = DateFormatter()
            formatter.calendar = calendar
            formatter.dateFormat = "yyyy-MM-dd"
            return formatter.date(from: raw).map { calendar.isDate($0, inSameDayAs: date) } ?? false
        }
    }
}

private struct YearCalendarGrid: View {
    let anchorDate: Date
    let selectMonth: (Date) -> Void
    private let calendar = Calendar.current
    private let columns = Array(repeating: GridItem(.flexible(), spacing: 14), count: 3)

    private var months: [Date] {
        guard let start = calendar.dateInterval(of: .year, for: anchorDate)?.start else { return [] }
        return (0..<12).compactMap { calendar.date(byAdding: .month, value: $0, to: start) }
    }

    var body: some View {
        ScrollView {
            LazyVGrid(columns: columns, spacing: 14) {
                ForEach(months, id: \.self) { month in
                    Button {
                        selectMonth(month)
                    } label: {
                        VStack(alignment: .leading, spacing: 10) {
                            Text(month.formatted(.dateTime.month(.wide)))
                                .font(.headline)
                            MiniMonth(month: month)
                        }
                        .padding(12)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.regularMaterial, in: .rect(cornerRadius: 12))
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(18)
        }
    }
}

private struct MiniMonth: View {
    let month: Date
    private let calendar = Calendar.current
    private let columns = Array(repeating: GridItem(.flexible(), spacing: 2), count: 7)

    private var values: [Int?] {
        guard let interval = calendar.dateInterval(of: .month, for: month),
              let range = calendar.range(of: .day, in: .month, for: month) else { return [] }
        let leading = (calendar.component(.weekday, from: interval.start) - calendar.firstWeekday + 7) % 7
        return Array(repeating: nil, count: leading) + range.map(Optional.some)
    }

    var body: some View {
        LazyVGrid(columns: columns, spacing: 4) {
            ForEach(Array(values.enumerated()), id: \.offset) { _, day in
                Text(day.map(String.init) ?? "")
                    .font(.caption2.monospacedDigit())
                    .frame(maxWidth: .infinity)
                    .foregroundStyle(day.flatMap(dateForDay).map(calendar.isDateInToday) == true ? Color.accentColor : .secondary)
            }
        }
    }

    private func dateForDay(_ day: Int) -> Date? {
        calendar.date(bySetting: .day, value: day, of: month)
    }
}

private enum CalendarEditScope: String, CaseIterable, Identifiable {
    case onlyThis
    case thisAndFollowing

    var id: String { rawValue }
    var title: String {
        switch self {
        case .onlyThis: "仅本次"
        case .thisAndFollowing: "本次及以后"
        }
    }
}

private struct CalendarEntryEditDraft {
    var scope: CalendarEditScope = .onlyThis
    var timingType: TimingType
    var plannedDate: Date
    var allDayEndDate: Date
    var localStartDate: Date
    var durationMinutes: Int
    var recurrenceType: RecurrenceType = .daily
    var effectiveFrom: Date
    var generateThroughExclusive: Date
    var selectedOffsetSeconds: Int?

    init(entry: CalendarEntryV2, timezone: TimeZone) {
        let calendar = Calendar(identifier: .gregorian)
        let planned = Self.localDate(entry.plannedDate, timezone: timezone) ?? Date()
        let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) ?? planned
        let end = entry.plannedEndAt.flatMap(Date.flowSpaceISO8601)
        let defaultEnd = calendar.date(byAdding: .day, value: 1, to: planned) ?? planned

        timingType = entry.timingType == .timeBlock ? .timeBlock : .date
        plannedDate = planned
        allDayEndDate = Self.localDate(entry.allDayEndDate, timezone: timezone) ?? defaultEnd
        localStartDate = start
        durationMinutes = max(15, end.map { Int($0.timeIntervalSince(start) / 60) } ?? 30)
        effectiveFrom = planned
        generateThroughExclusive = calendar.date(byAdding: .day, value: 31, to: planned) ?? planned
    }

    private static func localDate(_ raw: String?, timezone: TimeZone) -> Date? {
        guard let raw else { return nil }
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timezone
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.date(from: raw)
    }
}

struct CalendarEntryEditorSheet: View {
    @Environment(\.dismiss) private var dismiss
    let entry: CalendarEntryV2
    let timezone: TimeZone
    let store: WorkspaceStore
    let onSaved: () async -> Void
    @State private var draft: CalendarEntryEditDraft
    @State private var offsetCandidates: [ScheduleOffsetCandidate] = []
    @State private var errorMessage: String?

    init(
        entry: CalendarEntryV2,
        timezone: TimeZone,
        store: WorkspaceStore,
        onSaved: @escaping () async -> Void
    ) {
        self.entry = entry
        self.timezone = timezone
        self.store = store
        self.onSaved = onSaved
        _draft = State(initialValue: CalendarEntryEditDraft(entry: entry, timezone: timezone))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("执行详情") {
                    LabeledContent("任务", value: entry.taskTitle)
                    LabeledContent("状态", value: executionStatusTitle)
                    LabeledContent("显示时区", value: timezone.identifier)
                }

                if entry.executionStatus == .done {
                    Section {
                        Text("已完成的执行不能直接移动，请先重新打开。")
                            .foregroundStyle(.secondary)
                        Button("重新打开任务", systemImage: "arrow.uturn.backward") {
                            Task { await reopen() }
                        }
                        .disabled(store.isMutating)
                    }
                } else if entry.executionStatus.isTerminal {
                    Section {
                        Label("已跳过或已取消的执行不能改期。", systemImage: "exclamationmark.circle")
                            .foregroundStyle(.secondary)
                    }
                } else {
                    scheduleForm
                }

                if !offsetCandidates.isEmpty {
                    Section("这个本地时间出现了两次") {
                        Text("请选择准确的 UTC 偏移后再次保存。")
                            .foregroundStyle(.secondary)
                        ForEach(offsetCandidates) { candidate in
                            Button {
                                draft.selectedOffsetSeconds = candidate.offsetSeconds
                            } label: {
                                HStack {
                                    Image(systemName: draft.selectedOffsetSeconds == candidate.offsetSeconds ? "checkmark.circle.fill" : "circle")
                                    Text("\(offsetLabel(candidate.offsetSeconds)) · \(candidate.utc)")
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }

                if let errorMessage {
                    Section {
                        Label(errorMessage, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("编辑日程")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                if !entry.executionStatus.isTerminal {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("保存日程") { Task { await save() } }
                            .buttonStyle(.borderedProminent)
                            .disabled(store.isMutating || requiresOffsetSelection)
                    }
                }
            }
        }
        .frame(width: 560, height: 650)
    }

    @ViewBuilder
    private var scheduleForm: some View {
        Section("修改范围") {
            if entry.recurring {
                Picker("范围", selection: $draft.scope) {
                    ForEach(CalendarEditScope.allCases) { scope in
                        Text(scope.title).tag(scope)
                    }
                }
                .pickerStyle(.segmented)
            } else {
                LabeledContent("范围", value: CalendarEditScope.onlyThis.title)
            }
        }

        Section("安排") {
            Picker("安排方式", selection: $draft.timingType) {
                Text("全天").tag(TimingType.date)
                Text("时间块").tag(TimingType.timeBlock)
            }
            DatePicker("计划日期", selection: $draft.plannedDate, displayedComponents: .date)

            if draft.timingType == .date {
                DatePicker("结束日期（不含）", selection: $draft.allDayEndDate, displayedComponents: .date)
            } else {
                DatePicker("开始时间", selection: $draft.localStartDate, displayedComponents: .hourAndMinute)
                Stepper(
                    "时长：\(draft.durationMinutes) 分钟",
                    value: $draft.durationMinutes,
                    in: 15...480,
                    step: 15
                )
            }
        }

        if entry.recurring, draft.scope == .thisAndFollowing {
            Section("后续重复规则") {
                Picker("重复", selection: $draft.recurrenceType) {
                    Text("每天").tag(RecurrenceType.daily)
                    Text("每周").tag(RecurrenceType.weekly)
                    Text("每月").tag(RecurrenceType.monthly)
                }
                DatePicker("生效日期", selection: $draft.effectiveFrom, displayedComponents: .date)
                DatePicker("生成至（不含）", selection: $draft.generateThroughExclusive, displayedComponents: .date)
            }
        }
    }

    private var requiresOffsetSelection: Bool {
        !offsetCandidates.isEmpty && draft.selectedOffsetSeconds == nil
    }

    private var executionStatusTitle: String {
        switch entry.executionStatus {
        case .open: "未开始"
        case .active: "进行中"
        case .blocked: "已阻塞"
        case .done: "已完成"
        case .skipped: "已跳过"
        case .cancelled: "已取消"
        }
    }

    private func save() async {
        errorMessage = nil
        let plannedDate = dateString(draft.plannedDate)
        do {
            if draft.scope == .thisAndFollowing, entry.recurring {
                let effectiveFrom = dateString(draft.effectiveFrom)
                let generateThrough = dateString(draft.generateThroughExclusive)
                let schedule = ScheduleInput(
                    recurrenceType: draft.recurrenceType,
                    timingType: draft.timingType,
                    timezone: timezone.identifier,
                    startsOn: effectiveFrom,
                    endsOn: nil,
                    localStartTime: draft.timingType == .timeBlock ? timeString(draft.localStartDate) : nil,
                    durationMinutes: draft.timingType == .timeBlock ? draft.durationMinutes : nil,
                    rule: RecurrenceRule(interval: 1, weekdays: nil, monthDays: nil)
                )
                try await store.rescheduleThisAndFollowing(
                    entry,
                    effectiveFrom: effectiveFrom,
                    generateThroughExclusive: generateThrough,
                    schedule: schedule,
                    selectedOffsetSeconds: draft.selectedOffsetSeconds
                )
            } else {
                let allDayEnd = draft.timingType == .date ? dateString(draft.allDayEndDate) : nil
                if let allDayEnd, allDayEnd <= plannedDate {
                    throw ValidationError("结束日期必须晚于计划日期")
                }
                try await store.rescheduleOnlyThis(
                    entry,
                    timing: OccurrenceTimingInput(
                        timingType: draft.timingType,
                        timezone: timezone.identifier,
                        plannedDate: plannedDate,
                        allDayEndDate: allDayEnd,
                        localStartTime: draft.timingType == .timeBlock ? timeString(draft.localStartDate) : nil,
                        durationMinutes: draft.timingType == .timeBlock ? draft.durationMinutes : nil
                    ),
                    selectedOffsetSeconds: draft.selectedOffsetSeconds
                )
            }
            await onSaved()
            dismiss()
        } catch let error as APIError {
            offsetCandidates = error.details?.offsetCandidates ?? []
            draft.selectedOffsetSeconds = nil
            errorMessage = error.isRevisionConflict
                ? "这个安排已在其他窗口中更新，请关闭后重新打开再修改。"
                : error.localizedDescription
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func reopen() async {
        errorMessage = nil
        do {
            try await store.reopen(entry)
            await onSaved()
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func dateString(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timezone
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    private func timeString(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timezone
        formatter.dateFormat = "HH:mm"
        return formatter.string(from: date)
    }

    private func offsetLabel(_ seconds: Int) -> String {
        let sign = seconds < 0 ? "−" : "+"
        let absolute = abs(seconds)
        return String(format: "UTC%@%02d:%02d", sign, absolute / 3_600, (absolute % 3_600) / 60)
    }
}
