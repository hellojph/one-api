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

// 仅拦截 /v1/chat/completions：
// 命中受限模型（gpt-5 / o1 / o3 家族及子版本）时：
//  1. 移除 temperature / top_p
//  2. 将 max_tokens -> max_completion_tokens
func ConstrainedModelSanitizer() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只处理 POST + /v1/chat/completions
		if c.Request.Method != http.MethodPost || !strings.HasPrefix(c.Request.URL.Path, "/v1/chat/completions") {
			c.Next()
			return
		}

		// 读取原始请求体
		raw, err := io.ReadAll(c.Request.Body)

		// 定义并统一恢复请求体（无论是否改写/早退）
		restore := func(b []byte) {
			c.Request.Body = io.NopCloser(bytes.NewReader(b))
			c.Request.ContentLength = int64(len(b))
		}
		defer restore(raw)

		// 读取失败或空体则放行（保持原样）
		if err != nil || len(raw) == 0 {
			c.Next()
			return
		}

		// 解析 JSON
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			c.Next()
			return
		}

		// 非受限模型直接放行
		model, _ := body["model"].(string)
		if !isConstrainedModel(model) {
			c.Next()
			return
		}

		// 1) 移除不支持的采样参数
		delete(body, "temperature")
		delete(body, "top_p")

		// 2) max_tokens -> max_completion_tokens（若用户传入了 max_tokens）
		if mt, ok := body["max_tokens"]; ok && mt != nil {
			if _, exists := body["max_completion_tokens"]; !exists {
				body["max_completion_tokens"] = mt
			}
			delete(body, "max_tokens")
		}

		// 写回改写后的请求体
		if patched, err := json.Marshal(body); err == nil {
			restore(patched)
		}

		// 交给后续处理
		c.Next()
	}
}

// 受限模型判定：gpt-4o / gpt-5 / o1 / o3 及其子版本
func isConstrainedModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}

	// 精确匹配（可按需扩展）
	switch m {
	case "gpt-4o", "gpt-5", "o1", "o1-mini", "o3":
		return true
	}

	// 前缀匹配（覆盖家族/子版本）
	for _, p := range []string{"gpt-4o-", "gpt-5-", "o1-", "o3-"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}

	// 环境变量追加（ONEAPI_CONSTRAINED_MODELS="foo,bar"）
	if extra := strings.TrimSpace(os.Getenv("ONEAPI_CONSTRAINED_MODELS")); extra != "" {
		for _, x := range strings.Split(extra, ",") {
			if strings.ToLower(strings.TrimSpace(x)) == m {
				return true
			}
		}
	}

	return false
}
