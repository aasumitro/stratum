package app

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeRedis answers AUTH and PING the way a server with one ACL user and a default
// user with a password does, and returns its address.
func fakeRedis(t *testing.T, user, password string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				authenticated := false
				for {
					args, err := readRedisCommand(reader)
					if err != nil {
						return
					}
					reply := "-ERR unknown command\r\n"
					switch args[0] {
					case "AUTH":
						authenticated = args[len(args)-1] == password && (len(args) == 2 || args[1] == user)
						reply = "-WRONGPASS invalid username-password pair\r\n"
						if authenticated {
							reply = "+OK\r\n"
						}
					case "PING":
						reply = "-NOAUTH Authentication required.\r\n"
						if authenticated {
							reply = "+PONG\r\n"
						}
					}
					if _, err := conn.Write([]byte(reply)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func readRedisCommand(reader *bufio.Reader) ([]string, error) {
	readInt := func() (int, error) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(line[1:]))
	}
	count, err := readInt()
	if err != nil {
		return nil, err
	}
	args := make([]string, count)
	for i := range args {
		size, err := readInt()
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		for read := 0; read < len(buf); {
			n, err := reader.Read(buf[read:])
			if err != nil {
				return nil, err
			}
			read += n
		}
		args[i] = string(buf[:size])
	}
	return args, nil
}

func TestTestRedis(t *testing.T) {
	const user, password = "op_ana", "p@ss word"
	addr := fakeRedis(t, user, password)
	service := NewConnectionService()
	for name, c := range map[string]struct {
		dsn  string
		want string // part of the error, "" for none
	}{
		"own login":            {"redis://op_ana:p%40ss%20word@" + addr, ""},
		"wrong password":       {"redis://op_ana:nope@" + addr, "WRONGPASS"},
		"no credentials":       {"redis://" + addr, "NOAUTH"},
		"bare address":         {addr, "NOAUTH"},
		"password only":        {"redis://:p%40ss%20word@" + addr, "shared default user"},
		"unsupported scheme":   {"http://" + addr, "must start with"},
		"user, no password":    {"redis://op_ana@" + addr, "no password"},
		"malformed":            {"redis://op_ana:p@ss word@h:bad", "not a valid URL"},
		"malformed, no user":   {"redis://:p@ss%word@h", "not a valid URL"},
		"nothing listening":    {"redis://127.0.0.1:1", "cannot connect to Redis"},
		"plain server, TLS on": {"rediss://op_ana:p%40ss%20word@" + addr, "cannot connect to Redis"},
	} {
		t.Run(name, func(t *testing.T) {
			err := service.TestRedis(c.dsn)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("got %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("got %v, want an error naming %q", err, c.want)
			}
			for _, secret := range []string{password, "p%40ss", "ss%word", "ss word"} {
				if err != nil && strings.Contains(err.Error(), secret) {
					t.Errorf("the error carries the password: %v", err)
				}
			}
		})
	}
}

// A server that does not know AUTH answers with the arguments it was sent.
func TestTestRedis_RefusalNeverEchoesThePassword(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := readRedisCommand(bufio.NewReader(conn)); err != nil {
			return
		}
		_, _ = conn.Write([]byte("-ERR unknown command 'AUTH', with args beginning with: 'op_ana' 's3cret'\r\n"))
	}()
	err = NewConnectionService().TestRedis("redis://op_ana:s3cret@" + listener.Addr().String())
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("got %v, want a refusal without the password", err)
	}
}

// Only a 401 says something about the login. Anything else is the API not answering.
func TestManagementAPIErrors(t *testing.T) {
	status := http.StatusOK
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"name":"op_ana","tags":["management"]}`))
	}))
	cfg := mqConfig{apiBase: broker.URL + "/api", user: "op_ana", pass: "x"}
	if err := testManagementAPI(cfg); err != nil {
		t.Errorf("working login: %v", err)
	}
	status = http.StatusUnauthorized
	if err := testManagementAPI(cfg); err == nil || !strings.Contains(err.Error(), "management tag") {
		t.Errorf("401: got %v, want the tag named", err)
	}
	status = http.StatusBadGateway
	if err := testManagementAPI(cfg); err == nil || strings.Contains(err.Error(), "management tag") {
		t.Errorf("502: got %v, want no word about the tag", err)
	}
	broker.Close()
	if err := testManagementAPI(cfg); err == nil || strings.Contains(err.Error(), "management tag") {
		t.Errorf("unreachable: got %v, want no word about the tag", err)
	}
}

func TestParseMQConfig(t *testing.T) {
	cfg, err := parseMQConfig("amqp://op_ana:x@[::1]:5672/")
	if err != nil || cfg.apiBase != "http://[::1]:15672/api" {
		t.Errorf("IPv6 host: %+v, %v", cfg, err)
	}
	if _, err := parseMQConfig("amqp://op_ana:s3cr/et@host"); err == nil || strings.Contains(err.Error(), "s3cr") {
		t.Errorf("malformed: got %v, want an error without the password", err)
	}
}

func TestParseRedisDSN(t *testing.T) {
	for dsn, want := range map[string]redisTarget{
		"":                                  {addr: "localhost:6379", host: "localhost"},
		":6380":                             {addr: "localhost:6380", host: "localhost"},
		"::1":                               {addr: "[::1]:6379", host: "::1"},
		"cache.example":                     {addr: "cache.example:6379", host: "cache.example"},
		"cache.example:6380":                {addr: "cache.example:6380", host: "cache.example"},
		"redis://cache.example/3":           {addr: "cache.example:6379", host: "cache.example"},
		"redis://[::1]":                     {addr: "[::1]:6379", host: "::1"},
		"rediss://op_ana:x@cache.example:1": {addr: "cache.example:1", host: "cache.example", user: "op_ana", password: "x", useTLS: true},
	} {
		got, err := parseRedisDSN(dsn)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", dsn, got, err, want)
		}
	}
}
