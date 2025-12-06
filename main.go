package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat Completions API format (what CLINE sends)
type ChatCompletionsRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float32      `json:"temperature,omitempty"`
	MaxTokens   *int32        `json:"max_tokens,omitempty"`
	Stream      *bool         `json:"stream,omitempty"`
	Tools       interface{}   `json:"tools,omitempty"`
	ToolChoice  interface{}   `json:"tool_choice,omitempty"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or array
}

// Chat Completions API response format (what CLINE expects)
type ChatCompletionsResponse struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Choices []Choice  `json:"choices"`
	Usage   *Usage    `json:"usage,omitempty"`
}

type Choice struct {
	Index        int32             `json:"index"`
	Message      ChatResponseMessage `json:"message"`
	FinishReason *string           `json:"finish_reason,omitempty"`
}

type ChatResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

// Codex Responses API format (what we send to ChatGPT backend)
type ResponsesApiRequest struct {
	Model             string        `json:"model"`
	Instructions      string        `json:"instructions"`
	Input             []ResponseItem `json:"input"`
	Tools             []interface{} `json:"tools"`
	ToolChoice        string        `json:"tool_choice"`
	ParallelToolCalls bool          `json:"parallel_tool_calls"`
	Reasoning         interface{}   `json:"reasoning,omitempty"`
	Store             bool          `json:"store"`
	Stream            bool          `json:"stream"`
	Include           []string      `json:"include"`
}

type ResponseItem struct {
	Type    string         `json:"type"`
	ID      *string        `json:"id,omitempty"`
	Role    string         `json:"role"`
	Content []ContentItem  `json:"content"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Codex auth.json structure
type AuthData struct {
	APIKey    *string     `json:"OPENAI_API_KEY,omitempty"`
	Tokens    *TokenData  `json:"tokens,omitempty"`
}

type TokenData struct {
	AccessToken  string  `json:"access_token"`
	AccountID    string  `json:"account_id"`
	RefreshToken *string `json:"refresh_token,omitempty"`
}

// Codex Responses API response format
type ResponsesApiResponse struct {
	Response *ResponseOutput `json:"response,omitempty"`
	ID       *string         `json:"id,omitempty"`
}

type ResponseOutput struct {
	Content *[]ResponseContentItem `json:"content,omitempty"`
	Role    *string                `json:"role,omitempty"`
}

type ResponseContentItem struct {
	Type string  `json:"type"`
	Text *string `json:"text,omitempty"`
}

type ProxyServer struct {
	Client    *http.Client
	AuthData  AuthData
}

func main() {
	// Parse command line arguments
	var port string
	var authPath string

	flag.StringVar(&port, "port", "4303", "Port to listen on")
	flag.StringVar(&authPath, "auth-path", "", "Path to Codex auth.json file (auto-discover if not specified)")
	flag.Parse()

	fmt.Println("Initializing Codex OpenAI Proxy...")

	// Initialize proxy server
	proxy, err := NewProxyServer(authPath)
	if err != nil {
		log.Fatalf("Failed to initialize proxy server: %v", err)
	}

	if authPath == "" {
		fmt.Println("✓ Loaded authentication via auto-discovery")
	} else {
		fmt.Printf("✓ Loaded authentication from %s\n", authPath)
	}

	// Setup Gin router
	r := gin.Default()

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "authorization, content-type, accept, accept-encoding, x-stainless-arch, x-stainless-lang, x-stainless-os, x-stainless-package-version, x-stainless-retry-count, x-stainless-runtime, x-stainless-runtime-version, x-stainless-timeout")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		fmt.Println("💚 Health check requested")
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "codex-openai-proxy",
		})
	})

	// Models endpoints
	r.GET("/models", handleModels)
	r.GET("/v1/models", handleModels)

	// Chat completions endpoints
	r.POST("/chat/completions", func(c *gin.Context) { handleChatCompletions(c, proxy) })
	r.POST("/v1/chat/completions", func(c *gin.Context) { handleChatCompletions(c, proxy) })

	fmt.Printf("🚀 Codex OpenAI Proxy listening on http://0.0.0.0:%s\n", port)
	fmt.Printf("   Health check: http://localhost:%s/health\n", port)
	fmt.Printf("   Chat endpoint: http://localhost:%s/v1/chat/completions\n", port)
	fmt.Println("\n   Configure CLINE with:")
	fmt.Printf("   Base URL: http://localhost:%s\n", port)
	fmt.Println("   Model: gpt-5")
	fmt.Println("   API Key: (any value)")

	r.Run(":" + port)
}

func findAuthFile() (string, error) {
	// Possible locations for auth.json
	possiblePaths := []string{
		"./auth.json",
		"./.codex/auth.json",
		"~/.codex/auth.json",
	}

	for _, path := range possiblePaths {
		var fullPath string

		if strings.HasPrefix(path, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			fullPath = filepath.Join(homeDir, path[2:])
		} else {
			fullPath = path
		}

		if _, err := os.Stat(fullPath); err == nil {
			fmt.Printf("✓ Found auth.json at: %s\n", fullPath)
			return fullPath, nil
		}
	}

	return "", fmt.Errorf("auth.json not found in any of the expected locations: %v", possiblePaths)
}

func NewProxyServer(authPath string) (*ProxyServer, error) {
	var finalAuthPath string
	var err error

	if authPath == "" || authPath == "~/.codex/auth.json" {
		// Auto-discovery mode
		finalAuthPath, err = findAuthFile()
		if err != nil {
			return nil, fmt.Errorf("auto-discovery failed: %w", err)
		}
	} else {
		// Expand home directory for manual path
		if strings.HasPrefix(authPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			finalAuthPath = filepath.Join(homeDir, authPath[2:])
		} else {
			finalAuthPath = authPath
		}
	}

	// Read auth.json file
	authContent, err := os.ReadFile(finalAuthPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read auth.json from %s: %w", finalAuthPath, err)
	}

	var authData AuthData
	if err := json.Unmarshal(authContent, &authData); err != nil {
		return nil, fmt.Errorf("failed to parse auth.json: %w", err)
	}

	// Create HTTP client with browser-like configuration
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	return &ProxyServer{
		Client:   client,
		AuthData: authData,
	}, nil
}

func (p *ProxyServer) convertChatToResponses(chatReq ChatCompletionsRequest) ResponsesApiRequest {
	// Convert messages to ResponseItems
	var input []ResponseItem

	for _, msg := range chatReq.Messages {
		// Convert content to string (handle both string and array formats)
		var contentText string
		switch content := msg.Content.(type) {
		case string:
			contentText = content
		case []interface{}:
			// Extract text from array elements
			var parts []string
			for _, item := range content {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if text, exists := itemMap["text"]; exists {
						if textStr, ok := text.(string); ok {
							parts = append(parts, textStr)
						}
					}
				} else if itemStr, ok := item.(string); ok {
					parts = append(parts, itemStr)
				}
			}
			contentText = strings.Join(parts, " ")
		default:
			contentText = fmt.Sprintf("%v", content)
		}

		input = append(input, ResponseItem{
			Type: "message",
			Role: msg.Role,
			Content: []ContentItem{
				{
					Type: "input_text",
					Text: contentText,
				},
			},
		})
	}

	// Use proper instructions for ChatGPT Responses API
	instructions := "You are a helpful AI assistant. Provide clear, accurate, and concise responses to user questions and requests."

	var tools []interface{}
	if chatReq.Tools != nil {
		tools = []interface{}{} // For now, use empty array
	}

	return ResponsesApiRequest{
		Model:             chatReq.Model,
		Instructions:      instructions,
		Input:             input,
		Tools:             tools,
		ToolChoice:        "auto",
		ParallelToolCalls: false,
		Store:             false,
		Stream:            true,
		Include:           []string{},
	}
}

func (p *ProxyServer) proxyRequest(chatReq ChatCompletionsRequest) (*ChatCompletionsResponse, error) {
	// For now, return a working response while we implement backend
	fmt.Printf("🔄 Processing CLINE request...\n")
	if chatReq.Stream != nil {
		fmt.Printf("🔍 Stream setting: %t\n", *chatReq.Stream)
	}

	chatRes := ChatCompletionsResponse{
		ID:      fmt.Sprintf("chatcmpl-%s", uuid.New().String()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   chatReq.Model,
		Choices: []Choice{
			{
				Index: 0,
				Message: ChatResponseMessage{
					Role:    "assistant",
					Content: "I can help you with coding tasks! The proxy connection is working well. What would you like assistance with? (Note: Currently running in development mode while ChatGPT backend integration is being finalized.)",
				},
				FinishReason: stringPtr("stop"),
			},
		},
		Usage: &Usage{
			PromptTokens:     50,
			CompletionTokens: 30,
			TotalTokens:      80,
		},
	}

	return &chatRes, nil
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

func handleModels(c *gin.Context) {
	logRequest(c)

	fmt.Println("📋 === MATCHED MODELS REQUEST ===")
	fmt.Println("📋 === END MATCHED ===\n")

	modelsResponse := gin.H{
		"object": "list",
		"data": []gin.H{
			{
				"id":      "gpt-4",
				"object":  "model",
				"created": 1687882411,
				"owned_by": "openai",
			},
			{
				"id":       "gpt-5",
				"object":   "model",
				"created":  1687882411,
				"owned_by": "openai",
			},
		},
	}

	c.JSON(200, modelsResponse)
}

func handleChatCompletions(c *gin.Context, proxy *ProxyServer) {
	logRequest(c)

	// Read request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Failed to read request body"}})
		return
	}

	fmt.Println("🔥 === MATCHED CHAT COMPLETIONS ===")

	// Log request details
	fmt.Printf("\n📋 === CLINE REQUEST DETAILS FOR CURL ===\n")
	fmt.Printf("Method: %s\n", c.Request.Method)
	fmt.Printf("Path: %s\n", c.Request.URL.Path)
	fmt.Printf("Body size: %d bytes\n", len(body))

	// Log headers
	fmt.Println("\nHeaders for curl:")
	for name, values := range c.Request.Header {
		headerName := strings.ToLower(name)
		if headerName == "authorization" {
			if len(values) > 0 {
				fmt.Printf("  -H \"%s: %s***\"\n", name, values[0][:min(20, len(values[0]))])
			}
		} else {
			for _, value := range values {
				fmt.Printf("  -H \"%s: %s\"\n", name, value)
			}
		}
	}

	// Log body (truncated)
	fmt.Println("\nBody (first 1000 chars):")
	truncated := body
	if len(body) > 1000 {
		truncated = body[:1000]
		fmt.Printf("%s... [TRUNCATED]\n", string(truncated))
	} else {
		fmt.Println(string(body))
	}

	// Parse JSON
	var chatReq ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		fmt.Printf("❌ JSON parse error: %v\n", err)
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid JSON"}})
		return
	}

	fmt.Printf("   Model: %s\n", chatReq.Model)
	fmt.Printf("   Messages: %d items\n", len(chatReq.Messages))
	for i, msg := range chatReq.Messages {
		var contentPreview string
		switch content := msg.Content.(type) {
		case string:
			contentPreview = content[:min(50, len(content))]
		case []interface{}:
			contentPreview = fmt.Sprintf("[array with %d items]", len(content))
		default:
			contentStr := fmt.Sprintf("%v", content)
			contentPreview = fmt.Sprintf("[%s]", contentStr[:min(50, len(contentStr))])
		}
		fmt.Printf("   [%d] %s: %s\n", i, msg.Role, contentPreview)
	}
	fmt.Println("🔥 === END MATCHED ===\n")

	// Check if streaming is requested
	if chatReq.Stream != nil && *chatReq.Stream {
		fmt.Println("🔄 STREAMING: CLINE requested streaming response, implementing SSE format")

		// Generate contextual response based on user messages
		message := generateContextualResponse(chatReq.Messages)
		fmt.Printf("📝 Generated contextual response: %s\n", message[:min(100, len(message))])

		chunkID := fmt.Sprintf("chatcmpl-streaming-%s", uuid.New().String())
		model := chatReq.Model

		// Create SSE response
		var sseChunks []string

		// First chunk with role
		sseChunks = append(sseChunks, fmt.Sprintf("data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n",
			chunkID, time.Now().Unix(), model))

		// Content chunk
		sseChunks = append(sseChunks, fmt.Sprintf("data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"},\"finish_reason\":null}]}\n\n",
			chunkID, time.Now().Unix(), model, strings.ReplaceAll(message, "\"", "\\\"")))

		// Final chunk
		sseChunks = append(sseChunks, fmt.Sprintf("data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			chunkID, time.Now().Unix(), model))

		// End marker
		sseChunks = append(sseChunks, "data: [DONE]\n\n")

		sseResponse := strings.Join(sseChunks, "")

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.String(200, sseResponse)
	} else {
		// Handle non-streaming request
		response, err := proxy.proxyRequest(chatReq)
		if err != nil {
			fmt.Printf("Proxy error: %v\n", err)
			c.JSON(500, gin.H{
				"error": gin.H{
					"message": fmt.Sprintf("Proxy error: %v", err),
					"type":    "proxy_error",
					"code":    "internal_error",
				},
			})
			return
		}

		c.Header("Content-Type", "application/json")
		c.JSON(200, response)
	}
}

func logRequest(c *gin.Context) {
	timestamp := time.Now().Format("2006-01-02 15:04:05.000000 UTC")

	fmt.Printf("\n🔍 === INTERCEPTED REQUEST ===\n")
	fmt.Printf("⏰ Timestamp: %s\n", timestamp)
	fmt.Printf("📥 Method: %s\n", c.Request.Method)
	fmt.Printf("📍 Path: %s\n", c.Request.URL.Path)

	// Log all headers
	fmt.Printf("\n📋 Headers (%d total):\n", len(c.Request.Header))
	for name, values := range c.Request.Header {
		headerName := strings.ToLower(name)
		for _, value := range values {
			if headerName == "user-agent" || headerName == "client" || strings.Contains(headerName, "cline") {
				fmt.Printf("  🎯 %s: %s\n", name, value)
			} else if headerName == "authorization" {
				fmt.Printf("  🔐 %s: %s***\n", name, value[:min(20, len(value))])
			} else {
				fmt.Printf("  📄 %s: %s\n", name, value)
			}
		}
	}

	// Check for VS Code specific patterns
	userAgent := c.GetHeader("User-Agent")
	if strings.Contains(strings.ToLower(userAgent), "vscode") {
		fmt.Println("🎯 DETECTED: VS Code client!")
	}
	if strings.Contains(strings.ToLower(userAgent), "cline") {
		fmt.Println("🎯 DETECTED: CLINE extension!")
	}

	fmt.Println("🔍 === END INTERCEPT ===\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
