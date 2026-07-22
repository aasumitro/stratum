package httpserver

import (
	"context"
	"crypto/hmac"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/platform/messaging"
)

const (
	statusHealthy   = "healthy"
	statusUnhealthy = "unhealthy"
)

var processStart = time.Now()

// HealthDeps holds the dependencies checked in the readiness probe.
type HealthDeps struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
	MQ    *messaging.Connection // nil = skip check

	// StatsToken gates /health/stats — see config.Config.StatsToken.
	StatsToken string
}

// verifyStatsToken reports whether headerToken may access /health/stats.
// An empty configToken means the check is disabled (open) — the same
// dev-friendly default as the payment webhook verifiers.
func verifyStatsToken(headerToken, configToken string) bool {
	if configToken == "" {
		return true
	}
	return hmac.Equal([]byte(headerToken), []byte(configToken))
}

// RegisterHealth mounts:
//
//	GET /health        — liveness  (always 200 while process is alive)
//	GET /health/ready  — readiness (pings Postgres, Redis, RabbitMQ)
//	GET /health/stats  — runtime   (goroutines, memory, pool stats) — gated
//	                     by StatsToken when configured, see HealthDeps
func RegisterHealth(r *gin.Engine, deps HealthDeps) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		status := http.StatusOK
		checks := gin.H{}

		if err := deps.Pool.Ping(ctx); err != nil {
			checks["postgres"] = statusUnhealthy
			status = http.StatusServiceUnavailable
		} else {
			checks["postgres"] = statusHealthy
		}

		if deps.Redis != nil {
			if err := deps.Redis.Ping(ctx).Err(); err != nil {
				checks["redis"] = statusUnhealthy
				status = http.StatusServiceUnavailable
			} else {
				checks["redis"] = statusHealthy
			}
		}

		if deps.MQ != nil {
			if err := deps.MQ.Ping(); err != nil {
				checks["rabbitmq"] = statusUnhealthy
				status = http.StatusServiceUnavailable
			} else {
				checks["rabbitmq"] = statusHealthy
			}
		}

		result := "ok"
		if status != http.StatusOK {
			result = "degraded"
		}

		c.JSON(status, gin.H{
			"status": result,
			"checks": checks,
		})
	})

	r.GET("/health/stats", func(c *gin.Context) {
		if !verifyStatsToken(c.GetHeader("X-Stats-Token"), deps.StatsToken) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing X-Stats-Token"})
			return
		}

		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)

		pool := deps.Pool.Stat()

		result := gin.H{
			"uptime_seconds": int64(time.Since(processStart).Seconds()),
			"num_cpu":        runtime.NumCPU(),
			"goroutines":     runtime.NumGoroutine(),
			"memory": gin.H{
				"heap_alloc_bytes":   ms.HeapAlloc,
				"heap_sys_bytes":     ms.HeapSys,
				"stack_in_use_bytes": ms.StackInuse,
				"gc_runs":            ms.NumGC,
				"gc_cpu_fraction":    ms.GCCPUFraction,
			},
			"db_pool": gin.H{
				"total_conns":    pool.TotalConns(),
				"idle_conns":     pool.IdleConns(),
				"acquired_conns": pool.AcquiredConns(),
				"max_conns":      pool.MaxConns(),
			},
		}

		if deps.Redis != nil {
			ps := deps.Redis.PoolStats()
			result["redis_pool"] = gin.H{
				"total_conns": ps.TotalConns,
				"idle_conns":  ps.IdleConns,
				"hits":        ps.Hits,
				"misses":      ps.Misses,
			}
		}

		c.JSON(http.StatusOK, result)
	})
}
