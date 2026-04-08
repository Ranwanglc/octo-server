package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// VoiceService handles voice transcription via LiteLLM
type VoiceService struct {
	config *VoiceConfig
	client *http.Client
}

// NewVoiceService creates a new VoiceService
func NewVoiceService(cfg *VoiceConfig) *VoiceService {
	return &VoiceService{
		config: cfg,
		client: &http.Client{},
	}
}

// Transcribe transcribes audio data using the configured engine.
// Returns the transcribed text, the model used, or an error.
func (s *VoiceService) Transcribe(audioData []byte, mimeType string, contextText string, chatContext string) (string, string, error) {
	switch s.config.Engine {
	case "gpt":
		return s.transcribeViaAudioAPI(audioData, mimeType, contextText, chatContext)
	default:
		return s.transcribeViaChatCompletion(audioData, mimeType, contextText, chatContext)
	}
}

// transcribeViaChatCompletion uses the /chat/completions endpoint (Gemini path)
func (s *VoiceService) transcribeViaChatCompletion(audioData []byte, mimeType string, contextText string, chatContext string) (string, string, error) {
	prompt := buildPrompt(contextText, chatContext)

	totalCtx, totalCancel := context.WithTimeout(context.Background(), time.Duration(s.config.TotalTimeout)*time.Second)
	defer totalCancel()

	var lastErr error
	for _, model := range s.config.Models {
		if totalCtx.Err() != nil {
			break
		}

		text, err := s.callLiteLLM(totalCtx, model, audioData, mimeType, prompt)
		if err == nil {
			return text, model, nil
		}

		lastErr = err

		if isNonRetryableError(err) {
			return "", model, err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no models configured")
	}
	return "", "", fmt.Errorf("all models failed: %w", lastErr)
}

// transcribeViaAudioAPI uses the /audio/transcriptions endpoint (GPT path)
func (s *VoiceService) transcribeViaAudioAPI(audioData []byte, mimeType string, contextText string, chatContext string) (string, string, error) {
	prompt := buildPrompt(contextText, chatContext)

	totalCtx, totalCancel := context.WithTimeout(context.Background(), time.Duration(s.config.TotalTimeout)*time.Second)
	defer totalCancel()

	var lastErr error
	for _, model := range s.config.GPTModels {
		if totalCtx.Err() != nil {
			break
		}

		text, err := s.callAudioTranscriptions(totalCtx, model, audioData, mimeType, prompt)
		if err == nil {
			if text == "" {
				return "", model, nil
			}
			if isNoSpeech(text) {
				return "", model, nil
			}
			return text, model, nil
		}

		lastErr = err

		if isNonRetryableError(err) {
			return "", model, err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no GPT models configured")
	}
	return "", "", fmt.Errorf("all GPT models failed: %w", lastErr)
}

// callLiteLLM sends a chat completion request to LiteLLM with audio content.
// NOTE: The input_audio format uses OpenAI-compatible structure. This may need
// adjustment for LiteLLM+Gemini backends - verify the actual format accepted.
func (s *VoiceService) callLiteLLM(totalCtx context.Context, model string, audioData []byte, mimeType string, prompt string) (string, error) {
	b64Audio := base64.StdEncoding.EncodeToString(audioData)

	reqBody := chatCompletionRequest{
		Model: model,
		Messages: []message{
			{
				Role: "user",
				Content: []contentPart{
					{
						Type: "text",
						Text: prompt,
					},
					{
						Type: "input_audio",
						InputAudio: &inputAudio{
							Data:   b64Audio,
							Format: mimeTypeToFormat(mimeType),
						},
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// Use the shorter of per-model timeout and remaining total deadline
	perModelTimeout := time.Duration(s.config.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(totalCtx, perModelTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.config.LiteLLMUrl, "/")+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.LiteLLMKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", &apiError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	var chatResp chatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty response from model")
	}

	result := strings.TrimSpace(chatResp.Choices[0].Message.Content)

	// Handle no-speech sentinel and null content
	if isNoSpeech(result) {
		return "", nil
	}

	return result, nil
}

// callAudioTranscriptions calls /audio/transcriptions (multipart form)
func (s *VoiceService) callAudioTranscriptions(totalCtx context.Context, model string,
	audioData []byte, mimeType string, prompt string) (string, error) {

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	writer.WriteField("model", model)

	if s.config.Language != "" {
		writer.WriteField("language", s.config.Language)
	}

	if prompt != "" {
		writer.WriteField("prompt", prompt)
	}

	ext := mimeTypeToFormat(mimeType)
	part, err := writer.CreateFormFile("file", "audio."+ext)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	part.Write(audioData)
	writer.Close()

	perModelTimeout := time.Duration(s.config.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(totalCtx, perModelTimeout)
	defer cancel()

	url := strings.TrimRight(s.config.LiteLLMUrl, "/") + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.config.LiteLLMKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", &apiError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
}

// mimeTypeToFormat converts MIME type to a short format string for the API
func mimeTypeToFormat(mimeType string) string {
	switch {
	case strings.Contains(mimeType, "wav"):
		return "wav"
	case strings.Contains(mimeType, "mp3"), strings.Contains(mimeType, "mpeg"):
		return "mp3"
	case strings.Contains(mimeType, "ogg"):
		return "ogg"
	case strings.Contains(mimeType, "webm"):
		return "webm"
	case strings.Contains(mimeType, "mp4"), strings.Contains(mimeType, "m4a"):
		return "m4a"
	case strings.Contains(mimeType, "flac"):
		return "flac"
	default:
		return "wav"
	}
}

// apiError represents an HTTP error from the LiteLLM API
type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// isNonRetryableError returns true for 4xx errors other than 429
func isNonRetryableError(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.StatusCode >= 400 && ae.StatusCode < 500 && ae.StatusCode != 429
	}
	return false
}

// Request/response types for OpenAI-compatible chat completion API

type chatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	InputAudio *inputAudio `json:"input_audio,omitempty"`
}

type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type chatCompletionResponse struct {
	Choices []choice `json:"choices"`
}

type choice struct {
	Message responseMessage `json:"message"`
}

type responseMessage struct {
	Content string `json:"content"`
}
