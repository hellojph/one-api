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
// 命中受限模型（gpt-5 / o1 / o3 家族）时：
//   - 移除 temperature/top_p
//   - 按端点把 max_tokens 重命名为正确参数：
//       /v1/responses           -> max_output_tokens
//       /v1/threads/.../runs    -> max_completion_tokens
//       /v1/assistants/.../runs -> max_completion_tokens
//       /v1/chat/completions    -> 保留 max_tokens（如模型不支持该端点，仍会被上游拒绝）
func ConstrainedModelSanitizer() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 仅处理 POST
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 仅处理常见生成路由
		if !(
			strings.HasPrefix(path, "/v1/chat/completions") ||
				strings.HasPrefix(path, "/v1/completions") ||
				strings.HasPrefix(path, "/v1/responses") ||
				strings.HasPrefix(path, "/v1/assistants") ||
				strings.HasPrefix(path, "/v1/threads/") || // runs 在 threads 下
				strings.HasSuffix(path, "/runs") ||
				strings.Contains(path, "/runs/") ||
				strings.Contains(path, "/submit_tool_outputs") ||
				strings.Contains(path, "/instructions")
		) {
			c.Next()
			return
		}

		// 读入原始 body
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

		// 解析为 map，避免强依赖具体结构
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

		// 1) 移除不被支持的采样参数
		delete(body, "temperature")
		delete(body, "top_p")

		// 2) 处理 “最大输出 token” 参数名
		// 逻辑：仅当用户提供了 max_tokens 且目标端点不接受该名字时，执行“改名”
		if mt, ok := body["max_tokens"]; ok && mt != nil {
			switch {
			case strings.HasPrefix(path, "/v1/responses"):
				// Responses API
				if _, exists := body["max_output_tokens"]; !exists {
					body["max_output_tokens"] = mt
				}
				delete(body, "max_tokens")

			case strings.HasPrefix(path, "/v1/threads/") || // Assistants Runs
				(strings.HasPrefix(path, "/v1/assistants") && strings.HasSuffix(path, "/runs")):
				if _, exists := body["max_completion_tokens"]; !exists {
					body["max_completion_tokens"] = mt
				}
				delete(body, "max_tokens")

			case strings.HasPrefix(path, "/v1/chat/completions"),
				strings.HasPrefix(path, "/v1/completions"):
				// 旧端点仍是 max_tokens：不改名
			default:
				// 保险：如果不明端点但模型受限，且环境变量要求兼容，则改成 max_completion_tokens
				if strings.EqualFold(os.Getenv("ONEAPI_SANITIZE_MAXTOKENS"), "true") {
					if _, exists := body["max_completion_tokens"]; !exists {
						body["max_completion_tokens"] = mt
					}
					delete(body, "max_tokens")
				}
			}
		} else {
			// 可选：给受限模型一个默认上限，避免无限输出（按需开启）
			// if strings.HasPrefix(path, "/v1/responses") {
			// 	if _, exists := body["max_output_tokens"]; !exists {
			// 		body["max_output_tokens"] = 1024
			// 	}
			// } else if strings.HasPrefix(path, "/v1/threads/") || strings.HasSuffix(path, "/runs") {
			// 	if _, exists := body["max_completion_tokens"]; !exists {
			// 		body["max_completion_tokens"] = 1024
			// 	}
			// }
		}

		// 写回
		if patched, err := json.Marshal(body); err == nil {
			restore(patched)
		}
		c.Next()
	}
}

// 受限模型判定：精确 + 前缀 + 环境变量追加
func isConstrainedModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	switch m {
	case "gpt-5", "o1", "o1-mini", "o3":
		return true
	}
	for _, p := range []string{"o1-", "o3-", "gpt-5-"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	// 允许通过环境变量追加：ONEAPI_CONSTRAINED_MODELS="gpt-5x,my-o1-proxy"
	if extra := strings.TrimSpace(os.Getenv("ONEAPI_CONSTRAINED_MODELS")); extra != "" {
		for _, x := range strings.Split(extra, ",") {
			if strings.EqualFold(strings.TrimSpace(x), model) {
				return true
			}
		}
	}
	return false
}
