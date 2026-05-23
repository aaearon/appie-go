package appie

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bonusTestServer builds an httptest server that serves a fixed list of
// categories for /v3/metadata and dispatches /v2/section calls to per-category
// handlers. Categories without a handler 200-OK with an empty section.
func bonusTestServer(t *testing.T, categories []string, sections map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mobile-services/bonuspage/v3/metadata", func(w http.ResponseWriter, r *http.Request) {
		meta := bonusMetadataResponse{}
		var period struct {
			BonusStartDate string `json:"bonusStartDate"`
			BonusEndDate   string `json:"bonusEndDate"`
			Tabs           []struct {
				Description     string `json:"description"`
				URLMetadataList []struct {
					URL         string `json:"url"`
					Count       int    `json:"count"`
					BonusType   string `json:"bonusType"`
					Description string `json:"description"`
				} `json:"urlMetadataList"`
			} `json:"tabs"`
		}
		period.BonusStartDate = "2026-05-21"
		period.BonusEndDate = "2026-05-27"
		tab := struct {
			Description     string `json:"description"`
			URLMetadataList []struct {
				URL         string `json:"url"`
				Count       int    `json:"count"`
				BonusType   string `json:"bonusType"`
				Description string `json:"description"`
			} `json:"urlMetadataList"`
		}{Description: "all"}
		for _, c := range categories {
			tab.URLMetadataList = append(tab.URLMetadataList, struct {
				URL         string `json:"url"`
				Count       int    `json:"count"`
				BonusType   string `json:"bonusType"`
				Description string `json:"description"`
			}{BonusType: "NATIONAL", Description: c})
		}
		period.Tabs = []struct {
			Description     string `json:"description"`
			URLMetadataList []struct {
				URL         string `json:"url"`
				Count       int    `json:"count"`
				BonusType   string `json:"bonusType"`
				Description string `json:"description"`
			} `json:"urlMetadataList"`
		}{tab}
		meta.Periods = append(meta.Periods, period)
		_ = json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("/mobile-services/bonuspage/v2/section", func(w http.ResponseWriter, r *http.Request) {
		category := r.URL.Query().Get("category")
		if h, ok := sections[category]; ok {
			h(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(bonusSectionResponse{})
	})
	return httptest.NewServer(mux)
}

// assertDateQueryParam wraps a handler to verify that every /v2/section
// request carries the expected date query parameter. Lets a single test
// confirm that the date plumbing in GetBonusProducts actually reaches the
// underlying HTTP call.
func assertDateQueryParam(t *testing.T, want string, inner http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("date"); got != want {
			t.Errorf("expected date=%q on /v2/section, got %q", want, got)
		}
		inner(w, r)
	}
}

func TestGetBonusProductsPartialSuccess(t *testing.T) {
	const wantDate = "2026-05-22"
	sections := map[string]http.HandlerFunc{
		"zuivel": assertDateQueryParam(t, wantDate, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(bonusSectionResponse{
				BonusGroupOrProducts: []struct {
					Product    *productResponse    `json:"product,omitempty"`
					BonusGroup *bonusGroupResponse `json:"bonusGroup,omitempty"`
				}{
					{Product: &productResponse{WebshopID: 1, Title: "Melk", IsBonus: true}},
					{Product: &productResponse{WebshopID: 2, Title: "Kaas", IsBonus: true}},
				},
			})
		}),
		"vlees": assertDateQueryParam(t, wantDate, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusFailedDependency)
		}),
		"brood": assertDateQueryParam(t, wantDate, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(bonusSectionResponse{
				BonusGroupOrProducts: []struct {
					Product    *productResponse    `json:"product,omitempty"`
					BonusGroup *bonusGroupResponse `json:"bonusGroup,omitempty"`
				}{
					{Product: &productResponse{WebshopID: 3, Title: "Brood", IsBonus: true}},
				},
			})
		}),
	}
	server := bonusTestServer(t, []string{"zuivel", "vlees", "brood"}, sections)
	defer server.Close()

	client := New(WithBaseURL(server.URL))
	products, failures, err := client.GetBonusProducts(context.Background(), wantDate)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(products) != 3 {
		t.Fatalf("expected 3 products from the 2 healthy categories, got %d", len(products))
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 category failure, got %d", len(failures))
	}
	if failures[0].Category != "vlees" {
		t.Errorf("expected failure on 'vlees', got %q", failures[0].Category)
	}
	if !strings.Contains(failures[0].Error(), "vlees") {
		t.Errorf("CategoryError.Error() should include the category name, got %q", failures[0].Error())
	}
	// CategoryError is the single source of per-category annotation; the
	// underlying getBonusSection error must not also embed the category.
	if strings.Count(failures[0].Error(), "vlees") > 1 {
		t.Errorf("CategoryError.Error() duplicates the category name, got %q", failures[0].Error())
	}
}

func TestGetBonusProductsMetadataFailureHardErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(WithBaseURL(server.URL))
	products, failures, err := client.GetBonusProducts(context.Background(), "")
	if err == nil {
		t.Fatal("expected top-level error when metadata lookup fails")
	}
	if products != nil || failures != nil {
		t.Errorf("expected nil products + nil failures on metadata error, got %v / %v", products, failures)
	}
}

func TestGetBonusProductsDedupesByIDAndTitle(t *testing.T) {
	// Same (ID, Title) returned by two categories must collapse to one
	// product, while a different Title with the same ID==0 (group case)
	// must stay distinct.
	dup := &productResponse{WebshopID: 42, Title: "Hak Appelmoes", IsBonus: true}
	group1 := &bonusGroupResponse{ID: "g1", SegmentDescription: "Alle Hak"}
	group2 := &bonusGroupResponse{ID: "g2", SegmentDescription: "Alle Knorr"}
	sections := map[string]http.HandlerFunc{
		"a": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(bonusSectionResponse{
				BonusGroupOrProducts: []struct {
					Product    *productResponse    `json:"product,omitempty"`
					BonusGroup *bonusGroupResponse `json:"bonusGroup,omitempty"`
				}{
					{Product: dup},
					{BonusGroup: group1},
				},
			})
		},
		"b": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(bonusSectionResponse{
				BonusGroupOrProducts: []struct {
					Product    *productResponse    `json:"product,omitempty"`
					BonusGroup *bonusGroupResponse `json:"bonusGroup,omitempty"`
				}{
					{Product: dup},
					{BonusGroup: group2},
				},
			})
		},
	}
	server := bonusTestServer(t, []string{"a", "b"}, sections)
	defer server.Close()

	client := New(WithBaseURL(server.URL))
	products, failures, err := client.GetBonusProducts(context.Background(), "2026-05-22")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if len(products) != 3 {
		t.Fatalf("expected 3 products (1 deduped + 2 distinct groups), got %d:\n%+v", len(products), products)
	}
}

func TestCategoryErrorUnwrap(t *testing.T) {
	inner := errors.New("upstream 424")
	ce := CategoryError{Category: "vlees", Err: inner}
	if !errors.Is(ce, inner) {
		t.Errorf("errors.Is should match wrapped error")
	}
}
