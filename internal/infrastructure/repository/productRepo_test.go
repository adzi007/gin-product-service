package repository

import (
	"strings"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// numericToDecimal converts a pgtype.Numeric (as produced by pgNumeric) back
// to a shopspring decimal so test assertions read naturally.
func numericToDecimal(n pgtype.Numeric) decimal.Decimal {
	if !n.Valid {
		return decimal.Decimal{}
	}
	return decimal.NewFromBigInt(n.Int, n.Exp)
}

// mustHaveArgs asserts the built conditions reference exactly the expected
// bound arguments, guarding against placeholder/arg drift.
func assertConditionsArgs(t *testing.T, conditions []string, args []interface{}, argIdx int, want []string) {
	t.Helper()
	if len(conditions) != len(want) {
		t.Fatalf("expected %d conditions, got %d: %v", len(want), len(conditions), conditions)
	}
	if argIdx != len(args) {
		t.Fatalf("expected argIdx %d to match len(args) %d", argIdx, len(args))
	}
	for i, w := range want {
		if conditions[i] != w {
			t.Errorf("condition[%d] = %q, want %q", i, conditions[i], w)
		}
	}
}

func TestBuildListConditions_NoPriceFilters(t *testing.T) {
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{})

	assertConditionsArgs(t, conditions, args, argIdx, nil)

	// Regression: without price params the generated conditions are exactly
	// the pre-existing ones (search/category/status only).
	conditions, args, argIdx = buildListConditions(domain.ListProductParams{
		Search:     "Smart",
		CategoryID: 7,
		Status:     "draft",
	})
	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"(products.title ILIKE $1 OR products.handle ILIKE $1 OR category.name ILIKE $1)",
		"products.category_id = $2",
		"products.status = $3",
	})
	if args[0] != "%Smart%" {
		t.Errorf("expected search arg %%Smart%%, got %v", args[0])
	}
	if args[1] != 7 {
		t.Errorf("expected category arg 7, got %v", args[1])
	}
	if args[2] != "draft" {
		t.Errorf("expected status arg draft, got %v", args[2])
	}
}

func TestBuildListConditions_MinPriceOnly(t *testing.T) {
	minPrice := decimal.RequireFromString("100000")
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		MinPrice: &minPrice,
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"products.price_max >= $1",
	})
	if got := numericToDecimal(args[0].(pgtype.Numeric)); !got.Equal(minPrice) {
		t.Errorf("expected arg 100000, got %s", got)
	}
}

func TestBuildListConditions_MaxPriceOnly(t *testing.T) {
	maxPrice := decimal.RequireFromString("500000")
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		MaxPrice: &maxPrice,
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"products.price_min <= $1",
	})
	if got := numericToDecimal(args[0].(pgtype.Numeric)); !got.Equal(maxPrice) {
		t.Errorf("expected arg 500000, got %s", got)
	}
}

func TestBuildListConditions_BothPrices(t *testing.T) {
	minPrice := decimal.RequireFromString("100000")
	maxPrice := decimal.RequireFromString("500000")
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		MinPrice: &minPrice,
		MaxPrice: &maxPrice,
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"products.price_max >= $1",
		"products.price_min <= $2",
	})
	if got := numericToDecimal(args[0].(pgtype.Numeric)); !got.Equal(minPrice) {
		t.Errorf("expected args[0] 100000, got %s", got)
	}
	if got := numericToDecimal(args[1].(pgtype.Numeric)); !got.Equal(maxPrice) {
		t.Errorf("expected args[1] 500000, got %s", got)
	}
}

func TestBuildListConditions_DecimalPrecisionPreserved(t *testing.T) {
	minPrice := decimal.RequireFromString("99.99")
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		MinPrice: &minPrice,
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"products.price_max >= $1",
	})
	if got := numericToDecimal(args[0].(pgtype.Numeric)); !got.Equal(minPrice) {
		t.Errorf("expected arg 99.99, got %s", got)
	}
	if numericToDecimal(args[0].(pgtype.Numeric)).String() != "99.99" {
		t.Errorf("expected exact 99.99, got %s", numericToDecimal(args[0].(pgtype.Numeric)))
	}
}

func TestBuildListConditions_ExactBoundariesInclusive(t *testing.T) {
	// Exact boundary values must be included: the operators are >= and <=.
	exact := decimal.RequireFromString("100000")
	conditions, _, _ := buildListConditions(domain.ListProductParams{
		MinPrice: &exact,
		MaxPrice: &exact,
	})
	joined := strings.Join(conditions, " AND ")
	if !strings.Contains(joined, ">=") {
		t.Errorf("minPrice boundary must be inclusive (>=): %s", joined)
	}
	if !strings.Contains(joined, "<=") {
		t.Errorf("maxPrice boundary must be inclusive (<=): %s", joined)
	}
}

func TestBuildListConditions_CombinedFilters(t *testing.T) {
	minPrice := decimal.RequireFromString("100000")
	maxPrice := decimal.RequireFromString("500000")
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		Search:     "shirt",
		CategoryID: 3,
		Status:     "active",
		MinPrice:   &minPrice,
		MaxPrice:   &maxPrice,
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"(products.title ILIKE $1 OR products.handle ILIKE $1 OR category.name ILIKE $1)",
		"products.category_id = $2",
		"products.status = $3",
		"products.price_max >= $4",
		"products.price_min <= $5",
	})
	if got := numericToDecimal(args[3].(pgtype.Numeric)); !got.Equal(minPrice) {
		t.Errorf("expected args[3] 100000, got %s", got)
	}
	if got := numericToDecimal(args[4].(pgtype.Numeric)); !got.Equal(maxPrice) {
		t.Errorf("expected args[4] 500000, got %s", got)
	}
}

func TestBuildListConditions_RatingsFilter(t *testing.T) {
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		Ratings: []int{3},
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"ROUND(products.rating_avg) = ANY($1)",
	})
	if got, ok := args[0].([]int32); !ok || len(got) != 1 || got[0] != 3 {
		t.Errorf("expected ratings arg []int32{3}, got %#v", args[0])
	}
}

func TestBuildListConditions_MultipleRatings(t *testing.T) {
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		Ratings: []int{3, 4, 5},
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"ROUND(products.rating_avg) = ANY($1)",
	})
	got, ok := args[0].([]int32)
	if !ok || len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Errorf("expected ratings arg []int32{3,4,5}, got %#v", args[0])
	}
}

func TestBuildListConditions_NoRatingsProducesNoCondition(t *testing.T) {
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		Ratings: nil,
	})

	assertConditionsArgs(t, conditions, args, argIdx, nil)
}

func TestBuildListConditions_CombinedSearchAndRatings(t *testing.T) {
	conditions, args, argIdx := buildListConditions(domain.ListProductParams{
		Search:  "shirt",
		Ratings: []int{4, 5},
	})

	assertConditionsArgs(t, conditions, args, argIdx, []string{
		"(products.title ILIKE $1 OR products.handle ILIKE $1 OR category.name ILIKE $1)",
		"ROUND(products.rating_avg) = ANY($2)",
	})
}
