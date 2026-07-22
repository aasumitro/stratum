package connect

import "regexp"

// credentialPattern matches a userinfo password in a connection-string URL
// (postgres://, amqp://, redis://, ...): scheme://user:PASSWORD@host.
var credentialPattern = regexp.MustCompile(`://([^:/\s@]+):[^@/\s]*@`)

// RedactDSN masks a userinfo password that connection-library errors
// (pgx, pgxpool, amqp091-go, net/url) often embed verbatim in their error
// message, before that message reaches the Wails frontend.
func RedactDSN(msg string) string {
	return credentialPattern.ReplaceAllString(msg, "://$1:***@")
}
