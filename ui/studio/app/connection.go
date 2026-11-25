package app

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
)

const connectionTestTimeout = 5 * time.Second

type ConnectionService struct{}

func NewConnectionService() *ConnectionService {
	return &ConnectionService{}
}

func (s *ConnectionService) TestDatabase(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectionTestTimeout)
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

// TestMQ checks the broker the two ways a login can fail: the AMQP port, and the
// management API, which is the one Studio works through and which refuses a login
// without a management tag.
func (s *ConnectionService) TestMQ(dsn string) error {
	cfg, err := parseMQConfig(dsn)
	if err != nil {
		return err
	}
	conn, err := amqp.DialConfig(dsn, amqp.Config{
		Dial: amqp.DefaultDial(connectionTestTimeout),
	})
	if err != nil {
		return fmt.Errorf("cannot connect to message queue: %s", connect.RedactDSN(err.Error()))
	}
	conn.Close()
	return testManagementAPI(cfg)
}

func testManagementAPI(cfg mqConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectionTestTimeout)
	defer cancel()
	_, err := fetchBrokerIdentity(ctx, http.DefaultClient, cfg)
	var refused *brokerStatusError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused) && refused.status == http.StatusUnauthorized:
		return fmt.Errorf("message queue connected, but its management API refused this login (it needs the management tag): %w", err)
	default:
		return fmt.Errorf("message queue connected, but its management API gave no usable answer: %w", err)
	}
}

// TestRedis logs in with the DSN's credentials and pings. Without a user name that is
// the default user, which everyone shares.
func (s *ConnectionService) TestRedis(dsn string) error {
	target, err := parseRedisDSN(dsn)
	if err != nil {
		return err
	}
	if err := pingRedis(target); err != nil {
		return fmt.Errorf("cannot connect to Redis: %w", err)
	}
	if target.user == "" || target.user == "default" {
		return errors.New("connected to Redis, but as its shared default user; use your own operator login")
	}
	return nil
}

type redisTarget struct {
	addr, host     string
	user, password string
	useTLS         bool
}

// parseRedisDSN reads redis://, rediss:// or a bare host[:port].
func parseRedisDSN(dsn string) (redisTarget, error) {
	if !strings.Contains(dsn, "://") {
		if ip := net.ParseIP(dsn); ip != nil && ip.To4() == nil {
			dsn = "[" + dsn + "]"
		}
		dsn = "redis://" + dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// url.Parse quotes what it could not read, which can be the password.
		return redisTarget{}, errors.New("the Redis DSN is not a valid URL; escape special characters in its password")
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return redisTarget{}, errors.New("the Redis DSN must start with redis:// or rediss://")
	}
	host := cmp.Or(u.Hostname(), "localhost")
	password, _ := u.User.Password()
	// Redis would not be asked to log in, and the ping would answer for another user.
	if u.User.Username() != "" && password == "" {
		return redisTarget{}, errors.New("the Redis DSN names a user but has no password")
	}
	return redisTarget{
		addr: net.JoinHostPort(host, cmp.Or(u.Port(), "6379")), host: host,
		user: u.User.Username(), password: password, useTLS: u.Scheme == "rediss",
	}, nil
}

func pingRedis(target redisTarget) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectionTestTimeout)
	defer cancel()

	var conn net.Conn
	var err error
	if target.useTLS {
		conn, err = (&tls.Dialer{Config: &tls.Config{ServerName: target.host}}).DialContext(ctx, "tcp", target.addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", target.addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}

	reader := bufio.NewReader(conn)
	call := func(args ...string) error {
		if _, err := conn.Write(redisCommand(args...)); err != nil {
			return err
		}
		reply, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		reply = strings.TrimSpace(reply)
		if !strings.HasPrefix(reply, "+") {
			return errors.New(strings.TrimPrefix(reply, "-"))
		}
		return nil
	}
	if target.password != "" {
		auth := []string{"AUTH"}
		if target.user != "" {
			auth = append(auth, target.user)
		}
		if err := call(append(auth, target.password)...); err != nil {
			// Only the error's code: a server that does not know the command answers
			// with its arguments, the password among them.
			code, _, _ := strings.Cut(err.Error(), " ")
			return fmt.Errorf("login refused (%s)", code)
		}
	}
	return call("PING")
}

// redisCommand encodes a command in Redis's wire format, where every argument
// carries its length, so a password may hold any byte.
func redisCommand(args ...string) []byte {
	out := []byte("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, arg := range args {
		out = fmt.Appendf(out, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return out
}
