package gemini

import "testing"

func TestRewriteGeminiResponseModelVersion_NonStream(t *testing.T) {
	input := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1},"modelVersion":"gemini-3.1-pro-high","responseId":"resp_1"}`)

	got := rewriteGeminiResponseModelVersion(input, "gemini-3.1-pro-preview")

	want := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1},"modelVersion":"gemini-3.1-pro-preview","responseId":"resp_1"}`
	if string(got) != want {
		t.Fatalf("rewrite result = %s, want %s", string(got), want)
	}
}

func TestRewriteGeminiResponseModelVersion_StreamChunk(t *testing.T) {
	input := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"chunk"}]}}],"modelVersion":"gemini-3.1-pro-high","responseId":"resp_stream"}`)

	got := rewriteGeminiResponseModelVersion(input, "gemini-3.1-pro-preview")

	want := `{"candidates":[{"content":{"role":"model","parts":[{"text":"chunk"}]}}],"modelVersion":"gemini-3.1-pro-preview","responseId":"resp_stream"}`
	if string(got) != want {
		t.Fatalf("rewrite result = %s, want %s", string(got), want)
	}
}

func TestRewriteGeminiResponseModelVersion_IgnoresPayloadWithoutModelVersion(t *testing.T) {
	input := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}]}`)

	got := rewriteGeminiResponseModelVersion(input, "gemini-3.1-pro-preview")

	if string(got) != string(input) {
		t.Fatalf("rewrite changed payload without modelVersion: got %s want %s", string(got), string(input))
	}
}
