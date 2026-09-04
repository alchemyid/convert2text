package vision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCleanEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://girirahayu-4423-resource.services.ai.azure.com/api/projects/girirahayu-4423",
			expected: "https://girirahayu-4423-resource.services.ai.azure.com",
		},
		{
			input:    "https://girirahayu-4423-resource.services.ai.azure.com/",
			expected: "https://girirahayu-4423-resource.services.ai.azure.com",
		},
		{
			input:    "girirahayu-4423-resource.services.ai.azure.com",
			expected: "https://girirahayu-4423-resource.services.ai.azure.com",
		},
	}

	for _, tc := range tests {
		got := CleanEndpoint(tc.input)
		if got != tc.expected {
			t.Errorf("CleanEndpoint(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestAnalyzeImageSuccess(t *testing.T) {
	mockResponse := `{
		"modelVersion": "2023-10-01",
		"metadata": {"width": 800, "height": 600},
		"tagsResult": {
			"values": [
				{"name": "software architecture", "confidence": 0.95},
				{"name": "diagram", "confidence": 0.88},
				{"name": "low confidence tag", "confidence": 0.20}
			]
		},
		"objectsResult": {
			"values": [
				{"tags": [{"name": "server", "confidence": 0.85}]}
			]
		},
		"readResult": {
			"blocks": [
				{
					"lines": [
						{"text": "Client App -> API Gateway"},
						{"text": "PostgreSQL DB"}
					]
				}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Ocp-Apim-Subscription-Key") != "test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", 2*time.Second, 2)
	fakeImage := make([]byte, 2000)

	res, err := client.AnalyzeImage(context.Background(), fakeImage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Tags) != 2 {
		t.Errorf("expected 2 high-confidence tags, got %d", len(res.Tags))
	}
	if len(res.Objects) != 1 || res.Objects[0] != "server" {
		t.Errorf("expected object 'server', got %v", res.Objects)
	}
	if len(res.ExtractedText) != 2 {
		t.Errorf("expected 2 extracted text lines, got %d", len(res.ExtractedText))
	}

	// Verify Markdown formatting
	md := FormatSolutioningMarkdown(res)
	if !strings.Contains(md, "software architecture") || !strings.Contains(md, "Client App -> API Gateway") {
		t.Errorf("markdown formatting missing expected content: %s", md)
	}

	// Verify Plain text formatting
	txt := FormatSolutioningText(res)
	if !strings.Contains(txt, "server") || !strings.Contains(txt, "PostgreSQL DB") {
		t.Errorf("plain text formatting missing expected content: %s", txt)
	}
}
