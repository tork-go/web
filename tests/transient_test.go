package tork_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tork-go/web"
)

// Ticket is a value that must not be shared: each injection gets its own,
// numbered so a test can tell two apart.
type Ticket struct{ id int }

// Each handler parameter of a transient type is built fresh, so two of them in
// one handler are two different values.
func TestTransientIsFreshAtEachInjection(t *testing.T) {
	var next int32
	newTicket := func() *Ticket { return &Ticket{id: int(atomic.AddInt32(&next, 1))} }

	app := newApp(tork.Provide(tork.Transient(newTicket)))
	app.GET("/", func(_ context.Context, a, b *Ticket) (greeting, error) {
		if a.id == b.id {
			return greeting{Message: "same"}, nil
		}
		return greeting{Message: "distinct"}, nil
	})

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"distinct"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A transient is built from the singletons in the graph.
type Clock struct{ base string }
type Stamp struct{ from string }

func TestTransientBuiltFromASingleton(t *testing.T) {
	newClock := func() *Clock { return &Clock{base: "clk"} }
	newStamp := func(c *Clock) *Stamp { return &Stamp{from: c.base} }

	app := newApp(tork.Provide(newClock, tork.Transient(newStamp)))
	app.GET("/", func(_ context.Context, s *Stamp) (greeting, error) {
		return greeting{Message: s.from}, nil
	})

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"clk"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A transient may itself be built from another transient.
type Inner struct{ n int }
type Outer struct{ inner *Inner }

func TestTransientBuiltFromATransient(t *testing.T) {
	newInner := func() *Inner { return &Inner{n: 7} }
	newOuter := func(i *Inner) *Outer { return &Outer{inner: i} }

	app := newApp(tork.Provide(tork.Transient(newInner), tork.Transient(newOuter)))
	app.GET("/", func(_ context.Context, o *Outer) (greeting, error) {
		return greeting{Message: itoa(o.inner.n)}, nil
	})

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"7"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A transient constructor that fails at request time stops that request; there
// is no boot to fail, since a transient is built on demand.
func TestTransientConstructorErrorFailsTheRequest(t *testing.T) {
	newFailing := func() (*Ticket, error) { return nil, errors.New("no ticket left") }

	app := newApp(tork.Provide(tork.Transient(newFailing)))
	app.GET("/", func(_ context.Context, tk *Ticket) (greeting, error) {
		return greeting{Message: itoa(tk.id)}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q", e.Code)
	}
}

// When a transient is built from another transient that fails, the failure
// stops the request just as a direct one does.
func TestTransientDependencyErrorFailsTheRequest(t *testing.T) {
	newInner := func() (*Inner, error) { return nil, errors.New("no inner") }
	newOuter := func(i *Inner) *Outer { return &Outer{inner: i} }

	app := newApp(tork.Provide(tork.Transient(newInner), tork.Transient(newOuter)))
	app.GET("/", func(_ context.Context, o *Outer) (greeting, error) {
		return greeting{}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

// A transient cannot register a cleanup: its lifetime is the injection, so
// there is no shutdown for one to run at.
func TestTransientCannotRegisterACleanup(t *testing.T) {
	newLeaky := func() (*Ticket, tork.Cleanup, error) {
		return &Ticket{}, func(context.Context) error { return nil }, nil
	}

	app := newApp(tork.Provide(tork.Transient(newLeaky)))
	app.GET("/", func(_ context.Context, tk *Ticket) (greeting, error) {
		return greeting{}, nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "a transient provider cannot register a tork.Cleanup") {
		t.Errorf("error = %q", msg)
	}
}

// itoa keeps these tests from importing strconv for one call each.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
