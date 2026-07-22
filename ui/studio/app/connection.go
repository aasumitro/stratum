package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
)

type ConnectionService struct{}

func NewConnectionService() *ConnectionService {
	return &ConnectionService{}
}

func (s *ConnectionService) TestDatabase(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("cannot connect to database: %s", connect.RedactDSN(err.Error()))
	}
	defer conn.Close(ctx)

	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %s", connect.RedactDSN(err.Error()))
	}
	return nil
}

func (s *ConnectionService) TestMQ(dsn string) error {
	conn, err := amqp.DialConfig(dsn, amqp.Config{
		Dial: amqp.DefaultDial(5 * time.Second),
	})
	if err != nil {
		return fmt.Errorf("cannot connect to message queue: %s", connect.RedactDSN(err.Error()))
	}
	conn.Close()
	return nil
}

func (s *ConnectionService) TestRedis(dsn string) error {
	addr, err := redisAddr(dsn)
	if err != nil {
		return fmt.Errorf("invalid Redis DSN: %s", connect.RedactDSN(err.Error()))
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to Redis: %w", err)
	}
	conn.Close()
	return nil
}

func redisAddr(dsn string) (string, error) {
	if strings.HasPrefix(dsn, "redis://") || strings.HasPrefix(dsn, "rediss://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		if u.Port() == "" {
			return u.Hostname() + ":6379", nil
		}
		return u.Host, nil
	}
	if !strings.Contains(dsn, ":") {
		return dsn + ":6379", nil
	}
	return dsn, nil
}
