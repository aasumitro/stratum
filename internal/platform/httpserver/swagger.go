package httpserver

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/aasumitro/stratum/internal/platform/httpserver/docs" // registers the generated spec with swag at init
)

// @title       Stratum API
// @version     1.0
// @description REST API for Stratum — organizations, accounts, billing, and notifications.
// @BasePath    /api/v1

// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
// @description                Bearer token issued by Supabase auth.

// RegisterSwagger mounts the Swagger UI at /swagger/*any, backed by the
// spec generated into ./docs by `make swagger`. Callers should only mount
// this for non-production environments — the UI exposes the full
// request/response schema and isn't meant to be public.
func RegisterSwagger(engine *gin.Engine) {
	engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}
