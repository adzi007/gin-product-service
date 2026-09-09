package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// errCoordination is an opaque internal sentinel for REST transport/protocol
// failures. Callers never see it directly; it is translated to a stable domain
// error so no Redis credentials or endpoint URLs leak.
var errCoordination = errors.New("redis coordination error")

// leaseTTLMillis is the fixed coordination lease TTL. PostgreSQL row locks and
// re-checks remain the durable correctness boundary if a lease expires.
const leaseTTLMillis = "5000"

// acquireScript atomically verifies that every canonical key is absent and, if
// so, assigns all of them the same owner token with the fixed PX TTL. It
// returns 1 on success and 0 if any key is already held, so a lease is never
// partially acquired.
const acquireScript = `
local token = ARGV[1]
for i = 1, #KEYS do
  if redis.call('EXISTS', KEYS[i]) == 1 then
    return 0
  end
end
for i = 1, #KEYS do
  redis.call('SET', KEYS[i], token, 'PX', ARGV[2])
end
return 1
`

// releaseScript removes only keys owned by this request's token, so a stale or
// re-acquired lease is never released by the wrong owner.
const releaseScript = `
local token = ARGV[1]
local released = 0
for i = 1, #KEYS do
  if redis.call('GET', KEYS[i]) == token then
    redis.call('DEL', KEYS[i])
    released = released + 1
  end
end
return released
`

// reservationLocker implements domain.ReservationLocker against the Upstash
// Redis REST API using only the standard library HTTP client. The token is
// sent exclusively as an Authorization header and never logged.
type reservationLocker struct {
	baseURL string
	token   string
	client  *http.Client
}

// failClosedLocker is used when coordination configuration is missing or
// unusable: every acquire fails closed without changing inventory.
type failClosedLocker struct{}

// NewReservationLocker constructs the REST lease adapter. When the base URL or
// token is empty it returns a fail-closed locker so unrelated endpoints can
// still start while the reservation endpoint degrades safely.
func NewReservationLocker(baseURL, token string) domain.ReservationLocker {
	if baseURL == "" || token == "" {
		return &failClosedLocker{}
	}
	return &reservationLocker{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

var (
	_ domain.ReservationLocker = (*reservationLocker)(nil)
	_ domain.ReservationLocker = (*failClosedLocker)(nil)
)

func (l *reservationLocker) Acquire(ctx context.Context, keys []string) (string, error) {
	owner := uuid.NewString()
	body, err := evalCommand(acquireScript, keys, owner, leaseTTLMillis)
	if err != nil {
		return "", domain.ErrReservationCoordinationUnavailable
	}

	result, err := l.do(ctx, body)
	if err != nil {
		return "", domain.ErrReservationCoordinationUnavailable
	}
	if result != 1 {
		// Contention: at least one canonical key is already held.
		return "", domain.ErrReservationCoordinationUnavailable
	}
	return owner, nil
}

func (l *reservationLocker) Release(ctx context.Context, keys []string, token string) error {
	body, err := evalCommand(releaseScript, keys, token)
	if err != nil {
		return err
	}
	_, err = l.do(ctx, body)
	return err
}

// do performs one REST EVAL round-trip. Errors are intentionally opaque so
// credentials and the Redis endpoint never leak into logs or responses.
func (l *reservationLocker) do(ctx context.Context, body []byte) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.token)

	resp, err := l.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}

	var parsed restResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0, err
	}
	if parsed.Error != "" {
		return 0, errCoordination
	}
	return parseResult(parsed.Result)
}

// restResponse is the Upstash REST envelope.
type restResponse struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// parseResult accepts the integer result Upstash returns for EVAL, tolerating
// numeric, string, and null encodings.
func parseResult(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errCoordination
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strconv.ParseInt(s, 10, 64)
	}
	return 0, errCoordination
}

// evalCommand builds the Upstash REST command array for an EVAL call.
func evalCommand(script string, keys []string, args ...string) ([]byte, error) {
	cmd := make([]interface{}, 0, 3+len(keys)+len(args))
	cmd = append(cmd, "EVAL", script, strconv.Itoa(len(keys)))
	for _, k := range keys {
		cmd = append(cmd, k)
	}
	for _, a := range args {
		cmd = append(cmd, a)
	}
	return json.Marshal(cmd)
}

func (failClosedLocker) Acquire(_ context.Context, _ []string) (string, error) {
	return "", domain.ErrReservationCoordinationUnavailable
}

func (failClosedLocker) Release(_ context.Context, _ []string, _ string) error {
	return nil
}
