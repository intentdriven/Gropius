// GropiusChat — a small native macOS client for a Gropius MLX server.
//
// It talks to the OpenAI-compatible endpoint another Mac exposes with Gropius:
// GET /v1/models to list what is available, POST /v1/chat/completions (streaming)
// to chat. Conversations are kept in a toggleable sidebar and persisted to disk.
//
// Built as a single-file SwiftUI app so it compiles with swiftc and packages
// into a .app without an Xcode project. See build.sh.

import SwiftUI

// MARK: - Model types

/// One turn in a conversation.
struct Message: Identifiable, Codable, Equatable {
    enum Role: String, Codable { case user, assistant }
    var id = UUID()
    var role: Role
    var text: String = ""
    /// Thinking models (e.g. Qwen3) stream their reasoning separately; we keep it
    /// so a reply that spends its whole budget reasoning is not shown as blank.
    var reasoning: String = ""
}

/// A saved chat: a title plus its messages.
struct Conversation: Identifiable, Codable {
    var id = UUID()
    var title: String = "New Chat"
    var messages: [Message] = []
    var createdAt = Date()
}

/// GET /v1/models
private struct ModelsResponse: Decodable {
    struct Model: Decodable { let id: String }
    let data: [Model]
}

/// One streamed chunk from /v1/chat/completions with stream=true.
private struct StreamChunk: Decodable {
    struct Choice: Decodable {
        struct Delta: Decodable {
            let content: String?
            let reasoning: String?
            let reasoning_content: String?
        }
        let delta: Delta
    }
    let choices: [Choice]
}

// MARK: - App model

@MainActor
final class AppModel: ObservableObject {
    // Persisted connection settings.
    @AppStorage("serverURL") var serverURL: String = "http://AlicesMac.local:11535"
    @AppStorage("apiKey") var apiKey: String = ""
    @AppStorage("selectedModel") var selectedModel: String = ""

    @Published var conversations: [Conversation] = []
    @Published var selectedID: UUID?

    @Published var models: [String] = []
    @Published var input: String = ""
    @Published var status: String = "Not connected"
    @Published var connected: Bool = false
    @Published var connecting: Bool = false
    @Published var sending: Bool = false

    /// Coarse connection health, for the status dot.
    enum Connection { case online, warning, offline }
    var connection: Connection {
        if !connected { return connecting ? .warning : .offline }
        return models.isEmpty ? .warning : .online
    }

    private var streamTask: Task<Void, Never>?

    init() {
        load()
        if conversations.isEmpty {
            let c = Conversation()
            conversations = [c]
            selectedID = c.id
        } else {
            selectedID = conversations.first?.id
        }
    }

    // MARK: Conversation management

    var currentIndex: Int? { conversations.firstIndex { $0.id == selectedID } }
    var currentMessages: [Message] { currentIndex.map { conversations[$0].messages } ?? [] }

    func newChat() {
        stop()
        let c = Conversation()
        conversations.insert(c, at: 0)
        selectedID = c.id
        save()
    }

    func deleteChat(_ id: UUID) {
        stop()
        conversations.removeAll { $0.id == id }
        if selectedID == id { selectedID = conversations.first?.id }
        if conversations.isEmpty {
            let c = Conversation()
            conversations = [c]
            selectedID = c.id
        }
        save()
    }

    // MARK: Persistence

    private var saveURL: URL {
        let base = FileManager.default
            .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("GropiusChat", isDirectory: true)
        return base.appendingPathComponent("conversations.json")
    }

    private func load() {
        guard let data = try? Data(contentsOf: saveURL),
              let saved = try? JSONDecoder().decode([Conversation].self, from: data)
        else { return }
        conversations = saved
    }

    func save() {
        let dir = saveURL.deletingLastPathComponent()
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        if let data = try? JSONEncoder().encode(conversations) {
            try? data.write(to: saveURL, options: .atomic)
        }
    }

    // MARK: Networking

    private var base: String {
        var s = serverURL.trimmingCharacters(in: .whitespaces)
        while s.hasSuffix("/") { s.removeLast() }
        return s
    }

    private func request(_ path: String) -> URLRequest? {
        guard let url = URL(string: base + path) else { return nil }
        var r = URLRequest(url: url)
        r.timeoutInterval = 60
        if !apiKey.isEmpty {
            r.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        }
        return r
    }

    /// Fetch the model list; doubles as the connection test.
    func connect() async {
        guard var req = request("/v1/models") else {
            status = "That server URL is not valid."
            return
        }
        req.httpMethod = "GET"
        connecting = true
        defer { connecting = false }
        status = "Connecting…"
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse else {
                status = "No response from the server."; connected = false; return
            }
            if http.statusCode == 401 {
                status = "The server requires an API key. Add one in Settings."
                connected = false; return
            }
            guard http.statusCode == 200 else {
                status = "Server returned HTTP \(http.statusCode)."; connected = false; return
            }
            let list = try JSONDecoder().decode(ModelsResponse.self, from: data)
            models = list.data.map(\.id).sorted()
            if selectedModel.isEmpty || !models.contains(selectedModel) {
                selectedModel = models.first ?? ""
            }
            connected = true
            status = models.isEmpty
                ? "Connected, but no models are downloaded yet."
                : "Connected · \(models.count) model\(models.count == 1 ? "" : "s")"
        } catch {
            connected = false
            status = "Could not reach \(base). Is Gropius running and on the same network?"
        }
    }

    func send() {
        let prompt = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty, !selectedModel.isEmpty, !sending,
              let idx = currentIndex, let convoID = selectedID else { return }
        input = ""
        conversations[idx].messages.append(Message(role: .user, text: prompt))
        if conversations[idx].title == "New Chat" {
            conversations[idx].title = String(prompt.prefix(48))
        }
        conversations[idx].messages.append(Message(role: .assistant))
        save()

        sending = true
        streamTask = Task { await stream(convoID: convoID) }
    }

    func stop() { streamTask?.cancel() }

    private func stream(convoID: UUID) async {
        defer { sending = false; save() }

        guard let ci0 = conversations.firstIndex(where: { $0.id == convoID }) else { return }
        let assistantIndex = conversations[ci0].messages.count - 1
        guard assistantIndex >= 0 else { return }

        guard var req = request("/v1/chat/completions") else { return }
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")

        let history: [[String: String]] = conversations[ci0].messages[..<assistantIndex].map {
            ["role": $0.role == .user ? "user" : "assistant", "content": $0.text]
        }
        let body: [String: Any] = [
            "model": selectedModel,
            "messages": history,
            "stream": true,
            "max_tokens": 2048,
        ]
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)

        func write(content: String = "", reasoning: String = "") {
            guard let ci = conversations.firstIndex(where: { $0.id == convoID }),
                  assistantIndex < conversations[ci].messages.count else { return }
            conversations[ci].messages[assistantIndex].text += content
            conversations[ci].messages[assistantIndex].reasoning += reasoning
        }
        func assistant() -> Message? {
            guard let ci = conversations.firstIndex(where: { $0.id == convoID }),
                  assistantIndex < conversations[ci].messages.count else { return nil }
            return conversations[ci].messages[assistantIndex]
        }

        do {
            let (bytes, resp) = try await URLSession.shared.bytes(for: req)
            if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                write(content: "⚠️ Server returned HTTP \(http.statusCode).")
                return
            }
            for try await line in bytes.lines {
                if Task.isCancelled { break }
                guard line.hasPrefix("data: ") else { continue }
                let payload = String(line.dropFirst(6))
                if payload == "[DONE]" { break }
                guard let d = payload.data(using: .utf8),
                      let chunk = try? JSONDecoder().decode(StreamChunk.self, from: d),
                      let delta = chunk.choices.first?.delta else { continue }
                if let c = delta.content, !c.isEmpty { write(content: c) }
                if let r = delta.reasoning ?? delta.reasoning_content, !r.isEmpty {
                    write(reasoning: r)
                }
            }
            // A thinking model can exhaust its token budget before emitting a final
            // answer. Rather than show nothing, fall back to the reasoning.
            if let m = assistant(), m.text.isEmpty, !m.reasoning.isEmpty,
               let ci = conversations.firstIndex(where: { $0.id == convoID }) {
                conversations[ci].messages[assistantIndex].text = m.reasoning
                conversations[ci].messages[assistantIndex].reasoning = ""
            }
        } catch is CancellationError {
            if assistant()?.text.isEmpty == true { write(content: "⏹ Stopped.") }
        } catch {
            write(content: "⚠️ \(error.localizedDescription)")
        }
    }
}

// MARK: - Styling

extension View {
    /// Apply the macOS 26 Liquid Glass button style, falling back to a bordered
    /// style on earlier systems so the app still builds and runs there. Toolbar
    /// buttons adopt Liquid Glass automatically; this is for the custom buttons
    /// (the composer, the settings sheet) so they match.
    @ViewBuilder
    func glassButton(prominent: Bool = false) -> some View {
        if #available(macOS 26.0, *) {
            if prominent { buttonStyle(.glassProminent) } else { buttonStyle(.glass) }
        } else {
            if prominent { buttonStyle(.borderedProminent) } else { buttonStyle(.bordered) }
        }
    }
}

// MARK: - Views

@main
struct GropiusChatApp: App {
    var body: some Scene {
        WindowGroup("Gropius Chat") {
            RootView().frame(minWidth: 720, minHeight: 480)
        }
    }
}

struct RootView: View {
    @StateObject private var model = AppModel()
    @State private var showSettings = false

    var body: some View {
        NavigationSplitView {
            Sidebar(model: model)
                .navigationSplitViewColumnWidth(min: 200, ideal: 240, max: 340)
        } detail: {
            ChatDetail(model: model, showSettings: $showSettings)
        }
        .sheet(isPresented: $showSettings) { SettingsView(model: model) }
        .task { await model.connect() }
    }
}

struct Sidebar: View {
    @ObservedObject var model: AppModel

    var body: some View {
        List(selection: Binding(get: { model.selectedID },
                                set: { model.selectedID = $0 })) {
            ForEach(model.conversations) { c in
                VStack(alignment: .leading, spacing: 2) {
                    Text(c.title.isEmpty ? "New Chat" : c.title)
                        .lineLimit(1)
                    Text("\(c.messages.count) message\(c.messages.count == 1 ? "" : "s")")
                        .font(.caption2).foregroundStyle(.secondary)
                }
                .tag(c.id)
                .contextMenu {
                    Button("Delete", role: .destructive) { model.deleteChat(c.id) }
                }
            }
            .onDelete { offsets in
                offsets.map { model.conversations[$0].id }.forEach(model.deleteChat)
            }
        }
        .navigationTitle("Chats")
        .toolbar {
            ToolbarItem {
                Button { model.newChat() } label: { Image(systemName: "square.and.pencil") }
                    .help("New chat")
            }
        }
    }
}

struct ChatDetail: View {
    @ObservedObject var model: AppModel
    @Binding var showSettings: Bool

    var body: some View {
        VStack(spacing: 0) {
            transcript
            Divider()
            composer
        }
        .navigationTitle("Gropius Chat")
        .toolbar {
            ToolbarItem {
                StatusDot(color: statusColor,
                          pulsing: model.connection == .online,
                          tooltip: model.status)
            }
            if model.connected && !model.models.isEmpty {
                ToolbarItem {
                    Picker("", selection: $model.selectedModel) {
                        ForEach(model.models, id: \.self) { Text(short($0)).tag($0) }
                    }
                    .labelsHidden().frame(minWidth: 140)
                    .help("Model")
                }
            }
            ToolbarItem {
                Button { Task { await model.connect() } } label: {
                    Image(systemName: "arrow.clockwise")
                }.help("Reconnect and refresh models")
            }
            ToolbarItem {
                Button { showSettings = true } label: { Image(systemName: "gearshape") }
                    .help("Server settings")
            }
        }
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    if model.currentMessages.isEmpty {
                        EmptyState(model: model)
                    }
                    ForEach(model.currentMessages) { m in
                        MessageRow(message: m).id(m.id)
                    }
                }
                .padding(12)
            }
            .onChange(of: model.currentMessages.last?.text) { _, _ in
                if let last = model.currentMessages.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }
            .onChange(of: model.selectedID) { _, _ in
                if let last = model.currentMessages.last { proxy.scrollTo(last.id, anchor: .bottom) }
            }
        }
    }

    private var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            TextField("Message…", text: $model.input, axis: .vertical)
                .textFieldStyle(.plain)
                .lineLimit(1...6)
                .padding(8)
                .background(RoundedRectangle(cornerRadius: 8).fill(.quaternary))
                .onSubmit { model.send() }
                .disabled(!model.connected)
            if model.sending {
                Button { model.stop() } label: {
                    Image(systemName: "stop.fill")
                        .font(.system(size: 15, weight: .bold))
                        .frame(width: 26, height: 26)
                }
                .glassButton()
                .help("Stop")
            } else {
                Button { model.send() } label: {
                    Image(systemName: "arrow.up")
                        .font(.system(size: 16, weight: .bold))
                        .frame(width: 26, height: 26)
                }
                .glassButton(prominent: true)
                .disabled(!model.connected || model.input.trimmingCharacters(in: .whitespaces).isEmpty)
                .help("Send")
            }
        }
        .padding(12)
    }

    private var statusColor: Color {
        switch model.connection {
        case .online:  return .green
        case .warning: return .orange
        case .offline: return .red
        }
    }

    private func short(_ id: String) -> String {
        id.contains("/") ? String(id.split(separator: "/").last!) : id
    }
}

/// A small connection indicator: green (online), orange (degraded), red (offline).
/// The online state pulses gently. The full status text is available on hover.
struct StatusDot: View {
    let color: Color
    let pulsing: Bool
    let tooltip: String
    @State private var animate = false

    var body: some View {
        ZStack {
            Circle()
                .fill(color.opacity(0.45))
                .frame(width: 9, height: 9)
                .scaleEffect(animate ? 2.4 : 1)
                .opacity(animate ? 0 : 0.7)
            Circle()
                .fill(color)
                .frame(width: 9, height: 9)
                .shadow(color: color.opacity(0.8), radius: pulsing ? 3 : 0)
        }
        .frame(width: 22, height: 18)
        .help(tooltip)
        .onAppear(perform: restart)
        .onChange(of: pulsing) { _, _ in restart() }
        .onChange(of: color) { _, _ in restart() }
    }

    private func restart() {
        animate = false
        guard pulsing else { return }
        withAnimation(.easeOut(duration: 1.5).repeatForever(autoreverses: false)) {
            animate = true
        }
    }
}

struct EmptyState: View {
    @ObservedObject var model: AppModel
    var body: some View {
        VStack(spacing: 14) {
            HStack(spacing: 4) {
                Rectangle().fill(.red).frame(width: 16, height: 26)
                Rectangle().fill(.yellow).frame(width: 16, height: 26)
                Rectangle().fill(.blue).frame(width: 16, height: 26)
            }
            Text(model.selectedModel.isEmpty
                 ? "Connect to a Gropius server to start."
                 : "Ask \(model.selectedModel.split(separator: "/").last.map(String.init) ?? "the model") anything.")
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 60)
    }
}

struct MessageRow: View {
    let message: Message
    private var isUser: Bool { message.role == .user }

    // Bubble colours, independent of the system accent (which may be anything):
    // iMessage blue for sent, green for replies.
    private static let sentBlue = Color(red: 0.039, green: 0.518, blue: 1.0)
    private static let replyGreen = Color(red: 0.204, green: 0.780, blue: 0.349)

    // Models often stream leading/trailing newlines (e.g. after the reasoning),
    // which would show as an empty line inside the bubble. Trim for display.
    private var displayText: String {
        message.text.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    private var displayReasoning: String {
        message.reasoning.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        HStack {
            if isUser { Spacer(minLength: 64) }
            VStack(alignment: isUser ? .trailing : .leading, spacing: 5) {
                // Thinking-model reasoning sits above the answer, muted, no bubble.
                if !displayReasoning.isEmpty {
                    Text(displayReasoning)
                        .font(.callout).italic()
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .padding(.horizontal, 6)
                        .frame(maxWidth: 560, alignment: .leading)
                }
                if displayText.isEmpty && !isUser {
                    ProgressView().controlSize(.small).padding(.vertical, 6).padding(.horizontal, 4)
                } else if !displayText.isEmpty {
                    Text(displayText)
                        .textSelection(.enabled)
                        .foregroundStyle(.white)
                        .padding(.horizontal, 13)
                        .padding(.vertical, 8)
                        .background(bubble)
                        .frame(maxWidth: 560, alignment: isUser ? .trailing : .leading)
                }
            }
            if !isUser { Spacer(minLength: 64) }
        }
    }

    @ViewBuilder private var bubble: some View {
        let shape = RoundedRectangle(cornerRadius: 18, style: .continuous)
        shape.fill(isUser ? Self.sentBlue : Self.replyGreen)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Server settings").font(.title3).bold()
            VStack(alignment: .leading, spacing: 4) {
                Text("Server URL").font(.caption).foregroundStyle(.secondary)
                TextField("http://AlicesMac.local:11535", text: $model.serverURL)
                    .textFieldStyle(.roundedBorder)
                Text("The Gropius server's address — the Mac's .local name or LAN IP, port 11535.")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: 4) {
                Text("API key (optional)").font(.caption).foregroundStyle(.secondary)
                SecureField("Only if the server requires one", text: $model.apiKey)
                    .textFieldStyle(.roundedBorder)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .glassButton()
                Button("Connect") { dismiss(); Task { await model.connect() } }
                    .glassButton(prominent: true)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}
