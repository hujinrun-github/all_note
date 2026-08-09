import Foundation

enum NoteAttachmentKind: String, Codable, Sendable {
    case audio
    case video
    case image
    case file

    var systemImage: String {
        switch self {
        case .audio: "waveform"
        case .video: "film"
        case .image: "photo"
        case .file: "doc"
        }
    }
}

enum NoteAttachmentSource: String, Codable, Sendable {
    case upload
    case voiceNote = "voice_note"
}

enum VoiceTranscriptionState: String, Codable, Sendable {
    case notStarted = "not_started"
    case processing
    case completed
    case failed

    var label: String {
        switch self {
        case .notStarted: "尚未转写"
        case .processing: "正在转写"
        case .completed: "转写完成"
        case .failed: "转写失败"
        }
    }
}

struct NoteAttachment: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let noteID: String
    let kind: NoteAttachmentKind
    let originalName: String
    let mimeType: String
    let sizeBytes: Int64
    let sha256: String
    let source: NoteAttachmentSource
    let deletable: Bool
    let createdAt: Int64
    let contentURL: String
    let transcriptionState: VoiceTranscriptionState?
    let transcriptionError: String?
}

struct VoiceTranscriptionResult: Codable, Equatable, Sendable {
    let clientID: String
    let noteID: String
    let body: String
    let transcriptionState: VoiceTranscriptionState
    let transcriptionError: String?
    let updatedAt: Int64
}

struct FuriganaSegment: Codable, Equatable, Sendable {
    let text: String
    let reading: String?

    var markdown: String {
        guard let reading, !reading.isEmpty else { return text }
        return "｜\(text)《\(reading)》"
    }
}

struct FuriganaResult: Codable, Equatable, Sendable {
    let segments: [FuriganaSegment]
    let source: String

    var markdown: String { segments.map(\.markdown).joined() }
}
