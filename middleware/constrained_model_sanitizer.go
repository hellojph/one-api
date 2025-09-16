package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// ConstrainedModelSanitizer 拦截 /v1/* 生成类请求；
// 命中受限模型（gpt-4o / gpt-5 / o1 / o3 家族）时：
//   - 移除 temperature/top_p
//   - 把 max_tokens 重命名为端点要求的参数
func ConstrainedModelSanitizer() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		// 常见生成路由
		if !(strings.HasPrefix(path, "/v1/chat/completions") ||
			strings.HasPrefix(path, "/v1/completions") ||
			strings.HasPrefix(path, "/v1/responses") ||
			(strings.HasPrefix(path, "/v1/threads/") && strings.Contains(path, "/runs")) ||
			(strings.HasPrefix(path, "/v1/assistants") && strings.Contains(path, "/runs"))) {
			c.Next()
			return
		}

		raw, err := io.ReadAll(c.Request.Body)
		if err != nil || len(raw) == 0 {
			c.Next()
			return
		}
		restore := func(b []byte) {
			c.Request.Body = io.NopCloser(bytes.NewReader(b))
			c.Request.ContentLength = int64(len(b))
		}
		defer restore(raw)

		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			c.Next()
			return
		}

		model, _ := body["model"].(string)
		if !isConstrainedModel(model) {
			c.Next()
			return
		}

		// 1. 删除不被支持的采样参数
		delete(body, "temperature")
		delete(body, "top_p")

		// 2. 按端点改名 max_tokens
		if mt, ok := body["max_tokens"]; ok && mt != nil {
			switch {
			case strings.HasPrefix(path, "/v1/responses"):
				if _, ex := body["max_output_tokens"]; !ex {
					body["max_output_tokens"] = mt
				}
				delete(body, "max_tokens")

			case strings.HasPrefix(path, "/v1/threads/") ||
				(strings.HasPrefix(path, "/v1/assistants") && strings.Contains(path, "/runs")):
				if _, ex := body["max_completion_tokens"]; !ex {
					body["max_completion_tokens"] = mt
				}
				delete(body, "max_tokens")

			// 关键：gpt-4o / gpt-5 在 Chat Completions 也要 max_completion_tokens
			case strings.HasPrefix(path, "/v1/chat/completions"),
				strings.HasPrefix(path, "/v1/completions"):
				if _, ex := body["max_completion_tokens"]; !ex {
					body["max_completion_tokens"] = mt
				}
				delete(body, "max_tokens")
			}
		}

		if patched, err := json.Marshal(body); err == nil {
			restore(patched)
		}
		c.Next()
	}
}

// 受限模型判定：gpt-4o / gpt-5 / o1 / o3 及其子版本
func isConstrainedModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// 精确匹配
	switch m {
	case "gpt-4o", "gpt-4o-mini", "gpt-5", "o1", "o1-mini", "o3":
		return true
	}
	// 前缀匹配
	for _, p := range []string{"gpt-4o-", "gpt-5-", "o1-", "o3-"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	// 环境变量追加
	if extra := strings.TrimSpace(os.Getenv("ONEAPI_CONSTRAINED_MODELS")); extra != "" {
		for _, x := range strings.Split(extra, ",") {
			if strings.EqualFold(strings.TrimSpace(x), model) {
				return true
			}
		}
	}
	return false
}
