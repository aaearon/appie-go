package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appie "github.com/gwillem/appie-go"
)

// useFakeClient swaps clientFactory to point at the given httptest server.
func useFakeClient(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := clientFactory
	clientFactory = func() (*appie.Client, error) {
		return appie.New(
			appie.WithBaseURL(srv.URL),
			appie.WithTokens("fake-access", "fake-refresh"),
		), nil
	}
	t.Cleanup(func() { clientFactory = old })
}

// jsonMode sets globalOpts.JSON for the duration of the test.
func jsonMode(t *testing.T) {
	t.Helper()
	old := globalOpts.JSON
	globalOpts.JSON = true
	t.Cleanup(func() { globalOpts.JSON = old })
}

func decodeEnv(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	return env
}

func TestSearchJSONEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"products": []map[string]any{
				{
					"webshopId":        12345,
					"title":            "AH Volle Melk",
					"brand":            "AH",
					"mainCategory":     "Zuivel",
					"salesUnitSize":    "1 L",
					"currentPrice":     1.25,
					"priceBeforeBonus": 1.25,
					"isOrderable":      true,
				},
			},
		})
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &searchCommand{Limit: 5}
	cmd.Args.Query = "melk"

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	data, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("data not an array: %v", env["data"])
	}
	if len(data) != 1 {
		t.Fatalf("len(data)=%d want 1", len(data))
	}
	first := data[0].(map[string]any)
	if int(first["id"].(float64)) != 12345 {
		t.Errorf("id=%v want 12345", first["id"])
	}
}

func TestProductJSONEnvelopeWithMissingIDWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Endpoint returns a flat array of productResponse JSON.
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"webshopId":        12345,
				"title":            "AH Banaan",
				"brand":            "AH",
				"mainCategory":     "Fruit",
				"salesUnitSize":    "1 kg",
				"currentPrice":     1.99,
				"priceBeforeBonus": 1.99,
				"isOrderable":      true,
			},
		})
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &productCommand{}
	cmd.Args.IDs = []int{12345, 99999999}

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	warns, _ := env["warnings"].([]any)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d (%v)", len(warns), warns)
	}
	if !strings.Contains(warns[0].(string), "99999999") {
		t.Errorf("warning should mention missing id: %v", warns[0])
	}
}

func TestKoopjesJSONEnvelope(t *testing.T) {
	// Both stores search and bargains use GraphQL; the library posts to
	// /graphql for both, and the response shape is distinguished by the
	// "operationName" / query body. We pick the response based on the call
	// order: first call -> stores, second -> bargains.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"storesSearch": map[string]any{
						"result": []map[string]any{
							{
								"id":        1234,
								"name":      "AH Amsterdam",
								"storeType": "REGULAR",
								"address": map[string]any{
									"street":      "Kalverstraat",
									"houseNumber": "1",
									"city":        "Amsterdam",
									"postalCode":  "1000AA",
								},
							},
						},
					},
				},
			})
			return
		}
		// bargains
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"bargainItems": []map[string]any{},
			},
		})
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &koopjesCommand{}
	cmd.Args.PostalCode = "1000AA"

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not an object: %v", env["data"])
	}
	if _, ok := data["store"]; !ok {
		t.Errorf("data.store missing: %v", data)
	}
	if _, ok := data["bargains"]; !ok {
		t.Errorf("data.bargains missing: %v", data)
	}
}

func TestListShowJSONEnvelope(t *testing.T) {
	// list show fetches the lists (REST), then items (GraphQL), then enriches
	// items with product details (REST batch). Three call types; we route by
	// path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/mobile-services/lists/v3/lists":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "list-uuid-1", "description": "Boodschappen", "itemCount": 1},
			})
		case strings.Contains(r.URL.Path, "/product/search/v2/products"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"webshopId":        12345,
					"title":            "AH Banaan",
					"brand":            "AH",
					"mainCategory":     "Fruit",
					"salesUnitSize":    "1 kg",
					"currentPrice":     1.99,
					"priceBeforeBonus": 1.99,
					"isOrderable":      true,
				},
			})
		case r.URL.Path == "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"favoriteListV2": []map[string]any{
						{
							"id":          "list-uuid-1",
							"description": "Boodschappen",
							"totalSize":   1,
							"items": []map[string]any{
								{"id": "item-1", "productId": 12345, "quantity": 2},
							},
						},
					},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &shoppingListShowCommand{}
	cmd.Args.ListID = "list-uuid-1"

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not an object: %v", env["data"])
	}
	if _, ok := data["list"]; !ok {
		t.Errorf("data.list missing: %v", data)
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("data.items wrong: %v", data["items"])
	}
	first := items[0].(map[string]any)
	if int(first["productId"].(float64)) != 12345 {
		t.Errorf("productId=%v want 12345", first["productId"])
	}
	if _, ok := first["product"]; !ok {
		t.Errorf("enriched product missing: %v", first)
	}
}

func TestOrderShowJSONEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			// Fulfillments query.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"orderFulfillments": map[string]any{
						"result": []map[string]any{
							{
								"orderId":           229775812,
								"statusCode":        1,
								"statusDescription": "Submitted",
								"totalPrice": map[string]any{
									"totalPrice": map[string]any{"amount": 42.50},
								},
								"delivery": map[string]any{
									"slot": map[string]any{
										"dateDisplay": "Tue 9 Dec",
										"timeDisplay": "18:00 - 20:00",
									},
								},
							},
						},
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/mobile-services/order/v1/229775812/details-grouped"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"orderId":      229775812,
				"deliveryDate": "2025-12-09",
				"orderState":   "DELIVERED",
				"groupedProductsInTaxonomy": []map[string]any{
					{
						"taxonomyName": "Zuivel",
						"orderedProducts": []map[string]any{
							{
								"amount":   1,
								"quantity": 1,
								"product": map[string]any{
									"webshopId":        12345,
									"title":            "AH Melk",
									"brand":            "AH",
									"salesUnitSize":    "1 L",
									"priceBeforeBonus": 1.25,
								},
							},
						},
					},
				},
			})
		default:
			// GetOrder summary call — let it 404, command tolerates failure
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &orderShowCommand{}
	cmd.Args.OrderID = 229775812

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not an object: %v", env["data"])
	}
	if _, ok := data["order"]; !ok {
		t.Errorf("data.order missing: %v", data)
	}
	if _, ok := data["fulfillment"]; !ok {
		t.Errorf("data.fulfillment missing: %v", data)
	}
}

func TestReceiptShowJSONEnvelopeCopiesDate(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// First GraphQL call: receipt list (provides Date metadata)
		// Second GraphQL call: receipt details (omits Date)
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"posReceiptsPage": map[string]any{
						"posReceipts": []map[string]any{
							{
								"id":          "txn-001",
								"dateTime":    "2025-01-15T14:30:00",
								"totalAmount": map[string]any{"amount": 42.50},
							},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"posReceiptDetails": map[string]any{
					"id":        "txn-001",
					"products":  []any{},
					"discounts": []any{},
					"payments":  []any{},
				},
			},
		})
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &receiptShowCommand{}
	cmd.Args.TransactionID = "txn-001"

	out := captureStdout(t, func() {
		if err := cmd.Execute(nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	env := decodeEnv(t, out)
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not an object: %v", env["data"])
	}
	if data["date"] != "2025-01-15T14:30:00" {
		t.Errorf("expected Date copied from list meta; got %v", data["date"])
	}
}

func TestSearchNotFoundJSONEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"products": []any{}})
	}))
	defer srv.Close()

	jsonMode(t)
	useFakeClient(t, srv)

	cmd := &searchCommand{Limit: 5}
	cmd.Args.Query = "nope"

	err := cmd.Execute(nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	code, exit := errorCode(err)
	if code != "not_found" || exit != exitNotFound {
		t.Errorf("got (%q,%d) want (not_found, %d)", code, exit, exitNotFound)
	}
}
