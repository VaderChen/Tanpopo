package api

import (
	"bytes"
	"encoding/base64"
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
	Role    string            `json:"role"`
	Content string            `json:"-"`
	Parts   []chatContentPart `json:"-"`
}

type chatContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL    string `json:"url"`
		Detail string `json:"detail,omitempty"`
	} `json:"image_url,omitempty"`
}

func (m *chatMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	*m = chatMessage{Role: raw.Role}
	if err := json.Unmarshal(raw.Content, &m.Content); err == nil {
		return nil
	}
	decoder = json.NewDecoder(bytes.NewReader(raw.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m.Parts); err != nil {
		return errors.New("content 必須是文字或文字／圖片陣列")
	}
	return nil
}

func (m chatMessage) MarshalJSON() ([]byte, error) {
	var content any = m.Content
	if len(m.Parts) > 0 {
		content = m.Parts
	}
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}{m.Role, content})
}

type chatCompletionRequest struct {
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens"`
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

// 同一 Server 的請求共用連線池；金鑰仍只放在各自的 Request，且不使用代理或 Cookie Jar。
func (s *Server) runtimeChatHTTPClient() *http.Client {
	s.chatClientOnce.Do(func() { s.chatClient = newRuntimeChatHTTPClient() })
	return s.chatClient
}

func newRuntimeChatHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 8
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	return &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func isRuntimeLoadingMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	for _, fragment := range []string{
		"模型服務仍在載入中",
		"模型載入中",
		"loading model",
		"loading the model",
		"model is loading",
		"model loading",
		"model is still loading",
		"model is being loaded",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var request chatCompletionRequest
	if err := decodeJSONLimit(r, &request, 32<<20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateChatMessages(request.Messages); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	maxTokens := request.MaxTokens
	if maxTokens == 0 {
		maxTokens = 2048
	}
	if maxTokens < 1 || maxTokens > 4096 {
		writeError(w, http.StatusBadRequest, errors.New("max_tokens 必須介於 1 與 4096 之間"))
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
	upstreamPayload := map[string]any{
		"messages":   request.Messages,
		"max_tokens": maxTokens,
		"stream":     request.Stream,
	}
	if request.Stream {
		// llama-server 需要此選項才會在最後一個 SSE event 附上 usage；
		// mlx-server 會忽略不認識的欄位並照常串流。
		upstreamPayload["stream_options"] = map[string]any{"include_usage": true}
	}
	body, err := json.Marshal(upstreamPayload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("無法建立對話請求"))
		return
	}

	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("無法建立 Runtime 請求"))
		return
	}
	if request.Stream {
		upstreamRequest.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamRequest.Header.Set("Accept", "application/json")
	}
	upstreamRequest.Header.Set("Content-Type", "application/json")
	if runtimeKey := strings.TrimSpace(r.Header.Get("X-Tanpopo-Key")); runtimeKey != "" {
		if len(runtimeKey) > 256 {
			writeError(w, http.StatusBadRequest, errors.New("模型 API 金鑰超過長度限制"))
			return
		}
		upstreamRequest.Header.Set("X-OpenLoader-Key", runtimeKey)
	}

	requestStartedAt := time.Now()
	response, err := s.runtimeChatHTTPClient().Do(upstreamRequest)
	if err != nil {
		latest := s.llama.Status()
		if latest.Running && !latest.Ready {
			writeError(w, http.StatusConflict, errors.New("模型服務仍在載入中，請稍候"))
		} else {
			writeError(w, http.StatusBadGateway, errors.New("模型 Runtime 已停止或無法連線，請返回執行狀態確認"))
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeRuntimeChatError(w, response)
		return
	}
	if request.Stream {
		proxyRuntimeChatStream(w, response)
		return
	}

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

func writeRuntimeChatError(w http.ResponseWriter, response *http.Response) {
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
	_ = json.Unmarshal(responseBody, &completion)
	message := strings.TrimSpace(completion.Error.Message)
	if message == "" {
		message = fmt.Sprintf("模型 Runtime 拒絕請求（HTTP %d）", response.StatusCode)
	}
	if isRuntimeLoadingMessage(message) {
		writeError(w, http.StatusConflict, errors.New("模型服務仍在載入中，請稍候"))
		return
	}
	// 保留 Runtime 的用戶端錯誤語意，讓金鑰拒絕、容量限制與名額已滿
	// 不會被代理改成服務故障。串流與一般 JSON 回應共用此路徑。
	status := http.StatusBadGateway
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		status = response.StatusCode
	}
	if status == http.StatusTooManyRequests {
		if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
	}
	// 管理登入與模型金鑰是兩個不同驗證層；讓 UI 可區分 Runtime 的 401。
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"message": message, "source": "runtime"},
	})
}

func proxyRuntimeChatStream(w http.ResponseWriter, response *http.Response) {
	_, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("目前的 HTTP 連線不支援串流回應"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	buffer := make([]byte, 32*1024)
	for {
		readCount, readErr := response.Body.Read(buffer)
		if readCount > 0 {
			if _, writeErr := w.Write(buffer[:readCount]); writeErr != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
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
	imageCount := 0
	for index, message := range messages {
		role := message.Role
		if role != "system" && role != "user" && role != "assistant" {
			return fmt.Errorf("第 %d 則訊息的角色不支援", index+1)
		}
		contentRunes := len([]rune(message.Content))
		if len(message.Parts) > 64 {
			return errors.New("單則訊息的內容片段過多")
		}
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				if part.ImageURL != nil || strings.TrimSpace(part.Text) == "" {
					return errors.New("文字片段格式無效")
				}
				contentRunes += len([]rune(part.Text))
			case "image_url":
				imageCount++
				if role != "user" || part.ImageURL == nil || part.Text != "" || imageCount > 8 {
					return errors.New("圖片只能放在使用者訊息，且單次最多 8 張")
				}
				if err := validateChatImage(part.ImageURL.URL); err != nil {
					return err
				}
			default:
				return errors.New("對話片段只支援 text 與 image_url")
			}
		}
		if len(message.Parts) == 0 && strings.TrimSpace(message.Content) == "" {
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

func validateChatImage(value string) error {
	header, payload, ok := strings.Cut(value, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/webp;base64" && header != "data:image/gif;base64") {
		return errors.New("圖片需使用 PNG／JPEG／WebP／GIF 的 base64 data URL")
	}
	count, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload)), (25<<20)+1))
	if err != nil || count == 0 || count > 25<<20 {
		return errors.New("圖片 base64 無效或超過 25 MiB")
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
