import SwiftUI
import UniformTypeIdentifiers

struct NoteAttachmentsInspector: View {
    let noteID: String
    let currentBody: String
    let store: WorkspaceStore
    let prepareForTranscription: () async -> Bool
    let onTranscribed: (VoiceTranscriptionResult) -> Void
    @State private var isPickingFiles = false
    @State private var deletingAttachment: NoteAttachment?
    @State private var pendingTranscription: NoteAttachment?
    @State private var transcribingAttachmentID: String?
    @State private var localError: String?
    @State private var notice: String?

    private var attachments: [NoteAttachment] {
        store.attachmentsByNoteID[noteID] ?? []
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("附件").font(.title2.weight(.semibold))
                    Text("\(attachments.count) / 20")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("添加附件", systemImage: "plus") { isPickingFiles = true }
                    .labelStyle(.iconOnly)
                    .disabled(store.isMutating || attachments.count >= 20)
            }
            .padding(16)

            Divider()

            if attachments.isEmpty {
                ContentUnavailableView(
                    "还没有附件",
                    systemImage: "paperclip",
                    description: Text("可上传图片、音视频或其他文件，单个文件不超过 200 MB。")
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(attachments) { attachment in
                    AttachmentRow(
                        attachment: attachment,
                        isTranscribing: transcribingAttachmentID == attachment.id,
                        isMutating: store.isMutating,
                        transcribe: { requestTranscription(attachment) },
                        delete: { deletingAttachment = attachment }
                    )
                }
                .listStyle(.inset)
            }

            if let notice {
                Label(notice, systemImage: "checkmark.circle.fill")
                    .font(.caption)
                    .foregroundStyle(.green)
                    .padding(.horizontal, 14)
                    .padding(.top, 10)
            }

            if let localError {
                Label(localError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(14)
            }
        }
        .task { await store.loadAttachments(noteID: noteID) }
        .fileImporter(
            isPresented: $isPickingFiles,
            allowedContentTypes: [.data],
            allowsMultipleSelection: true
        ) { result in
            switch result {
            case .success(let urls):
                Task { await upload(urls) }
            case .failure(let error):
                localError = error.localizedDescription
            }
        }
        .confirmationDialog(
            "转写会替换当前正文",
            isPresented: Binding(
                get: { pendingTranscription != nil },
                set: { if !$0 { pendingTranscription = nil } }
            ),
            presenting: pendingTranscription
        ) { attachment in
            Button("保存当前修改并开始转写", role: .destructive) {
                pendingTranscription = nil
                Task { await transcribe(attachment) }
            }
            Button("取消", role: .cancel) { pendingTranscription = nil }
        } message: { _ in
            Text("识别完成后，服务端会用转写结果替换整篇正文。当前标题和项目变更会先保存，但原正文将被替换。")
        }
        .confirmationDialog(
            "删除“\(deletingAttachment?.originalName ?? "")”？",
            isPresented: Binding(
                get: { deletingAttachment != nil },
                set: { if !$0 { deletingAttachment = nil } }
            )
        ) {
            Button("删除附件", role: .destructive) {
                guard let attachment = deletingAttachment else { return }
                Task {
                    do {
                        try await store.deleteAttachment(noteID: noteID, attachment: attachment)
                        deletingAttachment = nil
                    } catch {
                        localError = error.localizedDescription
                    }
                }
            }
        } message: {
            Text("该文件会从附件存储中永久删除。")
        }
    }

    private func requestTranscription(_ attachment: NoteAttachment) {
        localError = nil
        notice = nil
        guard transcribingAttachmentID == nil else { return }
        if currentBody.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            Task { await transcribe(attachment) }
        } else {
            pendingTranscription = attachment
        }
    }

    private func transcribe(_ attachment: NoteAttachment) async {
        transcribingAttachmentID = attachment.id
        localError = nil
        notice = nil
        defer { transcribingAttachmentID = nil }

        guard await prepareForTranscription() else {
            localError = "请先解决当前笔记的保存错误，再开始转写。"
            return
        }
        do {
            let result = try await store.transcribeVoiceAttachment(noteID: noteID, attachment: attachment)
            onTranscribed(result)
            notice = "转写完成，识别结果已写入正文。"
        } catch is CancellationError {
            return
        } catch {
            localError = transcriptionErrorMessage(error)
        }
    }

    private func transcriptionErrorMessage(_ error: Error) -> String {
        if let apiError = error as? APIError {
            if apiError.status == 503 {
                return "语音转写服务暂不可用，请先在设置中检查语音转写配置。"
            }
            if apiError.status == 409 {
                return "录音尚未上传完成，请稍后再试。"
            }
        }
        return (error as? LocalizedError)?.errorDescription ?? "语音转写失败，请稍后重试。"
    }

    private func upload(_ urls: [URL]) async {
        localError = nil
        notice = nil
        for url in urls.prefix(max(0, 20 - attachments.count)) {
            let didAccess = url.startAccessingSecurityScopedResource()
            defer { if didAccess { url.stopAccessingSecurityScopedResource() } }
            do {
                let values = try url.resourceValues(forKeys: [.fileSizeKey, .contentTypeKey])
                if let size = values.fileSize, size > 200 * 1_024 * 1_024 {
                    throw ValidationError("“\(url.lastPathComponent)”超过 200 MB")
                }
                let data = try Data(contentsOf: url, options: .mappedIfSafe)
                let type = values.contentType ?? UTType(filenameExtension: url.pathExtension)
                try await store.uploadAttachment(
                    noteID: noteID,
                    fileName: url.lastPathComponent,
                    mimeType: type?.preferredMIMEType ?? "application/octet-stream",
                    data: data
                )
            } catch {
                localError = error.localizedDescription
                return
            }
        }
    }

}

private struct AttachmentRow: View {
    let attachment: NoteAttachment
    let isTranscribing: Bool
    let isMutating: Bool
    let transcribe: () -> Void
    let delete: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: attachment.kind.systemImage)
                .font(.title3)
                .foregroundStyle(.tint)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 4) {
                Text(attachment.originalName)
                    .fontWeight(.medium)
                    .lineLimit(2)
                HStack {
                    Text(ByteCountFormatter.string(fromByteCount: attachment.sizeBytes, countStyle: .file))
                    if attachment.source == .voiceNote { Text("语音笔记") }
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                if let state = attachment.transcriptionState {
                    Text(state.label)
                        .font(.caption2)
                        .foregroundStyle(state == .failed ? .red : .secondary)
                }
                if let transcriptionError = attachment.transcriptionError,
                   !transcriptionError.isEmpty {
                    Text(transcriptionError)
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
            }
            Spacer()
            if attachment.source == .voiceNote {
                if isTranscribing {
                    ProgressView()
                        .controlSize(.small)
                        .help("转写中")
                        .accessibilityLabel("转写中")
                } else {
                            Button(actionTitle, systemImage: "text.bubble") { transcribe() }
                                .labelStyle(.iconOnly)
                                .buttonStyle(.borderless)
                                .disabled(attachment.transcriptionState == .processing || attachment.transcriptionState == .completed)
                                .help(actionTitle)
                                .accessibilityLabel(actionTitle)
                }
            }
            if attachment.deletable {
                Button("删除", systemImage: "trash", role: .destructive) {
                    delete()
                }
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
                .disabled(isMutating)
            }
        }
        .padding(.vertical, 5)
    }

    private var actionTitle: String {
        if isTranscribing || attachment.transcriptionState == .processing {
            return "转写中"
        }
        return switch attachment.transcriptionState {
        case .completed: "已转写"
        case .failed: "重试转写"
        default: "转成文字"
        }
    }
}
