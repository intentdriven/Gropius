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

import AppKit
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

/// The residency value the models list carries for a model that is already in
/// memory. The server publishes three — `loaded`, `loading`, `not_loaded` — and
/// only this one means a request is served without a wait. A test in the
/// server's suite holds it to the value the pool reports.
let residencyLoaded = "loaded"

/// The SSE comment the server sends, about once a second, while it is loading a
/// model to serve a streaming request; the data frames follow once the model is
/// up.
///
/// A colon starts a comment in the SSE format, so this rides the existing
/// stream without changing its content type or its status code, and a client
/// that knows nothing about it ignores the line as the format says to. A test
/// in the server's suite holds this client to the same wire form.
let modelLoadingComment = ": loading"

/// GET /v1/models
private struct ModelsResponse: Decodable {
    struct Model: Decodable {
        let id: String
        /// Residency: `loaded`, `loading` or `not_loaded`. Absent on a server
        /// that does not publish residency to this client, which is not the
        /// same as "not loaded" — it is "not said", and nothing is claimed
        /// from it.
        let state: String?
        /// Whether the model can serve a chat request at all. Absent on a
        /// server that does not publish the capability.
        let chat: Bool?

        /// Absent means yes. A server that publishes no capability is an older
        /// one, and every model it serves must still be offered — defaulting
        /// the other way would empty the picker against every server already
        /// installed.
        var chattable: Bool { chat ?? true }
    }
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

    /// Every model the server serves, and the subset the picker offers. They
    /// differ by the chat capability: a model that cannot serve a chat request
    /// stays callable over the API by name and simply is not offered here.
    @Published var models: [String] = []
    @Published var chatModels: [String] = []
    @Published var input: String = ""
    @Published var status: String = "Not connected"
    @Published var connected: Bool = false
    @Published var connecting: Bool = false
    @Published var sending: Bool = false

    /// What the client is waiting for, once a message has been sent.
    ///
    /// Loading a model into memory takes seconds to a minute; generating an
    /// answer from a model already in memory starts at once. The two look
    /// identical from the outside — nothing arrives — so they are told apart
    /// here and shown differently, and `loading` is only ever entered on
    /// evidence: an SSE comment from the server, or a residency reading that
    /// says the chosen model is not in memory.
    enum Activity: Equatable { case idle, loading, generating }
    @Published var activity: Activity = .idle

    /// The line shown in place of the bare spinner while a model is loading,
    /// or nil when there is nothing to say beyond "working".
    var loadingLabel: String? {
        guard sending, activity == .loading else { return nil }
        let name = selectedModel.split(separator: "/").last.map(String.init) ?? selectedModel
        return name.isEmpty ? "Loading the model…" : "Loading \(name)…"
    }

    /// Coarse connection health, for the status dot.
    enum Connection { case online, warning, offline }
    var connection: Connection {
        if !connected { return connecting ? .warning : .offline }
        return models.isEmpty ? .warning : .online
    }

    private var streamTask: Task<Void, Never>?
    /// The residency poll that runs while a request is in flight. It is a
    /// fallback for a server that sends no loading comments, and it is stopped
    /// the moment the stream says anything at all.
    private var residencyTask: Task<Void, Never>?

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
            // The picker offers what can chat. The rest stay served — an API
            // client that asks for an OCR model by name still gets it — they
            // are just not put in front of someone about to type a sentence.
            chatModels = list.data.filter(\.chattable).map(\.id).sorted()
            if selectedModel.isEmpty || !chatModels.contains(selectedModel) {
                selectedModel = chatModels.first ?? ""
            }
            connected = true
            if models.isEmpty {
                status = "Connected, but no models are downloaded yet."
            } else if chatModels.isEmpty {
                status = "Connected · \(models.count) model\(models.count == 1 ? "" : "s"), none of them for chat"
            } else {
                status = "Connected · \(models.count) model\(models.count == 1 ? "" : "s")"
            }
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
        // Generating until something says otherwise. A server that sends no
        // loading comments and publishes no residency leaves it here, which is
        // the behaviour this client had before either signal existed.
        activity = .generating
        streamTask = Task { await stream(convoID: convoID) }
        startResidencyPoll(for: selectedModel)
    }

    func stop() { streamTask?.cancel() }

    /// The server has said something about this request, so the residency poll
    /// has nothing left to add: the stream itself is now the better witness.
    private func streamSpoke() {
        residencyTask?.cancel()
        residencyTask = nil
    }

    /// Watch the models list while the request waits, for a server that sends
    /// no loading comments.
    ///
    /// Residency is a snapshot and it is not always published — an unkeyed
    /// server reached from another machine says nothing — so an absent or
    /// unreadable answer changes nothing. Only a definite "not in memory"
    /// moves the client into the loading state, and a definite "in memory"
    /// moves it back out: the wait is then a slow answer, not a load.
    private func startResidencyPoll(for model: String) {
        residencyTask?.cancel()
        guard !model.isEmpty else { residencyTask = nil; return }
        residencyTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled, let self, self.sending else { return }
                guard let state = await self.residency(of: model) else { continue }
                guard !Task.isCancelled, self.sending else { return }
                self.activity = state == residencyLoaded ? .generating : .loading
            }
        }
    }

    /// One reading of a model's residency, or nil when the server did not say.
    private func residency(of model: String) async -> String? {
        guard var req = request("/models") else { return nil }
        req.httpMethod = "GET"
        req.timeoutInterval = 10
        guard let (data, resp) = try? await URLSession.shared.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200,
              let list = try? JSONDecoder().decode(ModelsResponse.self, from: data)
        else { return nil }
        // The server folds repository ids case-insensitively, and the id sent
        // in the request came out of this same listing; comparing the same way
        // keeps a spelling difference from reading as a different model.
        return list.data.first { $0.id.caseInsensitiveCompare(model) == .orderedSame }?.state
    }

    private func stream(convoID: UUID) async {
        defer {
            sending = false
            activity = .idle
            streamSpoke()
            save()
        }

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

            // An SSE comment, stripped of its colon and the optional space
            // after it. Both spellings are the same comment on the wire.
            func comment(_ line: String) -> String {
                String(line.dropFirst()).trimmingCharacters(in: .whitespaces)
            }
            let loadingMarker = comment(modelLoadingComment)

            func handle(_ line: String) -> Bool {
                // A line beginning with a colon is a comment, which the SSE
                // format says to ignore. The server uses one to say the wait is
                // a model load rather than a slow answer; any other comment is
                // ignored, as the format requires.
                if line.hasPrefix(":") {
                    streamSpoke()
                    if comment(line).hasPrefix(loadingMarker) { activity = .loading }
                    return false
                }
                guard line.hasPrefix("data:") else { return false }
                // The first data frame ends the load, whatever the comments
                // said: the model is answering.
                streamSpoke()
                activity = .generating
                var payload = String(line.dropFirst(5))
                if payload.hasPrefix(" ") { payload.removeFirst() }
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
            if model.connected && !model.chatModels.isEmpty {
                ToolbarItem {
                    Picker("", selection: $model.selectedModel) {
                        ForEach(model.chatModels, id: \.self) { Text(short($0)).tag($0) }
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
                        // Only the message being streamed can be waiting on a
                        // load, so only it is told about one.
                        MessageRow(message: m,
                                   loadingLabel: m.id == model.currentMessages.last?.id
                                       ? model.loadingLabel : nil)
                            .id(m.id)
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
            ComposerField(
                text: $model.input,
                isEnabled: model.connected,
                // The disabled placeholder is half the explanation of why the
                // field will not take a keystroke; the line under the composer
                // is the other half.
                placeholder: model.connected ? "Message…" : "Connect to a server to start typing",
                onSubmit: { model.send() })
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
                .disabled(!model.connected
                          || model.input.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
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

/// The message composer: a bordered, multi-line text input that grows with what
/// is typed into it.
///
/// It is an NSTextView rather than a SwiftUI TextField because three of the
/// things a composer has to do are AppKit's to give: Return sends while
/// Shift-Return inserts a newline (a key command a text field cannot intercept
/// without giving up its own editing), the control grows line by line to a
/// ceiling and then scrolls, and it reports its own first-responder state so the
/// focus ring can be drawn where macOS draws one. The surface is drawn from the
/// platform's own colours, so it follows light and dark, the chosen accent, and
/// Increase Contrast.
struct ComposerField: View {
    @Binding var text: String
    var isEnabled: Bool
    var placeholder: String
    var onSubmit: () -> Void

    @State private var height: CGFloat = ComposerField.minHeight
    @State private var focused = false
    @Environment(\.colorSchemeContrast) private var contrast

    /// One line, and roughly seven before it starts scrolling instead.
    static let minHeight: CGFloat = 21
    static let maxHeight: CGFloat = 150
    private static let corner: CGFloat = 7
    private static let inset = NSSize(width: 6, height: 5)
    /// NSTextContainer's own default padding, which the text sits behind. The
    /// placeholder has to clear the same distance or it lands off the caret.
    private static let lineFragmentPadding: CGFloat = 5

    var body: some View {
        ZStack(alignment: .topLeading) {
            ComposerTextView(text: $text,
                             height: $height,
                             focused: $focused,
                             isEnabled: isEnabled,
                             minHeight: Self.minHeight,
                             maxHeight: Self.maxHeight,
                             inset: Self.inset,
                             onSubmit: onSubmit)
                .frame(height: height)
            if text.isEmpty {
                Text(placeholder)
                    .foregroundStyle(isEnabled ? .secondary : .tertiary)
                    .padding(.leading, Self.inset.width + Self.lineFragmentPadding)
                    .padding(.top, Self.inset.height)
                    .allowsHitTesting(false)
            }
        }
        .padding(4)
        .background(surface)
        .animation(.easeOut(duration: 0.12), value: height)
        .accessibilityLabel("Message")
    }

    /// The field's own surface: a text background inside a hairline, and the
    /// accent colour for the focus ring. Disabled it drops to the window's
    /// background, so a field that will not take a keystroke does not look like
    /// one that will.
    @ViewBuilder private var surface: some View {
        let shape = RoundedRectangle(cornerRadius: Self.corner, style: .continuous)
        let showFocus = focused && isEnabled
        let increased = contrast == .increased
        shape
            .fill(Color(nsColor: isEnabled ? .textBackgroundColor : .windowBackgroundColor))
            .overlay(
                shape.strokeBorder(
                    showFocus ? Color.accentColor : Color(nsColor: .separatorColor),
                    lineWidth: showFocus ? 2 : (increased ? 1.5 : 1)))
    }
}

/// The AppKit half of ComposerField.
struct ComposerTextView: NSViewRepresentable {
    @Binding var text: String
    @Binding var height: CGFloat
    @Binding var focused: Bool
    var isEnabled: Bool
    var minHeight: CGFloat
    var maxHeight: CGFloat
    var inset: NSSize
    var onSubmit: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        // The TextKit 1 stack, assembled by hand. The height the composer grows
        // to is measured off the layout manager, and a text view left to choose
        // its own stack would answer that question from whichever one it picked
        // — on a newer system, one that has no layout manager to ask.
        let storage = NSTextStorage()
        let layout = NSLayoutManager()
        storage.addLayoutManager(layout)
        let container = NSTextContainer(size: NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude))
        container.widthTracksTextView = true
        layout.addTextContainer(container)

        let view = ComposerNSTextView(frame: .zero, textContainer: container)
        view.delegate = context.coordinator
        view.isRichText = false
        view.allowsUndo = true
        view.drawsBackground = false
        view.font = NSFont.preferredFont(forTextStyle: .body)
        view.textContainerInset = inset
        view.isVerticallyResizable = true
        view.isHorizontallyResizable = false
        view.autoresizingMask = [NSView.AutoresizingMask.width]
        view.minSize = NSSize(width: 0, height: 0)
        view.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude,
                              height: CGFloat.greatestFiniteMagnitude)
        view.string = text
        let coordinator = context.coordinator
        view.onFocusChange = { isFocused in
            // Reported from becomeFirstResponder, which can run inside a
            // SwiftUI update; handing it to the next turn keeps it out of one.
            DispatchQueue.main.async { coordinator.parent.focused = isFocused }
        }

        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.documentView = view
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let view = scroll.documentView as? ComposerNSTextView else { return }
        context.coordinator.parent = self
        // Only when it actually differs: assigning the same string would reset
        // the selection under the caret on every redraw.
        if view.string != text { view.string = text }
        view.isEditable = isEnabled
        view.isSelectable = isEnabled
        view.textColor = isEnabled ? .textColor : .disabledControlTextColor
        // A disabled composer must not keep the keyboard: left first responder
        // it would draw a focus ring around a field that ignores every key.
        if !isEnabled, view.window?.firstResponder === view {
            view.window?.makeFirstResponder(nil)
        }
        context.coordinator.updateHeight(view)
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: ComposerTextView

        init(_ parent: ComposerTextView) { self.parent = parent }

        func textDidChange(_ notification: Notification) {
            guard let view = notification.object as? NSTextView else { return }
            parent.text = view.string
            updateHeight(view)
        }

        /// Return sends, Shift-Return inserts a newline.
        ///
        /// Both are handled here rather than left to the key bindings: the
        /// system maps Shift-Return to insertNewlineIgnoringFieldEditor:, but a
        /// remapped keyboard or a text input method can deliver it as an
        /// ordinary insertNewline: with the shift flag still on the event, and
        /// a composer that sent the message on that would eat the newline the
        /// user asked for.
        func textView(_ view: NSTextView, doCommandBy selector: Selector) -> Bool {
            switch selector {
            case #selector(NSResponder.insertNewline(_:)):
                if NSApp.currentEvent?.modifierFlags.contains(.shift) == true {
                    view.insertNewlineIgnoringFieldEditor(nil)
                    return true
                }
                parent.onSubmit()
                return true
            case #selector(NSResponder.insertNewlineIgnoringFieldEditor(_:)):
                view.insertNewlineIgnoringFieldEditor(nil)
                return true
            default:
                return false
            }
        }

        /// Grow to the height the wrapped text needs, up to the ceiling; past
        /// it the scroll view takes over.
        func updateHeight(_ view: NSTextView) {
            guard let layout = view.layoutManager, let container = view.textContainer else { return }
            layout.ensureLayout(for: container)
            let used = layout.usedRect(for: container).height + view.textContainerInset.height * 2
            let wanted = min(max(used.rounded(.up), parent.minHeight), parent.maxHeight)
            guard abs(wanted - parent.height) > 0.5 else { return }
            // Never from inside a SwiftUI update, which is where updateNSView
            // calls this from.
            DispatchQueue.main.async { [parent] in parent.height = wanted }
        }
    }
}

/// An NSTextView that says when it has the keyboard, so the composer can draw a
/// focus ring. NSTextView reports this to nobody otherwise.
final class ComposerNSTextView: NSTextView {
    var onFocusChange: ((Bool) -> Void)?

    override func becomeFirstResponder() -> Bool {
        let accepted = super.becomeFirstResponder()
        if accepted { onFocusChange?(true) }
        return accepted
    }

    override func resignFirstResponder() -> Bool {
        let resigned = super.resignFirstResponder()
        if resigned { onFocusChange?(false) }
        return resigned
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
    /// Set only on the message currently being waited on, and only while the
    /// wait is a model load rather than generation. nil the rest of the time,
    /// which is the plain spinner this client has always shown.
    var loadingLabel: String? = nil
    @State private var showReasoning = false
    /// System Settings → Accessibility → Display → Increase contrast. The
    /// tinted bubble is the one thing here that could go thin under it, so it
    /// is read and answered rather than left to chance.
    @Environment(\.colorSchemeContrast) private var contrast
    private var isUser: Bool { message.role == .user }
    // Thinking is in progress while the assistant has streamed reasoning but no
    // answer text yet.
    private var isThinking: Bool { !isUser && displayText.isEmpty }

    /// A rounded rectangle rather than a tailed speech balloon, and a modest
    /// radius rather than a capsule: the shape macOS uses for a grouped surface,
    /// which is what this is. Alignment and tint are what say who spoke.
    private static let corner: CGFloat = 12
    private static let maxBubbleWidth: CGFloat = 560
    /// How much of the row the other speaker's side keeps, so a bubble never
    /// runs the full width and the alignment stays legible.
    private static let gutter: CGFloat = 56

    // Models often stream leading/trailing newlines (e.g. after the reasoning),
    // which would show as an empty line inside the bubble. Trim for display.
    private var displayText: String {
        message.text.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    private var displayReasoning: String {
        message.reasoning.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        HStack(spacing: 0) {
            if isUser { Spacer(minLength: Self.gutter) }
            VStack(alignment: isUser ? .trailing : .leading, spacing: 5) {
                // Thinking-model reasoning collapses behind a "Thinking…" line with
                // a disclosure toggle. Collapsed by default.
                if !displayReasoning.isEmpty {
                    reasoningDisclosure
                }
                // Only spin when nothing at all has arrived yet; once reasoning is
                // streaming, the "Thinking…" line is the activity indicator.
                if displayText.isEmpty && !isUser && displayReasoning.isEmpty {
                    waiting
                } else if !displayText.isEmpty {
                    Text(displayText)
                        .textSelection(.enabled)
                        // The label colour, not a colour of this view's choosing:
                        // it is the one foreground guaranteed to read against
                        // every accent, in both appearances and under Increase
                        // Contrast.
                        .foregroundStyle(.primary)
                        .multilineTextAlignment(.leading)
                        // Long messages wrap rather than clip: the text keeps
                        // whatever height its wrapped lines need.
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 8)
                        .background(bubble)
                        .frame(maxWidth: Self.maxBubbleWidth,
                               alignment: isUser ? .trailing : .leading)
                }
            }
            if !isUser { Spacer(minLength: Self.gutter) }
        }
    }

    /// Nothing has arrived yet. A bare spinner says only "working"; with a
    /// label it says what the work is, which is the whole difference between a
    /// wait a user can account for and one that reads as a hang.
    @ViewBuilder private var waiting: some View {
        if let loadingLabel {
            HStack(spacing: 7) {
                ProgressView().controlSize(.small)
                Text(loadingLabel)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(bubble)
            .accessibilityElement(children: .combine)
        } else {
            ProgressView().controlSize(.small).padding(.vertical, 6).padding(.horizontal, 4)
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

    /// The bubble's surface. Every colour in it is one the platform supplies:
    /// the person's side is a tint of the accent colour chosen in System
    /// Settings, the model's side a step of the same hierarchy the rest of the
    /// window's fills come from. Nothing is a fixed value, so all of it follows
    /// light and dark, a changed accent, and Increase Contrast — which is
    /// answered explicitly, because a tint is the one thing here thin enough to
    /// disappear under it.
    ///
    /// The model's side is deliberately NOT a named background colour:
    /// controlBackgroundColor and textBackgroundColor are both white in the
    /// light appearance, which is the transcript's own background, and a bubble
    /// the same colour as what it sits on is not a bubble. A hierarchical fill
    /// is a step away from whatever the surface underneath is, in either
    /// appearance.
    @ViewBuilder private var bubble: some View {
        let shape = RoundedRectangle(cornerRadius: Self.corner, style: .continuous)
        let increased = contrast == .increased
        if isUser {
            shape
                .fill(Color.accentColor.opacity(increased ? 0.34 : 0.18))
                .overlay(shape.strokeBorder(Color.accentColor.opacity(increased ? 0.95 : 0.4),
                                            lineWidth: increased ? 1.5 : 1))
        } else {
            shape
                .fill(increased ? AnyShapeStyle(.tertiary) : AnyShapeStyle(.quaternary))
                .overlay(shape.strokeBorder(Color(nsColor: .separatorColor),
                                            lineWidth: increased ? 1.5 : 1))
        }
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
