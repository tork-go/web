package tork_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

type CreateItemBody struct {
	tork.JSONBody
	Name  string   `json:"name"`
	Price int64    `json:"price"`
	Tags  []string `json:"tags"`
}

type UpdateItemBody struct {
	Name  string `json:"name"`
	Price int64  `json:"price"`
}

type UpdateItemInput struct {
	ItemID string         `path:"item_id"`
	DryRun bool           `query:"dryRun"`
	Token  string         `header:"X-Token"`
	Body   UpdateItemBody `body:"json"`
}

// post sends a body to a one-route application and returns the response.
func post(t *testing.T, app *tork.App, method, target, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)
	return rec
}

// A body struct embedding JSONBody is taken directly, with no wrapper.
func TestMarkedBodyIsTakenDirectly(t *testing.T) {
	var got CreateItemBody
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		got = body
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/json",
		`{"name":"Boots","price":4200,"tags":["new","sale"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Name != "Boots" || got.Price != 4200 {
		t.Errorf("got %+v", got)
	}
	if !equalStrings(got.Tags, []string{"new", "sale"}) {
		t.Errorf("Tags = %v", got.Tags)
	}
}

// The wrapper form binds the body beside path, query, and header parameters.
func TestBodyBesideParameters(t *testing.T) {
	var got UpdateItemInput
	app := newApp()
	app.PUT("/items/{item_id}", func(_ context.Context, in UpdateItemInput) (UpdateItemInput, error) {
		got = in
		return in, nil
	})

	request := httptest.NewRequest("PUT", "/items/42?dryRun=true",
		strings.NewReader(`{"name":"Boots","price":4200}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Token", "t0ken")

	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.ItemID != "42" || !got.DryRun || got.Token != "t0ken" {
		t.Errorf("parameters = %+v", got)
	}
	if got.Body.Name != "Boots" || got.Body.Price != 4200 {
		t.Errorf("body = %+v", got.Body)
	}
}

// A structured suffix is still JSON, and a client sending merge-patch should
// not be turned away over a spelling this API does not care about.
func TestJSONSuffixMediaTypesAreAccepted(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	for _, contentType := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/merge-patch+json",
		"", // no Content-Type at all
	} {
		rec := post(t, app, "POST", "/items", contentType, `{"name":"Boots"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("%q: status %d: %s", contentType, rec.Code, rec.Body)
		}
	}
}

func TestNonJSONBodyIsRefused(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "text/plain", `hello`)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "UNSUPPORTED_MEDIA_TYPE" {
		t.Errorf("code = %q", e.Code)
	}
}

func TestMalformedContentTypeIsRefused(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/", `{}`)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status %d", rec.Code)
	}
}

func TestBodyProblems(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	tests := []struct {
		name      string
		body      string
		wantField string
		wantIssue string
	}{
		{"empty", "", "body", tork.IssueBodyRequired},
		{"whitespace only", "   \n ", "body", tork.IssueBodyRequired},
		{"not json", "{oh no", "body", tork.IssueInvalidJSON},
		{"wrong type", `{"price":"free"}`, "price", tork.IssueInvalidType},
		{"wrong type in array", `{"tags":"new"}`, "tags", tork.IssueInvalidType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, app, "POST", "/items", "application/json", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			details := decodeFieldErrors(t, rec)
			if len(details) != 1 {
				t.Fatalf("details = %+v", details)
			}
			if details[0].Field != tt.wantField || details[0].Issue != tt.wantIssue {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

func TestWrongTypeNamesWhatItWanted(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/json", `{"price":"free"}`)
	details := decodeFieldErrors(t, rec)
	if details[0].Message != "price must be a whole number." {
		t.Errorf("message = %q", details[0].Message)
	}
}

// Unknown fields are ignored by default, which is how an API is extended
// without breaking the clients that have not caught up.
func TestUnknownBodyFieldsAreIgnoredByDefault(t *testing.T) {
	app := newApp()
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/json", `{"name":"Boots","colour":"brown"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestStrictBodiesRejectsUnknownFields(t *testing.T) {
	app := newApp(tork.StrictBodies())
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/json", `{"name":"Boots","colour":"brown"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if details[0].Field != "colour" || details[0].Issue != tork.IssueUnknownField {
		t.Errorf("field error = %+v", details[0])
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	app := newApp(tork.MaxBodyBytes(32))
	app.POST("/items", func(_ context.Context, body CreateItemBody) (CreateItemBody, error) {
		return body, nil
	})

	rec := post(t, app, "POST", "/items", "application/json",
		`{"name":"`+strings.Repeat("x", 200)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Code != "PAYLOAD_TOO_LARGE" {
		t.Errorf("code = %q", e.Code)
	}
}

// A rejected body and rejected parameters are reported together.
func TestBodyAndParameterProblemsAreJoined(t *testing.T) {
	app := newApp()
	app.PUT("/items/{item_id}", func(_ context.Context, in UpdateItemInput) (UpdateItemInput, error) {
		return in, nil
	})

	rec := post(t, app, "PUT", "/items/42?dryRun=maybe", "application/json", `{"price":"free"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if len(details) != 2 {
		t.Fatalf("details = %+v", details)
	}
	if details[0].Field != "query.dryRun" || details[1].Field != "price" {
		t.Errorf("fields = %s, %s", details[0].Field, details[1].Field)
	}
}

type LoginInput struct {
	Username string `form:"username"`
	Password string `form:"password"`
	Remember bool   `form:"remember"`
}

func TestURLEncodedForm(t *testing.T) {
	var got LoginInput
	app := newApp()
	app.POST("/login", func(_ context.Context, in LoginInput) (LoginInput, error) {
		got = in
		return in, nil
	})

	rec := post(t, app, "POST", "/login", "application/x-www-form-urlencoded",
		"username=ada&password=secret&remember=on")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Username != "ada" || got.Password != "secret" || !got.Remember {
		t.Errorf("got %+v", got)
	}
}

func TestFormRequiresAFormContentType(t *testing.T) {
	app := newApp()
	app.POST("/login", func(_ context.Context, in LoginInput) (LoginInput, error) {
		return in, nil
	})

	for _, contentType := range []string{"application/json", ""} {
		rec := post(t, app, "POST", "/login", contentType, "username=ada")
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%q: status %d", contentType, rec.Code)
		}
	}
}

type AvatarInput struct {
	UserID string                  `path:"user_id"`
	Note   string                  `form:"note"`
	Avatar *multipart.FileHeader   `form:"avatar"`
	Extras []*multipart.FileHeader `form:"extras"`
}

func TestMultipartFormAndFileUpload(t *testing.T) {
	var got AvatarInput
	app := newApp()
	app.POST("/users/{user_id}/avatar", func(_ context.Context, in AvatarInput) (string, error) {
		got = in
		return "ok", nil
	})

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("note", "my face"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	file, err := form.CreateFormFile("avatar", "face.png")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	file.Write([]byte("not really a png"))
	for _, name := range []string{"one.txt", "two.txt"} {
		extra, err := form.CreateFormFile("extras", name)
		if err != nil {
			t.Fatalf("create file: %v", err)
		}
		extra.Write([]byte(name))
	}
	form.Close()

	request := httptest.NewRequest("POST", "/users/7/avatar", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.UserID != "7" || got.Note != "my face" {
		t.Errorf("fields = %+v", got)
	}
	if got.Avatar == nil || got.Avatar.Filename != "face.png" {
		t.Fatalf("avatar = %+v", got.Avatar)
	}
	if len(got.Extras) != 2 || got.Extras[1].Filename != "two.txt" {
		t.Errorf("extras = %+v", got.Extras)
	}
}

// A file field nothing uploaded to is simply left nil.
func TestMissingUploadIsNotAnError(t *testing.T) {
	var got AvatarInput
	app := newApp()
	app.POST("/users/{user_id}/avatar", func(_ context.Context, in AvatarInput) (string, error) {
		got = in
		return "ok", nil
	})

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("note", "no picture")
	form.Close()

	request := httptest.NewRequest("POST", "/users/7/avatar", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Avatar != nil || got.Extras != nil {
		t.Errorf("uploads = %+v %+v", got.Avatar, got.Extras)
	}
}

func TestMalformedMultipartIsRefused(t *testing.T) {
	app := newApp()
	app.POST("/users/{user_id}/avatar", func(_ context.Context, in AvatarInput) (string, error) {
		return "ok", nil
	})

	rec := post(t, app, "POST", "/users/7/avatar", "multipart/form-data; boundary=xyz", "not a multipart body")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestOversizedFormIsRefused(t *testing.T) {
	app := newApp(tork.MaxBodyBytes(16))
	app.POST("/login", func(_ context.Context, in LoginInput) (LoginInput, error) {
		return in, nil
	})

	rec := post(t, app, "POST", "/login", "application/x-www-form-urlencoded",
		"username="+strings.Repeat("a", 200))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// The raw request is still reachable for whatever the framework does not model.
func TestRawRequestEscapeHatch(t *testing.T) {
	app := newApp()
	app.POST("/raw", func(_ context.Context, r *tork.Request) (string, error) {
		body := make([]byte, 4)
		r.HTTP().Body.Read(body)
		return string(body) + "|" + r.PathValue("nothing") + "|" + r.Context().Value(ctxKey{}).(string), nil
	}, tork.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, "ctx")))
		})
	}))

	rec := post(t, app, "POST", "/raw", "text/plain", "abcd")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec.Body.String() != `"abcd||ctx"` {
		t.Errorf("body = %s", rec.Body)
	}
}

type ctxKey struct{}

func TestRequestWriterIsReachable(t *testing.T) {
	app := newApp()
	app.GET("/raw", func(_ context.Context, r *tork.Request) (string, error) {
		r.Writer().Header().Set("X-Set-Directly", "yes")
		return "ok", nil
	})

	rec := do(t, app, "GET", "/raw", nil)
	if rec.Header().Get("X-Set-Directly") != "yes" {
		t.Errorf("header = %q", rec.Header().Get("X-Set-Directly"))
	}
}
