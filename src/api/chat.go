package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxChatMessages     = 100
	maxChatMessageRunes = 64 * 1024
	maxChatTotalRunes   = 256 * 1024
	maxRuntimeResponse  = 8 * 1024 * 1024
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Messages []chatMessage `json:"messages"`
}

type runtimeChatCompletion struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          json.RawMessage `json:"content"`
			ReasoningContent json.RawMessage `json:"reasoning_content"`
			Reasoning        json.RawMessage `json:"reasoning"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		TokensPerSecond  float64 `json:"tokens_per_second"`
	} `json:"usage"`
	Timings struct {
		PredictedPerSecond float64 `json:"predicted_per_second"`
		TokensPerSecond    float64 `json:"tokens_per_second"`
	} `json:"timings"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

var (
	thinkBlockPattern   = regexp.MustCompile(`(?is)<think\b[^>]*>(.*?)</think\s*>`)
	thinkOpenPattern    = regexp.MustCompile(`(?is)<think\b[^>]*>`)
	thinkClosePattern   = regexp.MustCompile(`(?is)</think\s*>`)
	thinkingTextPattern = regexp.MustCompile(
		`(?is)^\s*(?:#{1,6}\s*)?(?:\*\*)?thinking process(?:\*\*)?\s*:\s*(.*?)\n\s*(?:#{1,6}\s*)?(?:\*\*)?final answer(?:\*\*)?\s*:\s*(.*)$`,
	)
)

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var request chatCompletionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateChatMessages(request.Messages); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	status := s.llama.Status()
	if !status.Running {
		writeError(w, http.StatusConflict, errors.New("模型服務尚未啟動，請先到「執行狀態」載入模型"))
		return
	}
	endpoint, err := runtimeChatURL(status.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	body, err := json.Marshal(map[string]any{
		"messages":   request.Messages,
		"max_tokens": 2048,
		"stream":     false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("無法建立對話請求"))
		return
	}

	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("無法建立 Runtime 請求"))
		return
	}
	upstreamRequest.Header.Set("Accept", "application/json")
	upstreamRequest.Header.Set("Content-Type", "application/json")
	if runtimeKey := strings.TrimSpace(r.Header.Get("X-Tanpopo-Key")); runtimeKey != "" {
		if len(runtimeKey) > 256 {
			writeError(w, http.StatusBadRequest, errors.New("模型 API 金鑰超過長度限制"))
			return
		}
		upstreamRequest.Header.Set("X-OpenLoader-Key", runtimeKey)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	requestStartedAt := time.Now()
	response, err := client.Do(upstreamRequest)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("無法連線至模型 Runtime: %w", err))
		return
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeResponse+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New("讀取模型 Runtime 回應失敗"))
		return
	}
	if len(responseBody) > maxRuntimeResponse {
		writeError(w, http.StatusBadGateway, errors.New("模型 Runtime 回應超過大小限制"))
		return
	}
	var completion runtimeChatCompletion
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		writeError(w, http.StatusBadGateway, errors.New("模型 Runtime 回傳了無效格式"))
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(completion.Error.Message)
		if message == "" {
			message = fmt.Sprintf("模型 Runtime 拒絕請求（HTTP %d）", response.StatusCode)
		}
		writeError(w, http.StatusBadGateway, errors.New(message))
		return
	}
	if len(completion.Choices) == 0 {
		writeError(w, http.StatusBadGateway, errors.New("模型 Runtime 沒有回傳對話內容"))
		return
	}
	message := completion.Choices[0].Message
	content := decodeRuntimeMessageContent(message.Content)
	reasoning := decodeRuntimeMessageContent(message.ReasoningContent)
	if reasoning == "" {
		reasoning = decodeRuntimeMessageContent(message.Reasoning)
	}
	content, embeddedReasoning := splitRuntimeReasoning(content)
	if embeddedReasoning != "" && !strings.Contains(reasoning, embeddedReasoning) {
		if reasoning != "" {
			reasoning += "\n\n"
		}
		reasoning += embeddedReasoning
	}
	if content == "" && reasoning == "" {
		writeError(w, http.StatusBadGateway, errors.New("模型 Runtime 回傳了空白內容"))
		return
	}
	if content == "" {
		content = "（模型未提供最終回答）"
	}
	tokensPerSecond := completion.Timings.PredictedPerSecond
	if tokensPerSecond <= 0 {
		tokensPerSecond = completion.Timings.TokensPerSecond
	}
	if tokensPerSecond <= 0 {
		tokensPerSecond = completion.Usage.TokensPerSecond
	}
	if tokensPerSecond <= 0 && completion.Usage.CompletionTokens > 0 {
		elapsedSeconds := time.Since(requestStartedAt).Seconds()
		if elapsedSeconds > 0 {
			tokensPerSecond = float64(completion.Usage.CompletionTokens) / elapsedSeconds
		}
	}
	if math.IsNaN(tokensPerSecond) || math.IsInf(tokensPerSecond, 0) || tokensPerSecond < 0 {
		tokensPerSecond = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"content":       content,
		"reasoning":     reasoning,
		"finish_reason": completion.Choices[0].FinishReason,
		"runtime":       status.Runtime,
		"usage": map[string]any{
			"prompt_tokens":     completion.Usage.PromptTokens,
			"completion_tokens": completion.Usage.CompletionTokens,
			"total_tokens":      completion.Usage.TotalTokens,
			"tokens_per_second": tokensPerSecond,
		},
	})
}

func splitRuntimeReasoning(content string) (answer string, reasoning string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ""
	}
	for _, match := range thinkBlockPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			if reasoning != "" {
				reasoning += "\n\n"
			}
			reasoning += strings.TrimSpace(match[1])
		}
	}
	if reasoning != "" {
		return strings.TrimSpace(thinkBlockPattern.ReplaceAllString(content, "")), reasoning
	}
	// 部分模型的 chat template 會先吃掉 <think>，但把 </think> 留在輸出中。
	// 此時結尾標籤之前仍是思考內容，之後才是正式回答。
	if location := thinkClosePattern.FindStringIndex(content); location != nil {
		return strings.TrimSpace(content[location[1]:]), strings.TrimSpace(content[:location[0]])
	}
	if location := thinkOpenPattern.FindStringIndex(content); location != nil {
		return strings.TrimSpace(content[:location[0]]), strings.TrimSpace(content[location[1]:])
	}
	if match := thinkingTextPattern.FindStringSubmatch(content); len(match) == 3 {
		return strings.TrimSpace(match[2]), strings.TrimSpace(match[1])
	}
	return content, ""
}

func validateChatMessages(messages []chatMessage) error {
	if len(messages) == 0 {
		return errors.New("對話內容不可為空")
	}
	if len(messages) > maxChatMessages {
		return fmt.Errorf("單次對話最多包含 %d 則訊息", maxChatMessages)
	}
	totalRunes := 0
	for index, message := range messages {
		role := message.Role
		if role != "system" && role != "user" && role != "assistant" {
			return fmt.Errorf("第 %d 則訊息的角色不支援", index+1)
		}
		contentRunes := len([]rune(message.Content))
		if strings.TrimSpace(message.Content) == "" {
			return fmt.Errorf("第 %d 則訊息不可為空", index+1)
		}
		if contentRunes > maxChatMessageRunes {
			return fmt.Errorf("第 %d 則訊息超過長度限制", index+1)
		}
		totalRunes += contentRunes
		if totalRunes > maxChatTotalRunes {
			return errors.New("對話內容超過總長度限制，請清除後重新開始")
		}
	}
	if messages[len(messages)-1].Role != "user" {
		return errors.New("最後一則訊息必須由使用者送出")
	}
	return nil
}

func runtimeChatURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return "", errors.New("模型 Runtime API 位置無效")
	}
	parsed.Path = "/v1/chat/completions"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func decodeRuntimeMessageContent(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(texts, "\n")
}
