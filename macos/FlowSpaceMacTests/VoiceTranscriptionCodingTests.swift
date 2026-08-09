import Foundation
import Testing
@testable import FlowSpaceMac

struct VoiceTranscriptionCodingTests {
    @Test func decodesTranscriptionResultAndAttachmentStates() throws {
        let resultData = Data(#"""
        {
          "client_id":"voice-1",
          "note_id":"note-1",
          "body":"识别后的正文",
          "transcription_state":"completed",
          "updated_at":42
        }
        """#.utf8)
        let result = try JSONDecoder.flowSpace().decode(VoiceTranscriptionResult.self, from: resultData)
        #expect(result.clientID == "voice-1")
        #expect(result.noteID == "note-1")
        #expect(result.transcriptionState == .completed)

        let attachmentData = Data(#"""
        {
          "id":"voice-1",
          "note_id":"note-1",
          "kind":"audio",
          "original_name":"录音.m4a",
          "mime_type":"audio/mp4",
          "size_bytes":2048,
          "sha256":"abc",
          "source":"voice_note",
          "deletable":false,
          "created_at":40,
          "content_url":"/api/notes/note-1/attachments/voice-1/content",
          "transcription_state":"failed",
          "transcription_error":"service unavailable"
        }
        """#.utf8)
        let attachment = try JSONDecoder.flowSpace().decode(NoteAttachment.self, from: attachmentData)
        #expect(attachment.transcriptionState == .failed)
        #expect(attachment.transcriptionError == "service unavailable")
    }
}
