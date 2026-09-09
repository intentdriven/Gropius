// GropiusChat — a small native macOS client for a Gropius MLX server.
//
// It talks to the OpenAI-compatible endpoint a Mac exposes with Gropius — this
// one or another on the network: GET /v1/models to list what is available, POST
// /v1/chat/completions (streaming) to chat. Servers on the local network are
// found over Bonjour rather than typed in. Conversations are kept in a
// toggleable sidebar and persisted to disk.
//
// Built as a single-file SwiftUI app so it compiles with swiftc and packages
// into a .app without an Xcode project. See build.sh.

import Network
import SwiftUI
import Security

// MARK: - Keychain

/// Keychain-backed storage for the one secret this app holds: the server API
/// key. Storing it in UserDefaults (as an earlier build did) leaves it in
/// cleartext in the preferences plist, readable by any process running as the
/// user and by anything that syncs or backs up the home directory. The Keychain
/// gates it behind the login-keychain ACL instead.
enum Keychain {
    private static let service = "dev.gropius.chat"
    private static let account = "apiKey"

    private static var baseQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    static func read() -> String {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data,
              let value = String(data: data, encoding: .utf8)
        else { return "" }
        return value
    }

    static func write(_ value: String) {
        // Empty means "no key" — remove the item rather than store an empty secret.
        if value.isEmpty {
            SecItemDelete(baseQuery as CFDictionary)
            return
        }
        let attrs: [String: Any] = [
            kSecValueData as String: Data(value.utf8),
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlocked,
        ]
        if SecItemUpdate(baseQuery as CFDictionary, attrs as CFDictionary) == errSecItemNotFound {
            var add = baseQuery
            add.merge(attrs) { _, new in new }
            SecItemAdd(add as CFDictionary, nil)
        }
    }
}

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

// MARK: - Local network discovery

/// The mDNS service type a Gropius server advertises itself on.
///
/// It has to match the server's own (internal/discovery), and it is declared a
/// second time in Info.plist's NSBonjourServices — macOS Local Network Privacy
/// answers a browse for an undeclared type with an empty result set rather than
/// an error, so an omission there looks exactly like "no servers on this
/// network". A test in the server's suite holds all three to one value.
let gropiusServiceType = "_gropius._tcp"

/// One Gropius server seen on the local network.
///
/// Everything here comes out of the browse itself: the service instance name
/// and the TXT record the server publishes. Nothing has been resolved — a
/// service name is not an address — because resolving costs an mDNS round trip
/// per server and only the one the user picks is worth spending it on.
struct DiscoveredServer: Identifiable, Equatable {
    /// The service instance name, e.g. "Gropius (alices-mac)".
    let name: String
    /// The service type and domain exactly as browsed, kept so the resolver can
    /// ask for this service back rather than rebuild the triple from constants.
    let type: String
    let domain: String
    /// TXT "api": the dialect the endpoint speaks. This client speaks "openai".
    let api: String
    /// TXT "path": the base path the API is mounted under, e.g. "/v1".
    let path: String
    /// TXT "auth": "bearer" when the server requires an API key, "none" when it
    /// does not, "" when the record did not say.
    let auth: String
    /// TXT "models": how many models the server can serve, nil when unstated.
    let models: Int?

    var id: String { "\(name)|\(type)|\(domain)" }
    var authRequired: Bool { auth == "bearer" }
    var authStated: Bool { auth == "bearer" || auth == "none" }
    /// An empty api is treated as this client's dialect: an older server that
    /// publishes no hint is still an OpenAI-compatible endpoint.
    var speaksThisClientsAPI: Bool { api.isEmpty || api == "openai" }

    /// The one-line description under the server's name in Settings. Every part
    /// of it is a hint from the network, so it says what was advertised and
    /// never asserts more than that.
    var summary: String {
        var parts: [String] = []
        if authStated {
            parts.append(authRequired ? "API key required" : "No API key needed")
        } else {
            parts.append("Does not say whether it needs an API key")
        }
        if let models {
            parts.append("\(models) model\(models == 1 ? "" : "s")")
        }
        if !speaksThisClientsAPI {
            parts.append("speaks the \(api) API, not openai")
        }
        return parts.joined(separator: " · ")
    }

    init?(_ result: NWBrowser.Result) {
        guard case let .service(name, type, domain, _) = result.endpoint else { return nil }
        self.name = name
        self.type = type
        self.domain = domain
        var txt: [String: String] = [:]
        if case let .bonjour(record) = result.metadata {
            for key in ["api", "path", "auth", "models"] {
                txt[key] = record[key]
            }
        }
        api = txt["api"] ?? ""
        path = txt["path"] ?? ""
        auth = txt["auth"] ?? ""
        models = txt["models"].flatMap(Int.init)
    }
}

/// Browses the local network for Gropius servers.
///
/// It only ever lists what it finds. Connecting is the user's decision: a
/// client that auto-connected to the first server it saw would send the stored
/// bearer token to whichever machine on the network answered first.
@MainActor
final class ServerBrowser: ObservableObject {
    enum Status: Equatable {
        case stopped
        case searching
        /// The browse cannot run yet — most often local network access has not
        /// been granted. The reason is shown, because the user is the only one
        /// who can clear it.
        case waiting(String)
        case failed(String)
    }

    @Published private(set) var servers: [DiscoveredServer] = []
    @Published private(set) var status: Status = .stopped

    private var browser: NWBrowser?

    func start() {
        guard browser == nil else { return }
        status = .searching
        servers = []

        let browser = NWBrowser(
            for: .bonjourWithTXTRecord(type: gropiusServiceType, domain: nil),
            using: NWParameters())
        browser.stateUpdateHandler = { [weak self] state in
            Task { @MainActor in self?.apply(state) }
        }
        // The handler is called with the complete current result set, not a
        // delta, so replacing the list is also how a server that has left the
        // network stops being offered.
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            let found = results.compactMap(DiscoveredServer.init)
            Task { @MainActor in self?.apply(found) }
        }
        self.browser = browser
        browser.start(queue: .main)
    }

    func stop() {
        browser?.cancel()
        browser = nil
        servers = []
        status = .stopped
    }

    func restart() {
        stop()
        start()
    }

    private func apply(_ state: NWBrowser.State) {
        switch state {
        case .ready, .setup:
            status = .searching
        case .waiting(let error):
            status = .waiting(error.localizedDescription)
        case .failed(let error):
            // A failed browser never recovers on its own; drop it so "Search
            // again" can build a new one.
            browser?.cancel()
            browser = nil
            status = .failed(error.localizedDescription)
        case .cancelled:
            status = .stopped
        @unknown default:
            status = .searching
        }
    }

    private func apply(_ found: [DiscoveredServer]) {
        // One server seen on two interfaces arrives as two results; they carry
        // the same instance name, so collapse them.
        var seen = Set<String>()
        servers = found
            .filter { seen.insert($0.id).inserted }
            .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }
}

/// Turns a browsed service into an address that can be stored.
///
/// NWBrowser reports a service *name*; a URL needs a host and a port. Network
/// framework exposes no resolver of its own — the documented route is to open
/// an NWConnection and read the peer's IP back off the established path, which
/// yields an address that stops working the next time the server's DHCP lease
/// moves. NetService resolves to the host name the server publishes its address
/// records under instead ("gropius-<host>.local", which internal/discovery
/// picks precisely so it can own that name), and that keeps resolving after the
/// address changes. So: browse with NWBrowser, resolve with NetService.
final class ServiceResolver: NSObject, NetServiceDelegate {
    enum Outcome {
        case address(String)
        case failure(String)
    }

    private let service: NetService
    private var completion: ((Outcome) -> Void)?
    /// Held until an outcome is delivered: nothing else refers to a resolver
    /// once the button action that made it returns.
    private var keepAlive: ServiceResolver?

    init(server: DiscoveredServer) {
        service = NetService(domain: Self.qualified(server.domain),
                             type: Self.qualified(server.type),
                             name: server.name)
        super.init()
        service.delegate = self
    }

    /// NetService wants the wire form, with the trailing root dot.
    private static func qualified(_ s: String) -> String {
        s.hasSuffix(".") ? s : s + "."
    }

    /// Resolves, then calls completion exactly once on the main queue. The
    /// timeout is part of the contract: NetService reports a service that has
    /// left the network by failing to resolve it, not by any other signal.
    func resolve(timeout: TimeInterval = 5, completion: @escaping (Outcome) -> Void) {
        self.completion = completion
        keepAlive = self
        service.resolve(withTimeout: timeout)
    }

    private func deliver(_ outcome: Outcome) {
        service.stop()
        let done = completion
        completion = nil
        // keepAlive is this object's only strong reference by the time a
        // delegate callback runs, so it is released in the dispatched block,
        // after the last use of self -- not here, mid-method.
        DispatchQueue.main.async {
            done?(outcome)
            self.keepAlive = nil
        }
    }

    func netServiceDidResolveAddress(_ sender: NetService) {
        var host = sender.hostName ?? ""
        while host.hasSuffix(".") { host.removeLast() } // fully qualified on the wire

        // An SRV target is not a trusted string. mDNSResponder escapes only
        // "\", "." and non-printables, so a name published as
        // "host.local@evil.example" or "evil.example#" survives to here intact
        // -- and interpolating either into a URL moves the host: the first
        // makes "host.local" userinfo and evil.example the host, the second
        // truncates at the fragment. Either sends the stored bearer token to a
        // machine the user did not pick. So: a host name is accepted only as
        // the letters, digits, dots and hyphens a host name is made of, and the
        // URL is built field by field rather than by interpolation, so nothing
        // in the host can reach across into another component.
        //
        // An IPv6 literal is refused rather than bracketed: internal/discovery
        // publishes a name, so a literal here is not a shape this server
        // produces, and typing the address by hand still works.
        let hostCharacters = CharacterSet(charactersIn:
            "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-.")
        guard !host.isEmpty, host.count <= 253,
              host.unicodeScalars.allSatisfy(hostCharacters.contains),
              (1...65535).contains(sender.port)
        else {
            deliver(.failure("That server reported an address this client will not use."))
            return
        }

        var url = URLComponents()
        url.scheme = "http"
        url.host = host
        url.port = sender.port
        guard let address = url.string else {
            deliver(.failure("That server did not report an address."))
            return
        }
        deliver(.address(address))
    }

    func netService(_ sender: NetService, didNotResolve errorDict: [String: NSNumber]) {
        deliver(.failure("Could not work out that server's address — it may have left the network."))
    }

    /// Abandons the resolve: the completion is never called. Used when the user
    /// picks a different server, so a slow resolve for the one they moved off
    /// cannot land afterwards and overwrite the newer pick.
    func cancel() {
        service.stop()
        completion = nil
        // Released asynchronously: keepAlive is the only strong reference, and
        // the caller may be holding self no more firmly than this property does.
        DispatchQueue.main.async { self.keepAlive = nil }
    }
}

// MARK: - App model

@MainActor
final class AppModel: ObservableObject {
    /// The base path the OpenAI-compatible API is mounted under. Hand-typed
    /// addresses get this; a discovered server can name its own in TXT "path".
    static let defaultAPIPath = "/v1"

    // Persisted connection settings.
    //
    // The default address is this Mac's own server. The documented order
    // installs the server first and the client second on the same machine, so
    // loopback is the one address that is right before the user has told the
    // client anything — and it has to be an address that resolves, because the
    // composer stays disabled until a server answers.
    @AppStorage("serverURL") var serverURL: String = "http://localhost:11535"
    @AppStorage("serverPath") var serverPath: String = AppModel.defaultAPIPath
    @AppStorage("selectedModel") var selectedModel: String = ""

    /// The bearer token. Held in memory as @Published (so SettingsView's
    /// SecureField binds to it) but persisted to the Keychain, never
    /// UserDefaults. Loaded in init(); saved by SettingsView on change.
    @Published var apiKey: String = ""

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
        // Migrate a key saved by an earlier build (plaintext UserDefaults) into
        // the Keychain, then forget the plaintext copy.
        if let legacy = UserDefaults.standard.string(forKey: "apiKey"), !legacy.isEmpty {
            Keychain.write(legacy)
            UserDefaults.standard.removeObject(forKey: "apiKey")
        }
        apiKey = Keychain.read()
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

    /// The base path, sanitized. serverPath can come from a TXT record, which
    /// is unauthenticated network input: a value like "@example.net" appended
    /// raw would turn "host:11535" into userinfo and hand the bearer token to
    /// whatever host followed. So a path must be a plain, single-rooted path --
    /// no "." or ".." segment, which would climb back out of it -- or it is not
    /// used at all.
    private var apiPath: String {
        var p = serverPath.trimmingCharacters(in: .whitespaces)
        while p.hasSuffix("/") { p.removeLast() }
        if p.isEmpty { return AppModel.defaultAPIPath }
        if !p.hasPrefix("/") { p = "/" + p }
        let allowed = CharacterSet(charactersIn:
            "/ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        let segments = p.dropFirst().split(separator: "/", omittingEmptySubsequences: false)
        guard p.count <= 128,
              !p.contains("//"),
              p.unicodeScalars.allSatisfy(allowed.contains),
              !segments.contains(where: { $0 == "." || $0 == ".." })
        else { return AppModel.defaultAPIPath }
        return p
    }

    /// Point the client at a hand-typed address. The path resets with it: a
    /// typed address carries no TXT record, so a path left over from a
    /// previously picked server would silently misroute every request.
    func useTypedAddress(_ address: String) {
        serverURL = address
        serverPath = AppModel.defaultAPIPath
    }

    /// Point the client at a server found on the network, at the address its
    /// service resolved to and under the base path it advertises.
    func use(_ server: DiscoveredServer, resolvedAddress: String) {
        serverURL = resolvedAddress
        serverPath = server.path.isEmpty ? AppModel.defaultAPIPath : server.path
    }

    private func request(_ path: String) -> URLRequest? {
        // Require an http(s) URL with a host before attaching the bearer token.
        // The server URL is free-text; without this guard a stray scheme
        // (file://, ftp://) or a hostless string would still get the token
        // attached to whatever URL resulted.
        guard let url = URL(string: base + apiPath + path),
              let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https",
              let host = url.host, !host.isEmpty
        else { return nil }
        var r = URLRequest(url: url)
        r.timeoutInterval = 60
        if !apiKey.isEmpty {
            r.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        }
        return r
    }

    /// Fetch the model list; doubles as the connection test.
    func connect() async {
        guard var req = request("/models") else {
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

        guard var req = request("/chat/completions") else { return }
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
            // Assemble SSE lines from the raw byte stream ourselves, under hard
            // caps, rather than using bytes.lines: a hostile or broken server can
            // stream an unbounded body with no newline, and bytes.lines would
            // buffer it without limit. maxLineBytes bounds a single line;
            // maxTotalBytes bounds the whole response.
            let maxLineBytes = 1 << 20   // 1 MiB per SSE line
            let maxTotalBytes = 64 << 20 // 64 MiB per response
            var lineBuf = [UInt8]()
            var total = 0

            func handle(_ line: String) -> Bool {
                guard line.hasPrefix("data: ") else { return false }
                let payload = String(line.dropFirst(6))
                if payload == "[DONE]" { return true }
                guard let d = payload.data(using: .utf8),
                      let chunk = try? JSONDecoder().decode(StreamChunk.self, from: d),
                      let delta = chunk.choices.first?.delta else { return false }
                if let c = delta.content, !c.isEmpty { write(content: c) }
                if let r = delta.reasoning ?? delta.reasoning_content, !r.isEmpty {
                    write(reasoning: r)
                }
                return false
            }

            for try await b in bytes {
                if Task.isCancelled { break }
                total += 1
                if total > maxTotalBytes {
                    write(content: "\n⚠️ Response exceeded \(maxTotalBytes >> 20) MB — stopped.")
                    break
                }
                if b == 0x0A { // LF: end of an SSE line
                    if let line = String(bytes: lineBuf, encoding: .utf8), handle(line) { break }
                    lineBuf.removeAll(keepingCapacity: true)
                    continue
                }
                if b == 0x0D { continue } // ignore CR so CRLF is handled
                // Past the per-line cap, drop bytes until the next newline rather
                // than buffer an unbounded line.
                if lineBuf.count < maxLineBytes { lineBuf.append(b) }
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
    /// Apply the macOS 26 Liquid Glass button style. Toolbar buttons adopt
    /// Liquid Glass automatically; this is for the custom buttons (the
    /// composer, the settings sheet) so they match. The app's deployment
    /// target is macOS 26, so no availability fallback is needed.
    @ViewBuilder
    func glassButton(prominent: Bool = false) -> some View {
        if prominent { buttonStyle(.glassProminent) } else { buttonStyle(.glass) }
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
                        EmptyState(model: model, openSettings: { showSettings = true })
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
        VStack(alignment: .leading, spacing: 6) {
            composerRow
            // A disabled text field explains nothing by itself. Say why it is
            // disabled and where the fix is, rather than leaving the user to
            // guess at a box that will not take a keystroke.
            if !model.connected {
                HStack(spacing: 5) {
                    Image(systemName: "exclamationmark.circle")
                    Text("Not connected, so messages can't be sent yet.")
                    Button("Open Settings…") { showSettings = true }
                        .buttonStyle(.link)
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
        }
        .padding(12)
    }

    private var composerRow: some View {
        HStack(alignment: .bottom, spacing: 8) {
            TextField(model.connected ? "Message…" : "Connect to a server to start typing",
                      text: $model.input, axis: .vertical)
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
    /// Opens the settings sheet. Without it this view could only *tell* a
    /// first-run user to go and connect somewhere, in a window that offered
    /// them nothing to click and a message box they could not type in.
    var openSettings: () -> Void

    var body: some View {
        VStack(spacing: 14) {
            HStack(spacing: 4) {
                Rectangle().fill(.red).frame(width: 16, height: 26)
                Rectangle().fill(.yellow).frame(width: 16, height: 26)
                Rectangle().fill(.blue).frame(width: 16, height: 26)
            }
            if model.connected {
                Text(model.selectedModel.isEmpty
                     ? "Connected, but this server has no models to serve yet."
                     : "Ask \(model.selectedModel.split(separator: "/").last.map(String.init) ?? "the model") anything.")
                    .foregroundStyle(.secondary)
            } else {
                disconnected
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 60)
    }

    private var disconnected: some View {
        VStack(spacing: 10) {
            Text("Not connected to a Gropius server.")
                .foregroundStyle(.secondary)
            Text(model.status)
                .font(.caption)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
            Button("Open Settings…") { openSettings() }
                .glassButton(prominent: true)
            Text("Settings holds the server's address, and lists the Gropius servers it can find on your network.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
        }
    }
}

struct MessageRow: View {
    let message: Message
    @State private var showReasoning = false
    private var isUser: Bool { message.role == .user }
    // Thinking is in progress while the assistant has streamed reasoning but no
    // answer text yet.
    private var isThinking: Bool { !isUser && displayText.isEmpty }

    // Bubble colors, independent of the system accent (which may be anything):
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
                // Thinking-model reasoning collapses behind a "Thinking…" line with
                // a disclosure toggle. Collapsed by default.
                if !displayReasoning.isEmpty {
                    reasoningDisclosure
                }
                // Only spin when nothing at all has arrived yet; once reasoning is
                // streaming, the "Thinking…" line is the activity indicator.
                if displayText.isEmpty && !isUser && displayReasoning.isEmpty {
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

    @ViewBuilder private var reasoningDisclosure: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                withAnimation(.easeInOut(duration: 0.15)) { showReasoning.toggle() }
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: showReasoning ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                    if isThinking { ProgressView().controlSize(.mini) }
                    Text(isThinking ? "Thinking…" : "Thoughts")
                        .font(.caption)
                }
                .foregroundStyle(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if showReasoning {
                Text(displayReasoning)
                    .font(.callout).italic()
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .frame(maxWidth: 560, alignment: .leading)
                    .transition(.opacity)
            }
        }
        .padding(.horizontal, 6)
        .frame(maxWidth: 560, alignment: .leading)
    }

    @ViewBuilder private var bubble: some View {
        let shape = RoundedRectangle(cornerRadius: 18, style: .continuous)
        shape.fill(isUser ? Self.sentBlue : Self.replyGreen)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AppModel
    @StateObject private var browser = ServerBrowser()
    /// The server currently being resolved, and why the last attempt failed.
    @State private var resolving: String?
    @State private var resolveError: String?
    /// The resolve in flight, and which one it is. Resolves take as long as the
    /// network makes them take, so a second pick can be answered before the
    /// first: the generation says whose answer is still wanted.
    @State private var resolver: ServiceResolver?
    @State private var resolveGeneration = 0
    @Environment(\.dismiss) private var dismiss

    /// Typing in the address field is a hand-typed address, which resets the
    /// base path — see AppModel.useTypedAddress.
    private var typedAddress: Binding<String> {
        Binding(get: { model.serverURL }, set: { model.useTypedAddress($0) })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Server settings").font(.title3).bold()
            VStack(alignment: .leading, spacing: 4) {
                Text("Server URL").font(.caption).foregroundStyle(.secondary)
                TextField("http://alices-mac.local:11535", text: typedAddress)
                    .textFieldStyle(.roundedBorder)
                Text("The Gropius server's address — the Mac's .local name or LAN IP, port 11535. "
                     + "On the Mac running the server, that is http://localhost:11535.")
                    .font(.caption2).foregroundStyle(.secondary)
            }
            discovered
            VStack(alignment: .leading, spacing: 4) {
                Text("API key (optional)").font(.caption).foregroundStyle(.secondary)
                SecureField("Only if the server requires one", text: $model.apiKey)
                    .textFieldStyle(.roundedBorder)
                    .onChange(of: model.apiKey) { _, newValue in
                        Keychain.write(newValue)
                    }
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
        .frame(width: 460)
        .onAppear { browser.start() }
        .onDisappear {
            browser.stop()
            resolver?.cancel()
            resolver = nil
            resolving = nil
        }
    }

    // MARK: Servers on this network

    /// The browse results. Picking one fills the address field in; it never
    /// connects on its own, so the choice of which machine gets the API key
    /// stays with the user.
    private var discovered: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("On your network").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Search again") {
                    resolveError = nil
                    browser.restart()
                }
                .buttonStyle(.link).font(.caption)
            }
            switch browser.status {
            case .failed(let why):
                note("Could not search the local network: \(why)", systemImage: "exclamationmark.triangle")
            case .waiting(let why):
                note("Waiting to search the local network — \(why)", systemImage: "clock")
            case .stopped, .searching:
                if browser.servers.isEmpty {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.small)
                        Text("Looking for Gropius servers… if none appear, type the address above.")
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                } else {
                    serverList
                }
            }
            if let resolveError {
                note(resolveError, systemImage: "exclamationmark.triangle")
            }
        }
    }

    private var serverList: some View {
        ScrollView {
            VStack(spacing: 0) {
                ForEach(browser.servers) { server in
                    Button { pick(server) } label: { row(server) }
                        .buttonStyle(.plain)
                    Divider()
                }
            }
        }
        // Bounded, so a network full of servers cannot push the buttons below
        // off the sheet.
        .frame(maxHeight: 130)
        .background(RoundedRectangle(cornerRadius: 6).fill(.quaternary))
    }

    private func row(_ server: DiscoveredServer) -> some View {
        HStack(spacing: 8) {
            Image(systemName: server.authRequired ? "lock.fill" : "network")
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 1) {
                Text(server.name).lineLimit(1)
                Text(server.summary).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            if resolving == server.id { ProgressView().controlSize(.small) }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .contentShape(Rectangle())
    }

    private func note(_ text: String, systemImage: String) -> some View {
        Label(text, systemImage: systemImage)
            .font(.caption2)
            .foregroundStyle(.secondary)
    }

    /// Resolve the picked service to an address and put it in the field. The
    /// resolve is where a server that has left the network is found out: the
    /// browse can still be listing a service whose machine has gone.
    ///
    /// Only the newest pick may write the field. Picking a slow server and then
    /// a fast one would otherwise end with the slow one's address in the field,
    /// several seconds after the user watched the fast one land there.
    private func pick(_ server: DiscoveredServer) {
        resolver?.cancel()
        resolveError = nil
        resolving = server.id
        resolveGeneration += 1
        let generation = resolveGeneration

        let resolver = ServiceResolver(server: server)
        self.resolver = resolver
        resolver.resolve { outcome in
            guard generation == resolveGeneration else { return }
            resolving = nil
            self.resolver = nil
            switch outcome {
            case .address(let address):
                model.use(server, resolvedAddress: address)
            case .failure(let why):
                resolveError = "\(server.name): \(why)"
            }
        }
    }
}
