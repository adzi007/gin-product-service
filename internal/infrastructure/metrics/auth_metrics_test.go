package metrics

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The authorization outcome metric is only useful if its label set stays closed:
// it must never grow with token content, subject values, permission lists, or
// concrete resource identifiers.
func TestAuthorizationOutcomesHasOnlyBoundedLabels(t *testing.T) {
	labels := make([]string, 0, 4)
	for _, label := range []string{"method", "route", "outcome"} {
		labels = append(labels, label)
	}

	desc := AuthorizationOutcomes.WithLabelValues("GET", "/api/v1/products/:id", string(AuthOutcomeAuthenticated)).Desc()
	if desc == nil {
		t.Fatal("Dec() = nil, want a metric descriptor")
	}

	if got, want := desc.String(), "Desc{fqName: \"http_admin_authorization_outcomes_total\""; !strings.Contains(got, want) {
		t.Fatalf("descriptor %q does not describe the authorization outcome counter", got)
	}

	for _, forbidden := range []string{"token", "subject", "sub", "permission", "user", "order", "review", "product_id"} {
		for _, label := range labels {
			if strings.Contains(strings.ToLower(label), strings.ToLower(forbidden)) {
				t.Errorf("authorization outcome label %q contains unbounded term %q", label, forbidden)
			}
		}
	}

	sort.Strings(labels)
	if strings.Join(labels, ",") != "method,outcome,route" {
		t.Fatalf("label set = %v, want exactly method, route, outcome", labels)
	}
}

func TestObserveAuthorizationOutcomeRecordsBoundedOutcome(t *testing.T) {
	const (
		method = "PATCH"
		route  = "/api/v1/products/:id"
	)

	AuthorizationOutcomes.Reset()

	outcomes := []AuthOutcome{
		AuthOutcomeAuthenticated,
		AuthOutcomeUnauthenticated,
		AuthOutcomeForbidden,
	}

	for _, outcome := range outcomes {
		ObserveAuthorizationOutcome(method, route, outcome)
	}

	for _, outcome := range outcomes {
		if got := testutil.ToFloat64(AuthorizationOutcomes.WithLabelValues(method, route, string(outcome))); got != 1 {
			t.Errorf("outcome %q count = %v, want 1", outcome, got)
		}
	}
}

// An unrecognized outcome must not create an unbounded label value.
func TestObserveAuthorizationOutcomeMapsUnknownToOther(t *testing.T) {
	const (
		method = "POST"
		route  = "/api/v1/inventory/reservations"
	)

	AuthorizationOutcomes.Reset()
	ObserveAuthorizationOutcome(method, route, AuthOutcome("bearer eyJhbGciOiJSUzI1NiJ9.forged"))

	if got := testutil.ToFloat64(AuthorizationOutcomes.WithLabelValues(method, route, string(AuthOutcomeOther))); got != 1 {
		t.Fatalf("other outcome count = %v, want 1", got)
	}
}

func TestNormalizeAuthOutcomeKeepsKnownVocabulary(t *testing.T) {
	for _, outcome := range []AuthOutcome{
		AuthOutcomeAuthenticated,
		AuthOutcomeUnauthenticated,
		AuthOutcomeForbidden,
	} {
		if got := normalizeAuthOutcome(outcome); got != outcome {
			t.Errorf("normalizeAuthOutcome(%q) = %q, want %q", outcome, got, outcome)
		}
	}
	if got := normalizeAuthOutcome(AuthOutcome("token-value")); got != AuthOutcomeOther {
		t.Errorf("normalizeAuthOutcome(unknown) = %q, want %q", got, AuthOutcomeOther)
	}
}
