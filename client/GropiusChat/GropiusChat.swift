// GropiusChat — a small native macOS client for a Gropius MLX server.
//
// It talks to the OpenAI-compatible endpoint another Mac exposes with Gropius:
// GET /v1/models to list what is available, POST /v1/chat/completions (streaming)
// to chat. Nothing here is specific to a model — point it at the server, pick a
// model, type.
//
// Built as a single-file SwiftUI app so it compiles with swiftc and packages
// into a .app without an Xcode project. See build.sh.

import SwiftUI

// MARK: - Wire types

/// One turn in the transcript.
struct Message: Identifiable {
    enum Role { case user, assistant }
    let id = UUID()
    var role: Role
    var text: String = ""
    /// Thinking models (e.g. Qwen3) stream their reasoning separately; we keep it
    /// so a reply that spends its whole budget reasoning is not shown as blank.
    var reasoning: String = ""
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

// MARK: - Client

@MainActor
final class ChatModel: ObservableObject {
    // Persisted connection settings.
    @AppStorage("serverURL") var serverURL: String = "http://AlicesMac.local:11535"
    @AppStorage("apiKey") var apiKey: String = ""
    @AppStorage("selectedModel") var selectedModel: String = ""

    @Published var models: [String] = []
    @Published var messages: [Message] = []
    @Published var input: String = ""
    @Published var status: String = "Not connected"
    @Published var connected: Bool = false
    @Published var sending: Bool = false

    private var streamTask: Task<Void, Never>?

    /// The server base with any trailing slash trimmed.
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

    /// Fetch the model list; this doubles as the connection test.
    func connect() async {
        guard var req = request("/v1/models") else {
            status = "That server URL is not valid."
            return
        }
        req.httpMethod = "GET"
        status = "Connecting…"
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse else {
                status = "No response from the server."
                connected = false
                return
            }
            if http.statusCode == 401 {
                status = "The server requires an API key. Add one in Settings."
                connected = false
                return
            }
            guard http.statusCode == 200 else {
                status = "Server returned HTTP \(http.statusCode)."
                connected = false
                return
            }
            let list = try JSONDecoder().decode(ModelsResponse.self, from: data)
            models = list.data.map(\.id).sorted()
            if selectedModel.isEmpty || !models.contains(selectedModel) {
                selectedModel = models.first ?? ""
            }
            connected = true
            status = models.isEmpty
                ? "Connected, but the server has no models downloaded yet."
                : "Connected · \(models.count) model\(models.count == 1 ? "" : "s")"
        } catch {
            connected = false
            status = "Could not reach \(base). Is Gropius running and on the same network?"
        }
    }

    func send() {
        let prompt = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty, !selectedModel.isEmpty, !sending else { return }
        input = ""
        messages.append(Message(role: .user, text: prompt))
        messages.append(Message(role: .assistant))
        let assistantIndex = messages.count - 1

        sending = true
        streamTask = Task { await stream(into: assistantIndex) }
    }

    func stop() {
        streamTask?.cancel()
    }

    private func stream(into index: Int) async {
        defer { sending = false }

        guard var req = request("/v1/chat/completions") else { return }
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")

        // Build the message history from the transcript (excluding the empty
        // assistant turn we are about to fill in).
        let history: [[String: String]] = messages[..<index].map { m in
            ["role": m.role == .user ? "user" : "assistant", "content": m.text]
        }
        let body: [String: Any] = [
            "model": selectedModel,
            "messages": history,
            "stream": true,
            "max_tokens": 2048,
        ]
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)

        do {
            let (bytes, resp) = try await URLSession.shared.bytes(for: req)
            if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                messages[index].text = "⚠️ Server returned HTTP \(http.statusCode)."
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
                if let c = delta.content, !c.isEmpty {
                    messages[index].text += c
                }
                if let r = delta.reasoning ?? delta.reasoning_content, !r.isEmpty {
                    messages[index].reasoning += r
                }
            }
            // A thinking model can exhaust its token budget before emitting a final
            // answer. Rather than show nothing, fall back to the reasoning.
            if messages[index].text.isEmpty && !messages[index].reasoning.isEmpty {
                messages[index].text = messages[index].reasoning
                messages[index].reasoning = ""
            }
        } catch is CancellationError {
            if messages[index].text.isEmpty { messages[index].text = "⏹ Stopped." }
        } catch {
            messages[index].text = "⚠️ \(error.localizedDescription)"
        }
    }

    func newChat() {
        stop()
        messages.removeAll()
    }
}

// MARK: - Views

@main
struct GropiusChatApp: App {
    var body: some Scene {
        WindowGroup("Gropius Chat") {
            ContentView()
                .frame(minWidth: 560, minHeight: 480)
        }
        .windowResizability(.contentMinSize)
    }
}

struct ContentView: View {
    @StateObject private var model = ChatModel()
    @State private var showSettings = false

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            transcript
            Divider()
            composer
        }
        .sheet(isPresented: $showSettings) { SettingsView(model: model) }
        .task { await model.connect() }
    }

    private var header: some View {
        HStack(spacing: 12) {
            // Gropius mark: three primary blocks.
            HStack(spacing: 3) {
                Rectangle().fill(.red).frame(width: 10, height: 16)
                Rectangle().fill(.yellow).frame(width: 10, height: 16)
                Rectangle().fill(.blue).frame(width: 10, height: 16)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text("Gropius Chat").font(.headline)
                Text(model.status).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if model.connected && !model.models.isEmpty {
                Picker("", selection: $model.selectedModel) {
                    ForEach(model.models, id: \.self) { Text(short($0)).tag($0) }
                }
                .labelsHidden()
                .frame(maxWidth: 220)
            }
            Button {
                Task { await model.connect() }
            } label: { Image(systemName: "arrow.clockwise") }
                .help("Reconnect and refresh models")
            Button { model.newChat() } label: { Image(systemName: "square.and.pencil") }
                .help("New chat")
            Button { showSettings = true } label: { Image(systemName: "gearshape") }
                .help("Server settings")
        }
        .padding(12)
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    if model.messages.isEmpty {
                        Text("Ask the model anything. Messages go to \(model.selectedModel.isEmpty ? "the selected model" : short(model.selectedModel)) on your Gropius server.")
                            .foregroundStyle(.secondary)
                            .padding(.top, 40)
                            .frame(maxWidth: .infinity)
                            .multilineTextAlignment(.center)
                    }
                    ForEach(model.messages) { m in
                        MessageRow(message: m).id(m.id)
                    }
                }
                .padding(12)
            }
            .onChange(of: model.messages.last?.text) { _, _ in
                if let last = model.messages.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
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
                    Image(systemName: "stop.circle.fill").font(.title2)
                }.buttonStyle(.plain)
            } else {
                Button { model.send() } label: {
                    Image(systemName: "arrow.up.circle.fill").font(.title2)
                }
                .buttonStyle(.plain)
                .disabled(!model.connected || model.input.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(12)
    }

    private func short(_ id: String) -> String {
        id.contains("/") ? String(id.split(separator: "/").last!) : id
    }
}

struct MessageRow: View {
    let message: Message
    var body: some View {
        HStack {
            if message.role == .user { Spacer(minLength: 40) }
            VStack(alignment: .leading, spacing: 6) {
                if !message.reasoning.isEmpty {
                    Text(message.reasoning)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .italic()
                        .textSelection(.enabled)
                }
                if message.text.isEmpty && message.role == .assistant {
                    ProgressView().controlSize(.small)
                } else {
                    Text(message.text)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(10)
            .background(
                RoundedRectangle(cornerRadius: 10)
                    .fill(message.role == .user ? Color.accentColor.opacity(0.18) : Color(.textBackgroundColor))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 10).stroke(.quaternary, lineWidth: 1)
            )
            if message.role == .assistant { Spacer(minLength: 40) }
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: ChatModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Server settings").font(.title3).bold()
            VStack(alignment: .leading, spacing: 4) {
                Text("Server URL").font(.caption).foregroundStyle(.secondary)
                TextField("http://AlicesMac.local:11535", text: $model.serverURL)
                    .textFieldStyle(.roundedBorder)
                Text("The Gropius server's address. Use the Mac's .local name or its LAN IP, port 11535.")
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
                Button("Connect") {
                    dismiss()
                    Task { await model.connect() }
                }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}
