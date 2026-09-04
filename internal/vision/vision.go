package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AnalysisResult holds extracted visual intelligence tailored for solutioning & architecture understanding.
type AnalysisResult struct {
	Tags          []string `json:"tags"`
	Objects       []string `json:"objects"`
	ExtractedText []string `json:"extracted_text"`
	Summary       string   `json:"summary"`
}

// Analyzer interface defines vision analysis operations.
type Analyzer interface {
	AnalyzeImage(ctx context.Context, data []byte) (*AnalysisResult, error)
	BatchAnalyzeImages(ctx context.Context, imagesData map[string][]byte) map[string]*AnalysisResult
	IsEnabled() bool
}

// Client represents an Azure AI Vision API client.
type Client struct {
	Endpoint    string
	APIKey      string
	HTTPClient  *http.Client
	Concurrency int
	Timeout     time.Duration
}

// NewClient creates and initializes an Azure AI Vision client with normalized endpoint URL.
func NewClient(rawEndpoint, apiKey string, timeout time.Duration, concurrency int) *Client {
	endpoint := CleanEndpoint(rawEndpoint)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if concurrency <= 0 {
		concurrency = 4
	}

	return &Client{
		Endpoint: endpoint,
		APIKey:   apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
		Concurrency: concurrency,
		Timeout:     timeout,
	}
}

// IsEnabled checks if the vision client is properly configured.
func (c *Client) IsEnabled() bool {
	return c != nil && c.Endpoint != "" && c.APIKey != ""
}

// CleanEndpoint standardizes Azure AI Foundry / Vision service URLs to the root domain.
// Example: https://girirahayu-4423-resource.services.ai.azure.com/api/projects/girirahayu-4423 -> https://girirahayu-4423-resource.services.ai.azure.com
func CleanEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// Raw Azure Vision 4.0 API response models
type azureVisionResponse struct {
	ModelVersion string `json:"modelVersion"`
	Metadata     struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"metadata"`
	TagsResult *struct {
		Values []struct {
			Name       string  `json:"name"`
			Confidence float64 `json:"confidence"`
		} `json:"values"`
	} `json:"tagsResult"`
	ObjectsResult *struct {
		Values []struct {
			Tags []struct {
				Name       string  `json:"name"`
				Confidence float64 `json:"confidence"`
			} `json:"tags"`
		} `json:"values"`
	} `json:"objectsResult"`
	ReadResult *struct {
		Blocks []struct {
			Lines []struct {
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"blocks"`
	} `json:"readResult"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// AnalyzeImage calls Azure Computer Vision Image Analysis 4.0 with read, tags, and objects features.
func (c *Client) AnalyzeImage(ctx context.Context, data []byte) (*AnalysisResult, error) {
	if !c.IsEnabled() {
		return nil, errors.New("azure vision client is not configured")
	}

	// Skip tiny or non-informational images (e.g. spacer pixels, bullet points < 1500 bytes)
	if len(data) < 1500 {
		return nil, errors.New("image is too small for meaningful vision analysis")
	}

	apiURL := fmt.Sprintf("%s/computervision/imageanalysis:analyze?api-version=2024-02-01&features=read,tags,objects", c.Endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed creating vision request: %w", err)
	}

	req.Header.Set("Ocp-Apim-Subscription-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision api request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed reading vision response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp azureVisionResponse
		_ = json.Unmarshal(bodyBytes, &errResp)
		if errResp.Error != nil {
			return nil, fmt.Errorf("azure vision error (%d): %s - %s", resp.StatusCode, errResp.Error.Code, errResp.Error.Message)
		}
		return nil, fmt.Errorf("azure vision returned unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var visionResp azureVisionResponse
	if err := json.Unmarshal(bodyBytes, &visionResp); err != nil {
		return nil, fmt.Errorf("failed to decode vision json response: %w", err)
	}

	// 1. Extract Tags (Confidence >= 0.6)
	var tags []string
	if visionResp.TagsResult != nil {
		for _, tag := range visionResp.TagsResult.Values {
			if tag.Confidence >= 0.6 {
				cleanTag := strings.TrimSpace(tag.Name)
				if cleanTag != "" && !containsFold(tags, cleanTag) {
					tags = append(tags, cleanTag)
				}
			}
		}
	}

	// 2. Extract Objects
	var objects []string
	if visionResp.ObjectsResult != nil {
		for _, obj := range visionResp.ObjectsResult.Values {
			for _, objTag := range obj.Tags {
				if objTag.Confidence >= 0.5 {
					cleanObj := strings.TrimSpace(objTag.Name)
					if cleanObj != "" && !containsFold(objects, cleanObj) {
						objects = append(objects, cleanObj)
					}
				}
			}
		}
	}

	// 3. Extract Read (OCR) Text from lines
	var textLines []string
	if visionResp.ReadResult != nil {
		for _, block := range visionResp.ReadResult.Blocks {
			for _, line := range block.Lines {
				cleanLine := strings.TrimSpace(line.Text)
				if cleanLine != "" {
					textLines = append(textLines, cleanLine)
				}
			}
		}
	}

	// 4. Generate solutioning summary
	summary := buildSolutioningSummary(tags, objects, textLines)

	return &AnalysisResult{
		Tags:          tags,
		Objects:       objects,
		ExtractedText: textLines,
		Summary:       summary,
	}, nil
}

func buildSolutioningSummary(tags, objects, lines []string) string {
	parts := make([]string, 0, 3)
	if len(tags) > 0 {
		topTags := tags
		if len(topTags) > 4 {
			topTags = topTags[:4]
		}
		parts = append(parts, fmt.Sprintf("Concepts: %s", strings.Join(topTags, ", ")))
	}
	if len(objects) > 0 {
		topObjs := objects
		if len(topObjs) > 3 {
			topObjs = topObjs[:3]
		}
		parts = append(parts, fmt.Sprintf("Components: %s", strings.Join(topObjs, ", ")))
	}
	if len(lines) > 0 {
		parts = append(parts, fmt.Sprintf("%d inscribed text lines detected", len(lines)))
	}
	if len(parts) == 0 {
		return "Visual asset detected"
	}
	return strings.Join(parts, " • ")
}

func containsFold(slice []string, val string) bool {
	for _, item := range slice {
		if strings.EqualFold(item, val) {
			return true
		}
	}
	return false
}

// BatchAnalyzeImages analyzes multiple image payloads in parallel using a bounded worker pool.
func (c *Client) BatchAnalyzeImages(ctx context.Context, imagesData map[string][]byte) map[string]*AnalysisResult {
	results := make(map[string]*AnalysisResult)
	if !c.IsEnabled() || len(imagesData) == 0 {
		return results
	}

	var mu sync.Mutex
	sem := make(chan struct{}, c.Concurrency)
	var wg sync.WaitGroup

	for id, data := range imagesData {
		if len(data) < 1500 {
			continue
		}

		wg.Add(1)
		go func(imgID string, imgBytes []byte) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Use isolated per-image timeout so one slow call doesn't stall the batch
			imgCtx, cancel := context.WithTimeout(ctx, c.Timeout)
			defer cancel()

			res, err := c.AnalyzeImage(imgCtx, imgBytes)
			if err != nil {
				log.Printf("[Vision AI] Skipping image %s: %v", imgID, err)
				return
			}

			mu.Lock()
			results[imgID] = res
			mu.Unlock()
		}(id, data)
	}

	wg.Wait()
	return results
}

// FormatSolutioningMarkdown formats vision analysis insights into Markdown.
func FormatSolutioningMarkdown(res *AnalysisResult) string {
	if res == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n> 🔍 **AI Vision Analysis (Solutioning & Architecture Insights)**:\n")

	if len(res.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("> - **Domain & Concepts**: %s\n", strings.Join(res.Tags, ", ")))
	}
	if len(res.Objects) > 0 {
		sb.WriteString(fmt.Sprintf("> - **Detected Components / Objects**: %s\n", strings.Join(res.Objects, ", ")))
	}
	if len(res.ExtractedText) > 0 {
		sb.WriteString("> - **Diagram Inscriptions & OCR**:\n")
		for _, line := range res.ExtractedText {
			sb.WriteString(fmt.Sprintf(">   `%s`  \n", escapeMarkdownInline(line)))
		}
	}

	return sb.String()
}

// FormatSolutioningText formats vision analysis insights into plain text.
func FormatSolutioningText(res *AnalysisResult) string {
	if res == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n--- AI Vision Analysis (Solutioning Context) ---\n")
	if len(res.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Concepts   : %s\n", strings.Join(res.Tags, ", ")))
	}
	if len(res.Objects) > 0 {
		sb.WriteString(fmt.Sprintf("Components : %s\n", strings.Join(res.Objects, ", ")))
	}
	if len(res.ExtractedText) > 0 {
		sb.WriteString("Inscribed Diagram Text:\n")
		for _, line := range res.ExtractedText {
			sb.WriteString(fmt.Sprintf("  • %s\n", line))
		}
	}
	sb.WriteString("------------------------------------------------\n")
	return sb.String()
}

func escapeMarkdownInline(s string) string {
	s = strings.ReplaceAll(s, "`", `\'`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
