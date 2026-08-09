import CoreGraphics
import Foundation

enum CalendarDropPlanner {
    static let minuteStep = 15
    static let minimumDurationMinutes = 15
    static let maximumDurationMinutes = 480

    static func snappedMinute(y: CGFloat, cellHeight: CGFloat) -> Int {
        guard cellHeight > 0 else { return 0 }
        let boundedY = min(max(y, 0), cellHeight)
        let rawMinute = Int((boundedY / cellHeight) * 60)
        return min(45, max(0, (rawMinute / minuteStep) * minuteStep))
    }

    static func proposedStart(
        day: Date,
        hour: Int,
        minute: Int,
        timezone: TimeZone
    ) -> Date? {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timezone
        var components = calendar.dateComponents([.year, .month, .day], from: day)
        components.hour = hour
        components.minute = minute
        components.second = 0
        return calendar.date(from: components)
    }

    static func durationMinutes(for entry: CalendarEntryV2) -> Int {
        guard let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601),
              let end = entry.plannedEndAt.flatMap(Date.flowSpaceISO8601) else { return 30 }
        return max(minuteStep, Int(end.timeIntervalSince(start) / 60))
    }

    static func resizedDuration(
        originalMinutes: Int,
        verticalTranslation: CGFloat,
        pointsPerHour: CGFloat
    ) -> Int {
        guard pointsPerHour > 0 else {
            return min(maximumDurationMinutes, max(minimumDurationMinutes, originalMinutes))
        }
        let rawDelta = (verticalTranslation / pointsPerHour) * 60
        let snappedDelta = Int((rawDelta / CGFloat(minuteStep)).rounded()) * minuteStep
        let proposed = originalMinutes + snappedDelta
        return min(maximumDurationMinutes, max(minimumDurationMinutes, proposed))
    }

    static func conflicts(
        moving entry: CalendarEntryV2,
        proposedStart: Date,
        entries: [CalendarEntryV2]
    ) -> [CalendarEntryV2] {
        conflicts(
            changing: entry,
            proposedStart: proposedStart,
            proposedDurationMinutes: durationMinutes(for: entry),
            entries: entries
        )
    }

    static func conflicts(
        resizing entry: CalendarEntryV2,
        proposedDurationMinutes: Int,
        entries: [CalendarEntryV2]
    ) -> [CalendarEntryV2] {
        guard let start = entry.plannedStartAt.flatMap(Date.flowSpaceISO8601) else { return [] }
        return conflicts(
            changing: entry,
            proposedStart: start,
            proposedDurationMinutes: proposedDurationMinutes,
            entries: entries
        )
    }

    private static func conflicts(
        changing entry: CalendarEntryV2,
        proposedStart: Date,
        proposedDurationMinutes: Int,
        entries: [CalendarEntryV2]
    ) -> [CalendarEntryV2] {
        let proposedEnd = proposedStart.addingTimeInterval(TimeInterval(proposedDurationMinutes * 60))
        return entries.filter { candidate in
            guard candidate.occurrenceID != entry.occurrenceID,
                  candidate.timingType == .timeBlock,
                  let candidateStart = candidate.plannedStartAt.flatMap(Date.flowSpaceISO8601) else { return false }
            let candidateEnd = candidate.plannedEndAt.flatMap(Date.flowSpaceISO8601)
                ?? candidateStart.addingTimeInterval(30 * 60)
            return proposedStart < candidateEnd && proposedEnd > candidateStart
        }
    }
}

enum CalendarResizePreferences {
    static let enabledKey = "calendarResizeHandleEnabled"
}
