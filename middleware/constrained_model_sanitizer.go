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
// 命中受限模型（gpt-5 / o1 / o3 家族）时，移除 temperature/top_p；
// 可选把 max_tokens → max_completion_tokens（ONEAPI_SANITIZE_MAXTOKENS=true）。
func ConstrainedModelSanitizer() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 仅处理 POST
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		// 仅处理 OpenAI 兼容的常见生成路由
		p := c.Request.URL.Path
		if !(strings.HasPrefix(p, "/v1/chat/completions") ||
			strings.HasPrefix(p, "/v1/completions") ||
			strings.HasPrefix(p, "/v1/responses")) {
			c.Next()
			return
		}

		// 读入原始 Body
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

		// 用 map 解析，避免依赖具体 struct
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			c.Next()
			return
		}

		// 模型判定
		model, _ := body["model"].(string)
		if !isConstrainedModel(model) {
			c.Next()
			return
		}

		// 移除不被支持的采样参数
		delete(body, "temperature")
		delete(body, "top_p")

		// 可选：max_tokens → max_completion_tokens
		if strings.EqualFold(os.Getenv("ONEAPI_SANITIZE_MAXTOKENS"), "true") {
			if _, ok := body["max_completion_tokens"]; !ok {
				if mt, ok := body["max_tokens"]; ok && mt != nil {
					body["max_completion_tokens"] = mt
					delete(body, "max_tokens")
				}
			}
		}

		// 写回请求体
		if patched, err := json.Marshal(body); err == nil {
			restore(patched)
		}
		c.Next()
	}
}

// 受限模型：精确 + 前缀 + 环境变量追加
func isConstrainedModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// 精确命中
	switch m {
	case "gpt-5", "o1", "o1-mini", "o3":
		return true
	}
	// 系列前缀
	for _, p := range []string{"o1-", "o3-", "gpt-5-"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	// 环境变量追加：ONEAPI_CONSTRAINED_MODELS="gpt-5x,my-o1-proxy"
	if extra := strings.TrimSpace(os.Getenv("ONEAPI_CONSTRAINED_MODELS")); extra != "" {
		for _, x := range strings.Split(extra, ",") {
			if strings.EqualFold(strings.TrimSpace(x), model) {
				return true
			}
		}
	}
	return false
}
