import Foundation
import Testing
@testable import CLIProxyMenuBarApp

private actor MockURLProtocolStorage {
    typealias Handler = @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)

    private var handler: Handler?

    func setHandler(_ handler: Handler?) {
        self.handler = handler
    }

    func currentHandler() -> Handler? {
        handler
    }
}

private final class MockURLProtocol: URLProtocol, @unchecked Sendable {
    static let storage = MockURLProtocolStorage()

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        Task {
            do {
                guard let handler = await Self.storage.currentHandler() else {
                    throw URLError(.badServerResponse)
                }
                let (response, data) = try handler(request)
                client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: data)
                client?.urlProtocolDidFinishLoading(self)
            } catch {
                client?.urlProtocol(self, didFailWithError: error)
            }
        }
    }

    override func stopLoading() {}
}

private final class CapturedRequestStore: @unchecked Sendable {
    private let lock = NSLock()
    private var request: URLRequest?

    func record(_ request: URLRequest) {
        lock.lock()
        self.request = request
        lock.unlock()
    }

    func latest() -> URLRequest? {
        lock.lock()
        defer { lock.unlock() }
        return request
    }
}

private func makeMockSession() -> URLSession {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [MockURLProtocol.self]
    return URLSession(configuration: configuration)
}

private func requestBody(from request: URLRequest) -> Data? {
    if let body = request.httpBody {
        return body
    }
    guard let stream = request.httpBodyStream else {
        return nil
    }

    stream.open()
    defer { stream.close() }

    let bufferSize = 4096
    let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
    defer { buffer.deallocate() }

    var data = Data()
    while stream.hasBytesAvailable {
        let read = stream.read(buffer, maxLength: bufferSize)
        if read <= 0 {
            break
        }
        data.append(buffer, count: read)
    }
    return data.isEmpty ? nil : data
}

@Suite(.serialized)
struct APIClientManagementTests {
    @Test func fetchUsageSummaryPreservesModelRequestAndTokenTotals() async throws {
        await MockURLProtocol.storage.setHandler { request in
            let response = HTTPURLResponse(
                url: try #require(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let body = """
            {
              "usage": {
                "total_requests": 3,
                "total_tokens": 999,
                "apis": {
                  "sk-auggie": {
                    "total_requests": 2,
                    "total_tokens": 341,
                    "models": {
                      "claude-sonnet-4-6": {
                        "total_requests": 1,
                        "total_tokens": 341
                      },
                      "gpt-5-4": {
                        "total_requests": 1,
                        "total_tokens": 0
                      }
                    }
                  },
                  "sk-antigravity": {
                    "total_requests": 1,
                    "total_tokens": 658,
                    "models": {
                      "gemini-3.1-pro-high": {
                        "total_requests": 1,
                        "total_tokens": 658
                      }
                    }
                  }
                }
              }
            }
            """
            return (response, Data(body.utf8))
        }

        let client = CLIProxyAPIClient(session: makeMockSession())
        let summary = try await client.fetchUsageSummary(
            baseURL: "http://localhost:8317",
            managementKey: "menubar-key"
        )

        #expect(summary.totalRequests == 3)
        #expect(summary.totalTokens == 999)
        #expect(summary.keyUsages.count == 2)

        let auggieUsage = try #require(summary.keyUsages.first { $0.id == "sk-auggie" })
        #expect(auggieUsage.modelCalls.count == 2)
        #expect(auggieUsage.modelCalls[0].id == "claude-sonnet-4-6")
        #expect(auggieUsage.modelCalls[0].requests == 1)
        #expect(auggieUsage.modelCalls[0].totalTokens == 341)
        await MockURLProtocol.storage.setHandler(nil)
    }

    @Test func fetchClientAPIKeysDecodesScopedEntries() async throws {
        await MockURLProtocol.storage.setHandler { request in
            let response = HTTPURLResponse(
                url: try #require(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let body = """
            {
              "client-api-keys": [
                {
                  "key": "sk-auggie",
                  "note": "primary",
                  "enabled": false,
                  "scope": {
                    "provider": "auggie",
                    "auth_id": "auggie-main",
                    "models": ["gpt-5-4", "claude-sonnet-4-6"]
                  }
                },
                {
                  "key": "sk-open",
                  "note": ""
                }
              ]
            }
            """
            return (response, Data(body.utf8))
        }

        let client = CLIProxyAPIClient(session: makeMockSession())
        let keys = try await client.fetchClientAPIKeys(
            baseURL: "http://localhost:8317",
            managementKey: "menubar-key"
        )

        #expect(keys.count == 2)
        #expect(keys[0].id == "sk-auggie")
        #expect(keys[0].enabled == false)
        #expect(keys[0].scope?.provider == "auggie")
        #expect(keys[0].scope?.authID == "auggie-main")
        #expect(keys[0].scope?.models == ["gpt-5-4", "claude-sonnet-4-6"])
        #expect(keys[1].enabled == true)
        #expect(keys[1].scope == nil)
        await MockURLProtocol.storage.setHandler(nil)
    }

    @Test func fetchClientAPIKeysDefaultsMissingScopeModelsToEmptyArray() async throws {
        await MockURLProtocol.storage.setHandler { request in
            let response = HTTPURLResponse(
                url: try #require(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let body = """
            {
              "client-api-keys": [
                {
                  "key": "sk-antigravity",
                  "enabled": true,
                  "scope": {
                    "provider": "antigravity",
                    "auth_id": "antigravity-main"
                  }
                }
              ]
            }
            """
            return (response, Data(body.utf8))
        }

        let client = CLIProxyAPIClient(session: makeMockSession())
        let keys = try await client.fetchClientAPIKeys(
            baseURL: "http://localhost:8317",
            managementKey: "menubar-key"
        )

        #expect(keys.count == 1)
        #expect(keys[0].scope?.provider == "antigravity")
        #expect(keys[0].scope?.authID == "antigravity-main")
        #expect(keys[0].scope?.models == [])
        await MockURLProtocol.storage.setHandler(nil)
    }

    @Test func fetchAuthTargetsRequestsModelInventory() async throws {
        await MockURLProtocol.storage.setHandler { request in
            let response = HTTPURLResponse(
                url: try #require(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let body = """
            {
              "files": [
                {
                  "id": "auggie-main",
                  "provider": "auggie",
                  "label": "Auggie Main",
                  "email": "main@auggie.test",
                  "models": [
                    {"id": "gpt-5-4", "display_name": "GPT-5.4"},
                    {"id": "claude-sonnet-4-6"}
                  ]
                },
                {
                  "id": "antigravity-gemini",
                  "provider": "antigravity",
                  "account": "team-gemini",
                  "models": [
                    {"id": "gemini-3.1-pro-high"}
                  ]
                }
              ]
            }
            """
            return (response, Data(body.utf8))
        }

        let client = CLIProxyAPIClient(session: makeMockSession())
        let targets = try await client.fetchAuthTargets(
            baseURL: "http://localhost:8317",
            managementKey: "menubar-key"
        )

        #expect(targets.count == 2)
        #expect(targets[0].id == "auggie-main")
        #expect(targets[0].provider == "auggie")
        #expect(targets[0].displayName == "Auggie Main")
        #expect(targets[0].secondaryLabel == "main@auggie.test")
        #expect(targets[0].models.map(\.id) == ["gpt-5-4", "claude-sonnet-4-6"])
        #expect(targets[1].displayName == "team-gemini")
        await MockURLProtocol.storage.setHandler(nil)
    }

    @Test func saveClientAPIKeysUsesPutWithStructuredPayload() async throws {
        let requestStore = CapturedRequestStore()
        await MockURLProtocol.storage.setHandler { request in
            requestStore.record(request)
            let response = HTTPURLResponse(
                url: try #require(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            return (response, Data("{\"status\":\"ok\"}".utf8))
        }

        let client = CLIProxyAPIClient(session: makeMockSession())
        try await client.saveClientAPIKeys(
            baseURL: "http://localhost:8317",
            managementKey: "menubar-key",
            keys: [
                ManagedClientAPIKey(
                    key: "sk-bound",
                    enabled: true,
                    note: "primary",
                    scope: ClientAPIKeyScope(
                        provider: "auggie",
                        authID: "auggie-main",
                        models: ["gpt-5-4"]
                    )
                )
            ]
        )

        let request = try #require(requestStore.latest())
        let url = try #require(request.url)
        let components = try #require(URLComponents(url: url, resolvingAgainstBaseURL: false))
        #expect(request.httpMethod == "PUT")
        #expect(components.path == "/v0/management/client-api-keys")
        #expect(components.queryItems?.isEmpty ?? true)
        #expect(request.value(forHTTPHeaderField: "Authorization") == "Bearer menubar-key")

        let payload = try JSONDecoder().decode(
            SaveClientAPIKeysPayload.self,
            from: try #require(requestBody(from: request))
        )
        #expect(payload.clientAPIKeys.count == 1)
        #expect(payload.clientAPIKeys[0].key == "sk-bound")
        #expect(payload.clientAPIKeys[0].scope?.authID == "auggie-main")
        await MockURLProtocol.storage.setHandler(nil)
    }
}

private struct SaveClientAPIKeysPayload: Decodable {
    let clientAPIKeys: [ManagedClientAPIKey]

    enum CodingKeys: String, CodingKey {
        case clientAPIKeys = "client-api-keys"
    }
}
