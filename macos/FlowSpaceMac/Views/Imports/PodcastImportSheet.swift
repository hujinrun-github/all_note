import SwiftUI

struct PodcastImportSheet: View {
    enum Phase: Equatable {
        case enteringURL
        case resolving
        case configured(ResolvedPodcastEpisode)
        case submitting(ResolvedPodcastEpisode)
        case failed(String, ResolvedPodcastEpisode?)
    }

    @Environment(\.dismiss) private var dismiss
    let request: ContentImportPresentation
    let store: WorkspaceStore
    @State private var draft: ContentImportDraft
    @State private var phase: Phase = .enteringURL

    init(request: ContentImportPresentation, store: WorkspaceStore) {
        self.request = request
        self.store = store
        var draft = ContentImportDraft()
        draft.projectID = request.projectID
        _draft = State(initialValue: draft)
    }

    private var episode: ResolvedPodcastEpisode? {
        switch phase {
        case .configured(let episode), .submitting(let episode): episode
        case .failed(_, let episode): episode
        case .enteringURL, .resolving: nil
        }
    }

    private var isBusy: Bool {
        if case .resolving = phase { return true }
        if case .submitting = phase { return true }
        return false
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top, spacing: 14) {
                Image(systemName: "podcasts")
                    .font(.title)
                    .foregroundStyle(.tint)
                    .frame(width: 42, height: 42)
                    .background(.tint.opacity(0.1), in: .rect(cornerRadius: 10))
                VStack(alignment: .leading, spacing: 3) {
                    Text("导入播客").font(.title2.weight(.semibold))
                    Text("先生成可靠的逐字稿，再按需用 AI 整理成笔记。")
                        .foregroundStyle(.secondary)
                }
            }
            .padding(22)

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    TextField("小宇宙或 Apple Podcasts 单集链接", text: $draft.sourceURL)
                        .textFieldStyle(.roundedBorder)
                        .disabled(isBusy || episode != nil)
                        .onChange(of: draft.sourceURL) {
                            if episode == nil, !isBusy { phase = .enteringURL }
                        }

                    if case .failed(let message, _) = phase {
                        Label(message, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                            .font(.callout)
                    }

                    if let episode {
                        episodeCard(episode)
                        options(episode)
                    } else {
                        ContentUnavailableView {
                            Label("支持公开单集链接", systemImage: "headphones")
                        } description: {
                            Text("解析只读取公开元数据，不会在这一步调用转写或文本 AI。")
                        }
                        .frame(maxWidth: .infinity, minHeight: 190)
                    }
                }
                .padding(22)
            }

            Divider()

            HStack {
                if isBusy { ProgressView().controlSize(.small) }
                Text(footerMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("取消") { dismiss() }.disabled(isBusy)
                if episode != nil {
                    Button("重新选择链接") {
                        phase = .enteringURL
                    }
                    .disabled(isBusy)
                }
                Button(primaryTitle) { primaryAction() }
                    .buttonStyle(.borderedProminent)
                    .disabled(isBusy || draft.sourceURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(16)
        }
        .frame(width: 640, height: episode == nil ? 480 : 720)
        .interactiveDismissDisabled(isBusy)
    }

    private func episodeCard(_ episode: ResolvedPodcastEpisode) -> some View {
        GroupBox {
            HStack(alignment: .top, spacing: 14) {
                if let coverURL = episode.coverURL.flatMap(URL.init(string:)) {
                    AsyncImage(url: coverURL) { image in
                        image.resizable().scaledToFill()
                    } placeholder: {
                        Image(systemName: "waveform")
                            .foregroundStyle(.secondary)
                    }
                    .frame(width: 76, height: 76)
                    .clipShape(.rect(cornerRadius: 9))
                } else {
                    Image(systemName: "waveform")
                        .font(.title)
                        .frame(width: 76, height: 76)
                        .background(.quaternary, in: .rect(cornerRadius: 9))
                }
                VStack(alignment: .leading, spacing: 5) {
                    Text(episode.sourceType == "apple" ? "Apple Podcasts" : "小宇宙")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.tint)
                    Text(episode.title).font(.headline).lineLimit(2)
                    HStack {
                        Text(episode.podcastTitle ?? "播客节目")
                        if let duration = episode.durationSeconds { Text("· \(durationLabel(duration))") }
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    Label(
                        episode.hasPublicTranscript ? "发现发布者逐字稿" : "将从公开音频生成逐字稿",
                        systemImage: episode.hasPublicTranscript ? "text.document" : "waveform.badge.magnifyingglass"
                    )
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                Spacer()
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private func options(_ episode: ResolvedPodcastEpisode) -> some View {
        VStack(alignment: .leading, spacing: 15) {
            Toggle(isOn: $draft.summarizeWithAI) {
                VStack(alignment: .leading, spacing: 2) {
                    Label("使用 AI 整理", systemImage: "sparkles")
                    Text("需要工作区已配置文本 AI；关闭时直接保存完整逐字稿。")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            if draft.summarizeWithAI {
                TextField("AI 总结提示词", text: $draft.summaryPrompt, axis: .vertical)
                    .lineLimit(4...7)
                HStack {
                    Toggle("在笔记末尾附上完整逐字稿", isOn: $draft.includeTranscript)
                    Spacer()
                    Button("恢复默认提示词") {
                        draft.summaryPrompt = ContentImportDraft.defaultSummaryPrompt
                    }
                    .buttonStyle(.link)
                }
            }

            Divider()

            Picker("关联项目", selection: $draft.projectID) {
                Text("未归属").tag("")
                ForEach(store.projects) { project in Text(project.name).tag(project.id) }
            }
            TextField("标签（逗号分隔）", text: $draft.tags)
        }
        .padding(16)
        .background(.quaternary.opacity(0.35), in: .rect(cornerRadius: 12))
    }

    private var primaryTitle: String {
        switch phase {
        case .resolving: "解析中"
        case .submitting: "提交中"
        case .configured, .failed(_, .some): draft.summarizeWithAI ? "开始转写并整理" : "开始转写"
        case .enteringURL, .failed(_, .none): "解析链接"
        }
    }

    private var footerMessage: String {
        episode == nil ? "解析与转录是两个独立步骤" : "任务提交后会在后台继续处理"
    }

    private func primaryAction() {
        if let episode {
            phase = .submitting(episode)
            Task {
                do {
                    _ = try await store.createContentImport(from: draft)
                    dismiss()
                } catch {
                    phase = .failed(error.localizedDescription, episode)
                }
            }
        } else {
            phase = .resolving
            Task {
                do {
                    phase = .configured(try await store.resolvePodcast(sourceURL: draft.sourceURL))
                } catch {
                    phase = .failed(error.localizedDescription, nil)
                }
            }
        }
    }

    private func durationLabel(_ seconds: Int64) -> String {
        let minutes = Int((seconds + 30) / 60)
        return minutes >= 60 ? "\(minutes / 60) 小时 \(minutes % 60) 分" : "\(minutes) 分钟"
    }
}
