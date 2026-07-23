package tork_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/tork-go/web"
)

// The benchmark exercises everything a request pays for: a path parameter, a
// query parameter, a JSON body, a singleton dependency, and a request-scoped
// one. It is measured against the same work written by hand on net/http, which
// is the number that keeps the "no per-request reflection" claim honest.

type BenchInput struct {
	ItemID string `path:"item_id"`
	Limit  int    `query:"limit"`
}

type BenchBody struct {
	tork.JSONBody
	Name string `json:"name"`
}

type BenchService struct{ prefix string }

type BenchPrincipal struct{ name string }

type BenchResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func benchHandler(_ context.Context, in BenchInput, body BenchBody, svc *BenchService, p BenchPrincipal) (BenchResult, error) {
	return BenchResult{ID: svc.prefix + in.ItemID, Name: body.Name + p.name}, nil
}

func benchAuthenticate(context.Context) (BenchPrincipal, error) {
	return BenchPrincipal{name: "bench"}, nil
}

func benchRequest() *http.Request {
	req := httptest.NewRequest("POST", "/items/42?limit=10", bytes.NewReader([]byte(`{"name":"widget"}`)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func BenchmarkInjectedHandler(b *testing.B) {
	app := tork.New(
		tork.Provide(func() *BenchService { return &BenchService{prefix: "item-"} }),
		tork.Depends(benchAuthenticate),
	)
	app.POST("/items/{item_id}", benchHandler)

	handler, err := app.Handler()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, benchRequest())
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkBareNetHTTP(b *testing.B) {
	svc := &BenchService{prefix: "item-"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items/{item_id}", func(w http.ResponseWriter, r *http.Request) {
		itemID := r.PathValue("item_id")
		if _, err := strconv.Atoi(r.URL.Query().Get("limit")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body BenchBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		principal := BenchPrincipal{name: "bench"}
		result := BenchResult{ID: svc.prefix + itemID, Name: body.Name + principal.name}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, benchRequest())
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
