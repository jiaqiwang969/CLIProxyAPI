package registry

var geminiPublicModelCatalog = []*ModelInfo{
	geminiModel("gemini-2.5-flash", "001", "Gemini 2.5 Flash", "Stable version of Gemini 2.5 Flash, our mid-size multimodal model that supports up to 1 million tokens, released in June of 2025.", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash")),
	geminiModel("gemini-2.5-pro", "2.5", "Gemini 2.5 Pro", "Stable release (June 17th, 2025) of Gemini 2.5 Pro", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-pro")),
	geminiModel("gemini-2.0-flash", "2.0", "Gemini 2.0 Flash", "Gemini 2.0 Flash", 1048576, 8192, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(40), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.0-flash")),
	geminiModel("gemini-2.0-flash-001", "2.0", "Gemini 2.0 Flash 001", "Stable version of Gemini 2.0 Flash, our fast and versatile multimodal model for scaling across diverse tasks, released in January of 2025.", 1048576, 8192, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(40), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.0-flash-001")),
	geminiModel("gemini-2.0-flash-exp-image-generation", "2.0", "Gemini 2.0 Flash (Image Generation) Experimental", "Gemini 2.0 Flash (Image Generation) Experimental", 1048576, 8192, []string{"generateContent", "countTokens", "bidiGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(40), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.0-flash-exp-image-generation")),
	geminiModel("gemini-2.0-flash-lite-001", "2.0", "Gemini 2.0 Flash-Lite 001", "Stable version of Gemini 2.0 Flash-Lite", 1048576, 8192, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(40), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.0-flash-lite-001")),
	geminiModel("gemini-2.0-flash-lite", "2.0", "Gemini 2.0 Flash-Lite", "Gemini 2.0 Flash-Lite", 1048576, 8192, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(40), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.0-flash-lite")),
	geminiModel("gemini-2.5-flash-preview-tts", "gemini-2.5-flash-exp-tts-2025-05-19", "Gemini 2.5 Flash Preview TTS", "Gemini 2.5 Flash Preview TTS", 8192, 16384, []string{"countTokens", "generateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.5-flash-preview-tts")),
	geminiModel("gemini-2.5-pro-preview-tts", "gemini-2.5-pro-preview-tts-2025-05-19", "Gemini 2.5 Pro Preview TTS", "Gemini 2.5 Pro Preview TTS", 8192, 16384, []string{"countTokens", "generateContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), nil, geminiThinkingSupport("gemini-2.5-pro-preview-tts")),
	geminiModel("gemma-3-1b-it", "001", "Gemma 3 1B", "", 32768, 8192, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3-1b-it")),
	geminiModel("gemma-3-4b-it", "001", "Gemma 3 4B", "", 32768, 8192, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3-4b-it")),
	geminiModel("gemma-3-12b-it", "001", "Gemma 3 12B", "", 32768, 8192, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3-12b-it")),
	geminiModel("gemma-3-27b-it", "001", "Gemma 3 27B", "", 131072, 8192, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3-27b-it")),
	geminiModel("gemma-3n-e4b-it", "001", "Gemma 3n E4B", "", 8192, 2048, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3n-e4b-it")),
	geminiModel("gemma-3n-e2b-it", "001", "Gemma 3n E2B", "", 8192, 2048, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), nil, nil, geminiThinkingSupport("gemma-3n-e2b-it")),
	geminiModel("gemini-flash-latest", "Gemini Flash Latest", "Gemini Flash Latest", "Latest release of Gemini Flash", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-flash-latest")),
	geminiModel("gemini-flash-lite-latest", "Gemini Flash-Lite Latest", "Gemini Flash-Lite Latest", "Latest release of Gemini Flash-Lite", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-flash-lite-latest")),
	geminiModel("gemini-pro-latest", "Gemini Pro Latest", "Gemini Pro Latest", "Latest release of Gemini Pro", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-pro-latest")),
	geminiModel("gemini-2.5-flash-lite", "001", "Gemini 2.5 Flash-Lite", "Stable version of Gemini 2.5 Flash-Lite, released in July of 2025", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash-lite")),
	geminiModel("gemini-2.5-flash-image", "2.0", "Nano Banana", "Gemini 2.5 Flash Preview Image", 32768, 32768, []string{"generateContent", "countTokens", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(1), nil, geminiThinkingSupport("gemini-2.5-flash-image")),
	geminiModel("gemini-2.5-flash-lite-preview-09-2025", "2.5-preview-09-25", "Gemini 2.5 Flash-Lite Preview Sep 2025", "Preview release (Septempber 25th, 2025) of Gemini 2.5 Flash-Lite", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash-lite-preview-09-2025")),
	geminiModel("gemini-3-pro-preview", "3-pro-preview-11-2025", "Gemini 3 Pro Preview", "Gemini 3 Pro Preview", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-3-pro-preview")),
	geminiModel("gemini-3-flash-preview", "3-flash-preview-12-2025", "Gemini 3 Flash Preview", "Gemini 3 Flash Preview", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-3-flash-preview")),
	geminiModel("gemini-3.1-pro-preview", "3.1-pro-preview-01-2026", "Gemini 3.1 Pro Preview", "Gemini 3.1 Pro Preview", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-3.1-pro-preview")),
	geminiModel("gemini-3.1-pro-preview-customtools", "3.1-pro-preview-01-2026", "Gemini 3.1 Pro Preview Custom Tools", "Gemini 3.1 Pro Preview optimized for custom tool usage", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-3.1-pro-preview-customtools")),
	geminiModel("gemini-3.1-flash-lite-preview", "3.1-flash-lite-preview-03-2026", "Gemini 3.1 Flash Lite Preview", "Gemini 3.1 Flash Lite Preview", 1048576, 65536, []string{"generateContent", "countTokens", "createCachedContent", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-3.1-flash-lite-preview")),
	geminiModel("gemini-3-pro-image-preview", "3.0", "Nano Banana Pro", "Gemini 3 Pro Image Preview", 131072, 32768, []string{"generateContent", "countTokens", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(1), boolPtr(true), geminiThinkingSupport("gemini-3-pro-image-preview")),
	geminiModel("nano-banana-pro-preview", "3.0", "Nano Banana Pro", "Gemini 3 Pro Image Preview", 131072, 32768, []string{"generateContent", "countTokens", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(1), boolPtr(true), geminiThinkingSupport("nano-banana-pro-preview")),
	geminiModel("gemini-3.1-flash-image-preview", "3.0", "Nano Banana 2", "Gemini 3.1 Flash Image Preview.", 65536, 65536, []string{"generateContent", "countTokens", "batchGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(1), boolPtr(true), geminiThinkingSupport("gemini-3.1-flash-image-preview")),
	geminiModel("gemini-robotics-er-1.5-preview", "1.5-preview", "Gemini Robotics-ER 1.5 Preview", "Gemini Robotics-ER 1.5 Preview", 1048576, 65536, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-robotics-er-1.5-preview")),
	geminiModel("gemini-2.5-computer-use-preview-10-2025", "Gemini 2.5 Computer Use Preview 10-2025", "Gemini 2.5 Computer Use Preview 10-2025", "Gemini 2.5 Computer Use Preview 10-2025", 131072, 65536, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-computer-use-preview-10-2025")),
	geminiModel("deep-research-pro-preview-12-2025", "deepthink-exp-05-20", "Deep Research Pro Preview (Dec-12-2025)", "Preview release (December 12th, 2025) of Deep Research Pro", 131072, 65536, []string{"generateContent", "countTokens"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("deep-research-pro-preview-12-2025")),
	geminiModel("gemini-embedding-001", "001", "Gemini Embedding 001", "Obtain a distributed representation of a text.", 2048, 1, []string{"embedContent", "countTextTokens", "countTokens", "asyncBatchEmbedContent"}, nil, nil, nil, nil, nil, geminiThinkingSupport("gemini-embedding-001")),
	geminiModel("aqa", "001", "Model that performs Attributed Question Answering.", "Model trained to return answers to questions that are grounded in provided sources, along with estimating answerable probability.", 7168, 1024, []string{"generateAnswer"}, float64Ptr(0.2), float64Ptr(1), intPtr(40), nil, nil, geminiThinkingSupport("aqa")),
	geminiModel("imagen-4.0-generate-001", "001", "Imagen 4", "Vertex served Imagen 4.0 model", 480, 8192, []string{"predict"}, nil, nil, nil, nil, nil, geminiThinkingSupport("imagen-4.0-generate-001")),
	geminiModel("imagen-4.0-ultra-generate-001", "001", "Imagen 4 Ultra", "Vertex served Imagen 4.0 ultra model", 480, 8192, []string{"predict"}, nil, nil, nil, nil, nil, geminiThinkingSupport("imagen-4.0-ultra-generate-001")),
	geminiModel("imagen-4.0-fast-generate-001", "001", "Imagen 4 Fast", "Vertex served Imagen 4.0 Fast model", 480, 8192, []string{"predict"}, nil, nil, nil, nil, nil, geminiThinkingSupport("imagen-4.0-fast-generate-001")),
	geminiModel("veo-2.0-generate-001", "2.0", "Veo 2", "Vertex served Veo 2 model. Access to this model requires billing to be enabled on the associated Google Cloud Platform account. Please visit https://console.cloud.google.com/billing to enable it.", 480, 8192, []string{"predictLongRunning"}, nil, nil, nil, nil, nil, geminiThinkingSupport("veo-2.0-generate-001")),
	geminiModel("veo-3.0-generate-001", "3.0", "Veo 3", "Veo 3", 480, 8192, []string{"predictLongRunning"}, nil, nil, nil, nil, nil, geminiThinkingSupport("veo-3.0-generate-001")),
	geminiModel("veo-3.0-fast-generate-001", "3.0", "Veo 3 fast", "Veo 3 fast", 480, 8192, []string{"predictLongRunning"}, nil, nil, nil, nil, nil, geminiThinkingSupport("veo-3.0-fast-generate-001")),
	geminiModel("veo-3.1-generate-preview", "3.1", "Veo 3.1", "Veo 3.1", 480, 8192, []string{"predictLongRunning"}, nil, nil, nil, nil, nil, geminiThinkingSupport("veo-3.1-generate-preview")),
	geminiModel("veo-3.1-fast-generate-preview", "3.1", "Veo 3.1 fast", "Veo 3.1 fast", 480, 8192, []string{"predictLongRunning"}, nil, nil, nil, nil, nil, geminiThinkingSupport("veo-3.1-fast-generate-preview")),
	geminiModel("gemini-2.5-flash-native-audio-latest", "Gemini 2.5 Flash Native Audio Latest", "Gemini 2.5 Flash Native Audio Latest", "Latest release of Gemini 2.5 Flash Native Audio", 131072, 8192, []string{"countTokens", "bidiGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash-native-audio-latest")),
	geminiModel("gemini-2.5-flash-native-audio-preview-09-2025", "gemini-2.5-flash-preview-native-audio-dialog-2025-05-19", "Gemini 2.5 Flash Native Audio Preview 09-2025", "Gemini 2.5 Flash Native Audio Preview 09-2025", 131072, 8192, []string{"countTokens", "bidiGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash-native-audio-preview-09-2025")),
	geminiModel("gemini-2.5-flash-native-audio-preview-12-2025", "12-2025", "Gemini 2.5 Flash Native Audio Preview 12-2025", "Gemini 2.5 Flash Native Audio Preview 12-2025", 131072, 8192, []string{"countTokens", "bidiGenerateContent"}, float64Ptr(1), float64Ptr(0.95), intPtr(64), float64Ptr(2), boolPtr(true), geminiThinkingSupport("gemini-2.5-flash-native-audio-preview-12-2025")),
}

func geminiModel(id, version, displayName, description string, inputTokenLimit, outputTokenLimit int, supportedGenerationMethods []string, temperature, topP *float64, topK *int, maxTemperature *float64, publicThinking *bool, thinking *ThinkingSupport) *ModelInfo {
	return &ModelInfo{
		ID:                         id,
		Object:                     "model",
		OwnedBy:                    "google",
		Type:                       "gemini",
		Name:                       "models/" + id,
		Version:                    version,
		DisplayName:                displayName,
		Description:                description,
		InputTokenLimit:            inputTokenLimit,
		OutputTokenLimit:           outputTokenLimit,
		SupportedGenerationMethods: append([]string(nil), supportedGenerationMethods...),
		GeminiTemperature:          temperature,
		GeminiTopP:                 topP,
		GeminiTopK:                 topK,
		GeminiMaxTemperature:       maxTemperature,
		GeminiPublicThinking:       publicThinking,
		Thinking:                   thinking,
	}
}

func geminiThinkingSupport(id string) *ThinkingSupport {
	switch id {
	case "gemini-2.5-pro", "gemini-pro-latest", "gemini-2.5-pro-preview-tts", "gemini-robotics-er-1.5-preview", "gemini-2.5-computer-use-preview-10-2025", "deep-research-pro-preview-12-2025":
		return &ThinkingSupport{Min: 128, Max: 32768, ZeroAllowed: false, DynamicAllowed: true}
	case "gemini-2.5-flash", "gemini-flash-latest", "gemini-2.5-flash-preview-tts", "gemini-2.5-flash-native-audio-latest", "gemini-2.5-flash-native-audio-preview-09-2025", "gemini-2.5-flash-native-audio-preview-12-2025":
		return &ThinkingSupport{Min: 0, Max: 24576, ZeroAllowed: true, DynamicAllowed: true}
	case "gemini-2.5-flash-lite", "gemini-flash-lite-latest", "gemini-2.5-flash-lite-preview-09-2025", "gemini-3.1-flash-lite-preview":
		return &ThinkingSupport{Min: 0, Max: 24576, ZeroAllowed: true, DynamicAllowed: true}
	case "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3.1-pro-preview-customtools":
		return &ThinkingSupport{Min: 128, Max: 32768, ZeroAllowed: false, DynamicAllowed: true, Levels: []string{"low", "high"}}
	case "gemini-3-flash-preview":
		return &ThinkingSupport{Min: 128, Max: 32768, ZeroAllowed: false, DynamicAllowed: true, Levels: []string{"minimal", "low", "medium", "high"}}
	case "gemini-3-pro-image-preview", "nano-banana-pro-preview", "gemini-3.1-flash-image-preview":
		return &ThinkingSupport{Min: 128, Max: 32768, ZeroAllowed: false, DynamicAllowed: true, Levels: []string{"low", "high"}}
	default:
		return nil
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

func intPtr(value int) *int {
	return &value
}
