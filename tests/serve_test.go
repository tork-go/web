package tork_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// listenAddress reserves a port and gives it straight back, so a test can name
// an address it is confident nothing else holds.
func listenAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return address
}

func TestServeAnswersRequestsAndStopsWhenTold(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	address := listenAddress(t)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, address) }()

	body := getWhenReady(t, "http://"+address+"/")
	if body != `{"message":"hello"}` {
		t.Errorf("body = %s", body)
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

func TestServeReportsADeclarationMistakeRatherThanListening(t *testing.T) {
	app := newApp()
	app.GET("no-slash", hello)

	err := app.Serve(context.Background(), listenAddress(t))
	if err == nil || !strings.Contains(err.Error(), "must begin with a slash") {
		t.Errorf("Serve = %v", err)
	}
}

func TestServeReportsAnAddressItCannotListenOn(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	if err := app.Serve(context.Background(), "127.0.0.1:99999"); err == nil {
		t.Error("expected an error for an impossible port")
	}
}

// A listener that fails underneath the server is the one thing that ends
// serving without anyone asking, and it has to be reported rather than
// mistaken for a clean shutdown.
func TestServeListenerReportsAFailingListener(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- app.ServeListener(context.Background(), listener) }()

	getWhenReady(t, "http://"+listener.Addr().String()+"/")
	listener.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a listener that closed underneath the server should be reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener did not return")
	}
}

// The caller opened the listener, so it must be closed even when the
// application turns out not to build.
func TestServeListenerClosesTheListenerItCannotUse(t *testing.T) {
	app := newApp()
	app.GET("no-slash", hello)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()

	if err := app.ServeListener(context.Background(), listener); err == nil {
		t.Fatal("expected the build to fail")
	}

	// The port is free again only if the listener was closed.
	reopened, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("the listener was left open: %v", err)
	}
	reopened.Close()
}

// Run is Serve with the signals wired up, so the one thing worth asserting
// about it separately is that it reports what Serve reports.
func TestRunReportsAFailureToListen(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	if err := app.Run("127.0.0.1:99999"); err == nil {
		t.Error("expected an error for an impossible port")
	}
}

// getWhenReady polls until the server is accepting, so the test does not race
// the goroutine that started it.
func getWhenReady(t *testing.T, url string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				t.Fatalf("read body: %v", readErr)
			}
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became reachable: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
