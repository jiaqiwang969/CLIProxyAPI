import Foundation

struct ModelCallCount: Identifiable, Equatable, Sendable {
    let id: String
    let requests: Int64
    let totalTokens: Int64
}

struct APIKeyUsage: Identifiable, Equatable, Sendable {
    let id: String
    let label: String
    let totalRequests: Int64
    let totalTokens: Int64
    let modelCalls: [ModelCallCount]
}

struct UsageSummary: Equatable, Sendable {
    let totalRequests: Int64
    let totalTokens: Int64
    let keyUsages: [APIKeyUsage]

    var displayRequests: Int64 {
        if !keyUsages.isEmpty {
            return keyUsages.reduce(0) { $0 + $1.totalRequests }
        }
        return totalRequests
    }

    var displayTokens: Int64 {
        if !keyUsages.isEmpty {
            return keyUsages.reduce(0) { $0 + $1.totalTokens }
        }
        return totalTokens
    }
}

actor CLIProxyAPIClient: CLIProxyAPIManaging {
    private let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    func fetchUsageSummary(baseURL: String, managementKey: String) async throws -> UsageSummary {
        let trimmedBaseURL = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedKey = managementKey.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !trimmedBaseURL.isEmpty else {
            throw APIClientError.invalidBaseURL
        }

        let usageURL = try makeURL(
            baseURL: trimmedBaseURL,
            path: "/v0/management/usage",
            managementKey: trimmedKey
        )

        let usageResponse: UsageResponse = try await fetchJSON(
            at: usageURL,
            managementKey: trimmedKey
        )

        guard let usagePayload = usageResponse.usage else {
            throw APIClientError.serverMessage(usageResponse.error ?? "Missing usage payload")
        }

        let keyUsages = (usagePayload.apis ?? [:])
            .compactMap { (entry: Dictionary<String, APIPayload>.Element) -> APIKeyUsage? in
                let apiID = entry.key
                let apiPayload = entry.value
                let modelCalls = (apiPayload.models ?? [:])
                    .map { modelID, modelPayload in
                        ModelCallCount(
                            id: modelID,
                            requests: modelPayload.totalRequests,
                            totalTokens: modelPayload.totalTokens
                        )
                    }
                    .filter { $0.requests > 0 || $0.totalTokens > 0 }
                    .sorted { lhs, rhs in
                        if lhs.totalTokens == rhs.totalTokens {
                            if lhs.requests == rhs.requests {
                                return lhs.id < rhs.id
                            }
                            return lhs.requests > rhs.requests
                        }
                        return lhs.totalTokens > rhs.totalTokens
                    }

                let totalRequests = apiPayload.totalRequests ?? modelCalls.reduce(0) { $0 + $1.requests }
                let totalTokens = apiPayload.totalTokens ?? modelCalls.reduce(0) { $0 + $1.totalTokens }
                if totalRequests == 0 && totalTokens == 0 && modelCalls.isEmpty {
                    return nil
                }

                return APIKeyUsage(
                    id: apiID,
                    label: Self.maskedIdentifier(apiID),
                    totalRequests: totalRequests,
                    totalTokens: totalTokens,
                    modelCalls: modelCalls
                )
            }
            .sorted { (lhs: APIKeyUsage, rhs: APIKeyUsage) in
                if lhs.totalTokens == rhs.totalTokens {
                    if lhs.totalRequests == rhs.totalRequests {
                            return lhs.id < rhs.id
                        }
                    return lhs.totalRequests > rhs.totalRequests
                }
                return lhs.totalTokens > rhs.totalTokens
            }

        return UsageSummary(
            totalRequests: usagePayload.totalRequests,
            totalTokens: usagePayload.totalTokens,
            keyUsages: keyUsages
        )
    }

    func fetchClientAPIKeys(baseURL: String, managementKey: String) async throws -> [ManagedClientAPIKey] {
        let trimmedBaseURL = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedKey = managementKey.trimmingCharacters(in: .whitespacesAndNewlines)
        let url = try makeURL(
            baseURL: trimmedBaseURL,
            path: "/v0/management/client-api-keys",
            managementKey: trimmedKey
        )
        let response: ClientAPIKeysResponse = try await fetchJSON(at: url, managementKey: trimmedKey)
        return response.clientAPIKeys
    }

    func saveClientAPIKeys(
        baseURL: String,
        managementKey: String,
        keys: [ManagedClientAPIKey]
    ) async throws {
        let trimmedBaseURL = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedKey = managementKey.trimmingCharacters(in: .whitespacesAndNewlines)
        let url = try makeURL(
            baseURL: trimmedBaseURL,
            path: "/v0/management/client-api-keys",
            managementKey: trimmedKey
        )
        let payload = ClientAPIKeysResponse(clientAPIKeys: keys)
        let _: StatusResponse = try await sendJSON(
            method: "PUT",
            payload: payload,
            to: url,
            managementKey: trimmedKey
        )
    }

    func fetchAuthTargets(baseURL: String, managementKey: String) async throws -> [AuthTarget] {
        let trimmedBaseURL = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedKey = managementKey.trimmingCharacters(in: .whitespacesAndNewlines)
        let url = try makeURL(
            baseURL: trimmedBaseURL,
            path: "/v0/management/auth-files",
            queryItems: [URLQueryItem(name: "include_models", value: "true")],
            managementKey: trimmedKey
        )
        let response: AuthFilesResponse = try await fetchJSON(at: url, managementKey: trimmedKey)
        return response.files
    }

    private static func maskedIdentifier(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return "(unknown)"
        }

        if trimmed.hasPrefix("sk-") || trimmed.hasPrefix("sk_") {
            if trimmed.count <= 14 {
                return trimmed
            }
            return "\(trimmed.prefix(7))...\(trimmed.suffix(4))"
        }

        return trimmed
    }

    private func makeURL(
        baseURL: String,
        path: String,
        queryItems: [URLQueryItem] = [],
        managementKey: String
    ) throws -> URL {
        guard var components = URLComponents(string: baseURL) else {
            throw APIClientError.invalidBaseURL
        }
        components.path = path
        return try finalizeURL(components: components, queryItems: queryItems, managementKey: managementKey)
    }

    private func finalizeURL(
        components: URLComponents,
        queryItems: [URLQueryItem],
        managementKey _: String
    ) throws -> URL {
        var mutable = components
        mutable.queryItems = queryItems.isEmpty ? nil : queryItems

        guard let url = mutable.url else {
            throw APIClientError.invalidBaseURL
        }
        return url
    }

    private func fetchJSON<T: Decodable>(at url: URL, managementKey: String) async throws -> T {
        try await sendJSON(method: "GET", payload: Optional<String>.none, to: url, managementKey: managementKey)
    }

    private func sendJSON<T: Decodable, Body: Encodable>(
        method: String,
        payload: Body?,
        to url: URL,
        managementKey: String
    ) async throws -> T {
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 8

        if !managementKey.isEmpty {
            request.setValue("Bearer \(managementKey)", forHTTPHeaderField: "Authorization")
        }
        if let payload {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(payload)
        }

        let (data, response) = try await session.data(for: request)

        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIClientError.invalidResponse
        }

        guard (200 ..< 300).contains(httpResponse.statusCode) else {
            let body = String(data: data, encoding: .utf8) ?? ""
            throw APIClientError.httpError(statusCode: httpResponse.statusCode, body: body)
        }

        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw APIClientError.decodeError(error)
        }
    }
}

enum APIClientError: Error, LocalizedError {
    case invalidBaseURL
    case invalidResponse
    case httpError(statusCode: Int, body: String)
    case serverMessage(String)
    case decodeError(Error)

    var errorDescription: String? {
        switch self {
        case .invalidBaseURL:
            return "Invalid server URL"
        case .invalidResponse:
            return "Invalid server response"
        case let .httpError(statusCode, body):
            let preview = body.prefix(120)
            if preview.isEmpty {
                return "HTTP \(statusCode)"
            }
            return "HTTP \(statusCode): \(preview)"
        case let .serverMessage(message):
            return message
        case let .decodeError(error):
            return "Failed to decode response: \(error.localizedDescription)"
        }
    }
}

private struct UsageResponse: Decodable {
    let usage: UsagePayload?
    let error: String?
}

private struct UsagePayload: Decodable {
    let totalRequests: Int64
    let totalTokens: Int64
    let apis: [String: APIPayload]?

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case totalTokens = "total_tokens"
        case apis
    }
}

private struct APIPayload: Decodable {
    let totalRequests: Int64?
    let totalTokens: Int64?
    let models: [String: ModelPayload]?

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case totalTokens = "total_tokens"
        case models
    }
}

private struct ModelPayload: Decodable {
    let totalRequests: Int64
    let totalTokens: Int64

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case requests
        case totalTokens = "total_tokens"
        case tokens
    }

    init(from decoder: Decoder) throws {
        if let singleValue = try? decoder.singleValueContainer(), singleValue.decodeNil() {
            self.totalRequests = 0
            self.totalTokens = 0
            return
        }

        if let singleValue = try? decoder.singleValueContainer(),
           let requestCount = try? singleValue.decode(Int64.self)
        {
            self.totalRequests = requestCount
            self.totalTokens = 0
            return
        }

        let container = try decoder.container(keyedBy: CodingKeys.self)
        if let count = try container.decodeIfPresent(Int64.self, forKey: .totalRequests) {
            self.totalRequests = count
        } else if let count = try container.decodeIfPresent(Int64.self, forKey: .requests) {
            self.totalRequests = count
        } else {
            self.totalRequests = 0
        }

        if let count = try container.decodeIfPresent(Int64.self, forKey: .totalTokens) {
            self.totalTokens = count
        } else if let count = try container.decodeIfPresent(Int64.self, forKey: .tokens) {
            self.totalTokens = count
        } else {
            self.totalTokens = 0
        }
    }
}

private struct ClientAPIKeysResponse: Codable {
    let clientAPIKeys: [ManagedClientAPIKey]

    enum CodingKeys: String, CodingKey {
        case clientAPIKeys = "client-api-keys"
    }
}

private struct AuthFilesResponse: Decodable {
    let files: [AuthTarget]
}

private struct StatusResponse: Decodable {
    let status: String
}
