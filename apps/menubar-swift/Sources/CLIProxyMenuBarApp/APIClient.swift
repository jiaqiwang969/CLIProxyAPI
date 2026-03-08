import Foundation

struct ModelCallCount: Identifiable, Equatable {
    let id: String
    let requests: Int64
}

struct APIKeyUsage: Identifiable, Equatable {
    let id: String
    let label: String
    let totalRequests: Int64
    let totalTokens: Int64
    let modelCalls: [ModelCallCount]
}

struct UsageSummary: Equatable {
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

actor CLIProxyAPIClient {
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
                let modelRequestMap = apiPayload.modelRequestMap

                var selectedModelIDs = modelRequestMap.keys.filter { $0.hasPrefix("antigravity/") }
                if selectedModelIDs.isEmpty {
                    // Older payloads may only expose upstream model IDs (no antigravity/ prefix).
                    selectedModelIDs = Array(modelRequestMap.keys)
                }

                let modelCalls = selectedModelIDs
                    .map { modelID in
                        ModelCallCount(id: modelID, requests: modelRequestMap[modelID] ?? 0)
                    }
                    .filter { $0.requests > 0 }
                    .sorted { lhs, rhs in
                        if lhs.requests == rhs.requests {
                            return lhs.id < rhs.id
                        }
                        return lhs.requests > rhs.requests
                    }

                let totalRequests = apiPayload.totalRequests ?? modelRequestMap.values.reduce(0, +)
                let totalTokens = apiPayload.totalTokens ?? 0
                if totalRequests == 0 && modelCalls.isEmpty {
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
                if lhs.totalRequests == rhs.totalRequests {
                    return lhs.id < rhs.id
                }
                return lhs.totalRequests > rhs.totalRequests
            }

        return UsageSummary(
            totalRequests: usagePayload.totalRequests,
            totalTokens: usagePayload.totalTokens,
            keyUsages: keyUsages
        )
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
        managementKey: String
    ) throws -> URL {
        var mutable = components
        var finalQueryItems = queryItems
        if !managementKey.isEmpty {
            finalQueryItems.append(URLQueryItem(name: "key", value: managementKey))
        }
        mutable.queryItems = finalQueryItems

        guard let url = mutable.url else {
            throw APIClientError.invalidBaseURL
        }
        return url
    }

    private func fetchJSON<T: Decodable>(at url: URL, managementKey: String) async throws -> T {
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = 8

        if !managementKey.isEmpty {
            request.setValue("Bearer \(managementKey)", forHTTPHeaderField: "Authorization")
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

    var modelRequestMap: [String: Int64] {
        var result: [String: Int64] = [:]
        for (modelID, modelPayload) in models ?? [:] {
            result[modelID, default: 0] += modelPayload.totalRequests
        }
        return result
    }
}

private struct ModelPayload: Decodable {
    let totalRequests: Int64

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case requests
    }

    init(from decoder: Decoder) throws {
        if let singleValue = try? decoder.singleValueContainer(), singleValue.decodeNil() {
            self.totalRequests = 0
            return
        }

        let container = try decoder.container(keyedBy: CodingKeys.self)
        if let count = try container.decodeIfPresent(Int64.self, forKey: .totalRequests) {
            self.totalRequests = count
            return
        }
        if let count = try container.decodeIfPresent(Int64.self, forKey: .requests) {
            self.totalRequests = count
            return
        }
        self.totalRequests = 0
    }
}
