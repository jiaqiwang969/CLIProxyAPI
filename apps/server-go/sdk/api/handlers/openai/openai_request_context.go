package openai

import (
	"context"

	"github.com/gin-gonic/gin"
)

func ensureGinRequestContext(c *gin.Context) context.Context {
	if c == nil {
		return context.Background()
	}

	reqCtx := context.Background()
	if c.Request != nil && c.Request.Context() != nil {
		reqCtx = c.Request.Context()
		if ginCtx, ok := reqCtx.Value("gin").(*gin.Context); ok && ginCtx != nil {
			return reqCtx
		}
	}

	reqCtx = context.WithValue(reqCtx, "gin", c)
	if c.Request != nil {
		c.Request = c.Request.WithContext(reqCtx)
	}
	return reqCtx
}
