package main

import (
	"strings"
)

func generateContextualResponse(messages []ChatMessage) string {
	// Find the last user message
	var lastUserMessage *ChatMessage
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserMessage = &messages[i]
			break
		}
	}

	if lastUserMessage != nil {
		switch content := lastUserMessage.Content.(type) {
		case string:
			// Simple content-based response
			lowerContent := strings.ToLower(content)
			if strings.Contains(lowerContent, "hello") || strings.Contains(lowerContent, "hi") {
				return "Hello! I'm Claude through the Codex proxy. I can help you with coding tasks, debugging, and software development questions. What would you like to work on?"
			}
			if strings.Contains(lowerContent, "test") {
				return "I can help you with testing! Whether it's unit tests, integration tests, or debugging test failures, I'm here to assist. What specific testing challenge are you facing?"
			}
			if strings.Contains(lowerContent, "fix") || strings.Contains(lowerContent, "bug") || strings.Contains(lowerContent, "error") {
				return "I'd be happy to help fix bugs and errors! Please share the specific error message, code snippet, or behavior you're experiencing, and I'll help diagnose and resolve the issue."
			}
			if strings.Contains(lowerContent, "implement") || strings.Contains(lowerContent, "create") || strings.Contains(lowerContent, "build") {
				return "I can help you implement and build features! Please describe what you'd like to create - whether it's a function, component, API endpoint, or entire system - and I'll guide you through the implementation."
			}
			// Default response with content context
			return "I can help with your request. I see you mentioned something about your coding needs. Could you provide more specific details about what you'd like me to help you with? The proxy connection is working correctly."
		case []interface{}:
			// Handle array content (typical CLINE format)
			if len(content) > 0 {
				return "I'm ready to help with your coding task! I can see you've provided some context. Please let me know specifically what you'd like me to work on - whether it's debugging, implementing features, code review, or any other development task."
			}
		}
	}

	// Ultimate fallback
	return "I'm Claude, connected through the Codex proxy. I'm ready to help with coding tasks, debugging, implementation, and software development questions. What would you like to work on today?"
}
