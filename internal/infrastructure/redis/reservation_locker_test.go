package redis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gin-product-service/internal/domain"
)

// commandArg captures the command array sent to the fake Upstash endpoint.
func decodeCommand(t *testing.T, r *http.Request) []interface{} {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var cmd []interface{}
	if err := json.Unmarshal(body, &cmd); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	return cmd
}

func writeResult(w http.ResponseWriter, result interface{}) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result})
}

func TestReservationLocker_Acquire_SendsFixedFiveSecondTTL(t *testing.T) {
	var gotKeys, gotArgs []string
	var gotScript string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}
		cmd := decodeCommand(t, r)
		if cmd[0] != "EVAL" {
			t.Fatalf("command = %v, want EVAL", cmd[0])
		}
		gotScript = cmd[1].(string)
		numKeys, err := strconv.Atoi(cmd[2].(string))
		if err != nil {
			t.Fatalf("numkeys %v: %v", cmd[2], err)
		}
		for _, k := range cmd[3 : 3+numKeys] {
			gotKeys = append(gotKeys, k.(string))
		}
		for _, a := range cmd[3+numKeys:] {
			gotArgs = append(gotArgs, a.(string))
		}
		writeResult(w, 1)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	_, err := locker.Acquire(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	if !strings.Contains(gotScript, "PX") {
		t.Errorf("acquire script must set a PX TTL: %s", gotScript)
	}
	if len(gotArgs) != 2 || gotArgs[1] != "5000" {
		t.Errorf("args = %v, want [owner, 5000] with fixed five-second TTL", gotArgs)
	}
	if len(gotKeys) != 2 || gotKeys[0] != "a" || gotKeys[1] != "b" {
		t.Errorf("keys = %v, want [a b]", gotKeys)
	}
}

func TestReservationLocker_Acquire_ReturnsOwnerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	token, err := locker.Acquire(context.Background(), []string{"k"})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty owner token")
	}
}

func TestReservationLocker_Acquire_ContentionFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 0)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	_, err := locker.Acquire(context.Background(), []string{"k"})
	if err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_Release_IsTokenChecked(t *testing.T) {
	var gotKeys, gotArgs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := decodeCommand(t, r)
		numKeys, err := strconv.Atoi(cmd[2].(string))
		if err != nil {
			t.Fatalf("numkeys %v: %v", cmd[2], err)
		}
		for _, k := range cmd[3 : 3+numKeys] {
			gotKeys = append(gotKeys, k.(string))
		}
		for _, a := range cmd[3+numKeys:] {
			gotArgs = append(gotArgs, a.(string))
		}
		writeResult(w, 2)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	if err := locker.Release(context.Background(), []string{"a", "b"}, "owner-1"); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	if len(gotKeys) != 2 || gotKeys[0] != "a" || gotKeys[1] != "b" {
		t.Errorf("release keys = %v, want [a b]", gotKeys)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "owner-1" {
		t.Errorf("release args = %v, want [owner-1]", gotArgs)
	}
}

func TestReservationLocker_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_TransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	locker := NewReservationLocker(srv.URL, "test-token")
	srv.Close() // force a transport failure

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_MissingConfigFailsClosed(t *testing.T) {
	locker := NewReservationLocker("", "")

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
	if err := locker.Release(context.Background(), []string{"k"}, "owner"); err != nil {
		t.Fatalf("release should be a no-op for fail-closed locker, got %v", err)
	}
}
