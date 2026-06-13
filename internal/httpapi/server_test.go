package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/n3wscott/phonebook/internal/calls"
	"github.com/n3wscott/phonebook/internal/model"
	"github.com/n3wscott/phonebook/internal/testutil"
)

func TestPhonebookHandlerETagAndCaching(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/xml/", AllowDebug: true}, logger)

	contact := model.Contact{
		FirstName: "John",
		LastName:  "Doe",
		Extension: "8000",
		Phones: []model.Phone{
			{Number: "8000", AccountIndex: 1},
		},
		Endpoint: model.ContactEndpoint{Template: "endpoint-template"},
	}
	xml := []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><AddressBook></AddressBook>")
	lastMod := time.Unix(1700000000, 0).UTC()
	srv.Update([]model.Contact{contact}, xml, lastMod)

	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag header")
	}
	lastModified := rr.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatalf("missing Last-Modified header")
	}

	// If-None-Match should trigger 304
	req = httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for matching ETag, got %d", rr.Code)
	}

	// If-Modified-Since should also trigger 304
	req = httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	req.Header.Set("If-Modified-Since", lastModified)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for If-Modified-Since, got %d", rr.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/", AllowDebug: false}, logger)
	srv.Update([]model.Contact{}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		OK       bool   `json:"ok"`
		Contacts int    `json:"contacts"`
		Version  uint64 `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !body.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestProvisionEndpoint(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/xml/", AllowDebug: false}, logger)
	srv.UpdateProvision(
		[]model.Contact{},
		[]byte("<AddressBook></AddressBook>"),
		map[string][]byte{"cfgec74d74bee8c.xml": []byte("<gs_provision/>")},
		time.Unix(0, 0),
	)

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/prov/cfgec74d74bee8c.xml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "<gs_provision/>" {
		t.Fatalf("unexpected body: %q", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct == "" || !strings.Contains(ct, "xml") {
		t.Fatalf("expected xml content type, got %q", ct)
	}
}

func TestProvisionEndpointCaseInsensitivePath(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/xml/", AllowDebug: false}, logger)
	srv.UpdateProvision(
		[]model.Contact{},
		[]byte("<AddressBook></AddressBook>"),
		map[string][]byte{"cfgec74d74bee8c.xml": []byte("<gs_provision/>")},
		time.Unix(0, 0),
	)

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/prov/cfgEC74D74BEE8C.xml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "<gs_provision/>" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestTR069InformEndpoint(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/xml/", AllowDebug: false}, logger)
	srv.Update(
		[]model.Contact{},
		[]byte("<AddressBook></AddressBook>"),
		time.Unix(0, 0),
	)

	handler := srv.Handler()
	body := `<?xml version="1.0"?>
<soap-env:Envelope xmlns:soap-env="http://schemas.xmlsoap.org/soap/envelope/" xmlns:cwmp="urn:dslforum-org:cwmp-1-0">
  <soap-env:Body>
    <cwmp:Inform>
      <DeviceId>
        <Manufacturer>Grandstream</Manufacturer>
        <OUI>EC74D7</OUI>
        <ProductClass>WP816</ProductClass>
        <SerialNumber>34806PB22d</SerialNumber>
      </DeviceId>
      <Event soap-env:arrayType="cwmp:EventStruct[1]">
        <EventStruct>
          <EventCode>2 PERIODIC</EventCode>
          <CommandKey></CommandKey>
        </EventStruct>
      </Event>
    </cwmp:Inform>
  </soap-env:Body>
</soap-env:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/tr069", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "InformResponse") {
		t.Fatalf("expected InformResponse in body, got %q", rr.Body.String())
	}
}

func TestCallsEndpointsAreRootMountedWithBasePathCompatibility(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{
		Addr:        ":0",
		BasePath:    "/xml/",
		CallService: calls.NewService(calls.Options{}, logger),
	}, logger)
	srv.Update([]model.Contact{}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	handler := srv.Handler()
	for _, path := range []string{
		"/calls",
		"/api/calls/active",
		"/api/calls/history",
		"/api/calls/contacts",
		"/xml/calls",
		"/xml/api/calls/active",
		"/xml/api/calls/history",
		"/xml/api/calls/contacts",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rr.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/calls", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if body := rr.Body.String(); !strings.Contains(body, `"/api/calls/active"`) || strings.Contains(body, `"/xml/api/calls/active"`) {
		t.Fatalf("calls page should use root-mounted API paths, got body: %s", body)
	}
}

func TestBroadcastEndpointsAreRootMountedWithBasePathCompatibility(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{
		Addr:     ":0",
		BasePath: "/xml/",
		Broadcast: BroadcastConfig{
			Enabled: true,
		},
	}, logger)
	srv.Update([]model.Contact{}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	handler := srv.Handler()
	for _, path := range []string{
		"/broadcast",
		"/api/broadcast/contacts",
		"/xml/broadcast",
		"/xml/api/broadcast/contacts",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rr.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/broadcast", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if body := rr.Body.String(); !strings.Contains(body, `"/api/broadcast/contacts"`) || strings.Contains(body, `"/xml/api/broadcast/contacts"`) {
		t.Fatalf("broadcast page should use root-mounted API paths, got body: %s", body)
	}
}
