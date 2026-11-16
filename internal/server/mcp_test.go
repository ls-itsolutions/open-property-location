package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testRPCResponse struct {
	ID     int `json:"id"`
	Result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Tools           []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent surroundingsResponse `json:"structuredContent"`
		IsError           bool                 `json:"isError"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

func TestMCPStdio(t *testing.T) {
	places, cats, geocoder := newTestData(t, nil)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_surroundings","arguments":{"lat":53.0,"lon":8.8,"categories":["bus_tram_stop"],"limit":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_surroundings","arguments":{"lat":53.0,"lon":8.8,"preset":"stay","categories":["park"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":6,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := ServeMCP(context.Background(), places, cats, geocoder, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}

	var resps []testRPCResponse
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r testRPCResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		resps = append(resps, r)
	}
	// The notification gets no response.
	if len(resps) != 7 {
		t.Fatalf("got %d responses, want 7", len(resps))
	}
	if v := resps[0].Result.ProtocolVersion; v != "2025-06-18" {
		t.Errorf("negotiated version %q, want the client's", v)
	}
	if tools := resps[1].Result.Tools; len(tools) != 2 || tools[0].Name != "get_surroundings" || tools[1].Name != "list_categories" {
		t.Errorf("tools = %+v", tools)
	}

	call := resps[2].Result
	if call.IsError || len(call.Content) != 1 || call.Content[0].Type != "text" {
		t.Fatalf("tools/call result = %+v", call)
	}
	var fromText surroundingsResponse
	if err := json.Unmarshal([]byte(call.Content[0].Text), &fromText); err != nil {
		t.Fatal(err)
	}
	for _, r := range []surroundingsResponse{fromText, call.StructuredContent} {
		if r.Attribution != testAttribution || len(r.Categories) != 1 || len(r.Categories[0].Places) != 1 || *r.Categories[0].Places[0].Name != "Markt" {
			t.Errorf("surroundings = %+v", r)
		}
	}

	// Invalid arguments are a tool error, so the model sees the message.
	if r := resps[3].Result; !r.IsError || !strings.Contains(r.Content[0].Text, "either preset or categories") {
		t.Errorf("invalid arguments: %+v", r)
	}
	if e := resps[4].Error; e == nil || e.Code != jsonrpcMethodNotFound {
		t.Errorf("unknown method: %+v", e)
	}
	if v := resps[5].Result.ProtocolVersion; v != mcpVersions[0] {
		t.Errorf("unsupported client version negotiated to %q, want %q", v, mcpVersions[0])
	}
	if e := resps[6].Error; e == nil || e.Code != jsonrpcParseError {
		t.Errorf("invalid JSON: %+v", e)
	}
}

func TestMCPHTTP(t *testing.T) {
	h := newTestHandler(t, nil)
	post := func(body string, header http.Header) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range header {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_categories"}}`, nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("tools/call: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `\"id\":\"bus_tram_stop\"`) {
		t.Errorf("list_categories = %s", rec.Body)
	}

	if rec := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notification: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, http.Header{"Origin": {"https://evil.example"}}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign origin: %d", rec.Code)
	}
	if rec := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, http.Header{"Origin": {"http://localhost:8080"}}); rec.Code != http.StatusOK {
		t.Errorf("same origin: %d", rec.Code)
	}
	if rec := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, http.Header{"Mcp-Protocol-Version": {"1999-01-01"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported protocol version header: %d", rec.Code)
	}

	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp: %d, want 405 (no server-sent events)", get.Code)
	}
}
