package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestSelectAlertmanager(t *testing.T) {
	name, err := selectAlertmanager([]string{"production", "staging"}, "staging")
	if err != nil {
		t.Fatalf("selectAlertmanager() error = %v", err)
	}
	if name != "staging" {
		t.Fatalf("selectAlertmanager() = %q, want staging", name)
	}

	if _, err := selectAlertmanager([]string{"production"}, "missing"); err == nil {
		t.Fatal("selectAlertmanager() error = nil, want unknown instance error")
	}
}

func TestParseLabelsJSON(t *testing.T) {
	labels, err := parseLabelsJSON(`{"severity":"critical","team":"platform"}`)
	if err != nil {
		t.Fatalf("parseLabelsJSON() error = %v", err)
	}
	if labels["severity"] != "critical" || labels["team"] != "platform" {
		t.Fatalf("parseLabelsJSON() = %#v, want parsed labels", labels)
	}

	var object map[string]string
	if err := json.Unmarshal([]byte(`{"severity":"critical"}`), &object); err != nil {
		t.Fatal(err)
	}
	if _, err := parseLabelsJSON(`[]`); err == nil {
		t.Fatal("parseLabelsJSON() error = nil, want object validation error")
	}
}

func TestParseLabelsJSONRejectsNullValues(t *testing.T) {
	if _, err := parseLabelsJSON(`{"severity":null}`); err == nil {
		t.Fatal("parseLabelsJSON() error = nil, want null label rejection")
	}
}

func TestServeCancelsHandlerContextsOnSignal(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted)
			<-r.Context().Done()
			close(requestCanceled)
		}),
		ReadHeaderTimeout: time.Second,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	signals := make(chan os.Signal, 1)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serve(server, listener, signals)
	}()

	requestResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}

	signals <- os.Interrupt
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled")
	}
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("serve() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve() did not shut down after signal")
	}
	<-requestResult
}

func TestServeShutsDownOnSignal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: time.Second,
	}
	signals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serve(server, listener, signals)
	}()

	signals <- os.Interrupt

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve() did not shut down after signal")
	}
}
