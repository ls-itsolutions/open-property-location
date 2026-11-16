package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/geocode"
)

// The Model Context Protocol server: JSON-RPC 2.0 with the tools
// get_surroundings and list_categories, over stdio (ServeMCP) or Streamable
// HTTP (POST /mcp). It has no sessions and never sends requests itself, so
// the HTTP transport answers every request with a single JSON response.

// mcpVersions are the supported protocol versions, newest first. The tools
// subset this server uses is the same in all of them.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const (
	maxMCPMessageBytes = 1 << 20

	jsonrpcParseError     = -32700
	jsonrpcInvalidRequest = -32600
	jsonrpcMethodNotFound = -32601
	jsonrpcInvalidParams  = -32602
)

const mcpInstructions = `Looks up what is near a location in Germany, Austria, Switzerland or another region the server was built for: supermarkets, bakeries, schools, doctors, bus stops and train stations with their lines and departures per hour, beaches, parks and more, from OpenStreetMap data. Use it to describe the surroundings of a property, holiday rental or address. Walking minutes are estimates from the straight-line distance. Always show the attribution of a result to the user.`

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeMCP runs the MCP server on newline-delimited JSON-RPC messages, as
// MCP clients send them to a subprocess on stdin.
func ServeMCP(ctx context.Context, places *Places, cats *categories.Config, geocoder *geocode.Client, in io.Reader, out io.Writer) error {
	s := &server{places: places, cats: cats, geocoder: geocoder}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), maxMCPMessageBytes)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if resp := s.handleMCP(ctx, line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func (s *server) mcpHTTP(w http.ResponseWriter, r *http.Request) {
	// Browsers send an Origin header; only allow pages of this server, so a
	// website cannot use a server on localhost (DNS rebinding).
	if origin := r.Header.Get("Origin"); origin != "" {
		if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !slices.Contains(mcpVersions, v) {
		writeError(w, http.StatusBadRequest, "unsupported MCP-Protocol-Version "+v)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPMessageBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "message too large")
		return
	}
	resp := s.handleMCP(r.Context(), body)
	if resp == nil {
		// Notifications and responses get no answer.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMCP handles one JSON-RPC message. It returns nil for notifications.
func (s *server) handleMCP(ctx context.Context, data []byte) *rpcResponse {
	var msg rpcMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{jsonrpcParseError, "parse error"}}
	}
	// Notifications, and responses to requests this server never sends.
	if msg.ID == nil || (msg.Method == "" && (msg.Result != nil || msg.Error != nil)) {
		return nil
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: msg.ID}
	if msg.JSONRPC != "2.0" || msg.Method == "" {
		resp.Error = &rpcError{jsonrpcInvalidRequest, "invalid request"}
		return resp
	}
	var err *rpcError
	switch msg.Method {
	case "initialize":
		resp.Result, err = mcpInitialize(msg.Params)
	case "ping":
		resp.Result = struct{}{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.mcpTools()}
	case "tools/call":
		resp.Result, err = s.mcpCall(ctx, msg.Params)
	default:
		err = &rpcError{jsonrpcMethodNotFound, "method not found: " + msg.Method}
	}
	resp.Error = err
	return resp
}

func mcpInitialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{jsonrpcInvalidParams, "invalid initialize params"}
	}
	version := p.ProtocolVersion
	if !slices.Contains(mcpVersions, version) {
		version = mcpVersions[0]
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]string{"name": "open-property-location", "version": buildVersion()},
		"instructions":    mcpInstructions,
	}, nil
}

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

func (s *server) mcpTools() []map[string]any {
	ids := make([]string, 0, len(s.cats.Categories))
	for _, cat := range s.cats.Categories {
		ids = append(ids, cat.ID)
	}
	presets := slices.Sorted(maps.Keys(s.cats.Presets))
	readOnly := map[string]bool{"readOnlyHint": true, "idempotentHint": true}
	return []map[string]any{
		{
			"name":  "get_surroundings",
			"title": "Surroundings of a location",
			"description": "Returns the nearest places around a location, grouped by category, with distance in meters, " +
				"estimated walking minutes, address, opening hours, website, the public transport lines of stops and " +
				"their departures per hour on a weekday. Give either lat and lon, or an address, and either a preset or " +
				"categories. Preset stay fits holiday rentals (restaurants, beach, sights, transport), preset living fits " +
				"homes (schools, doctors, supermarkets, transport).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"lat":     map[string]any{"type": "number", "minimum": -90, "maximum": 90, "description": "WGS84 latitude"},
					"lon":     map[string]any{"type": "number", "minimum": -180, "maximum": 180, "description": "WGS84 longitude"},
					"address": map[string]any{"type": "string", "maxLength": maxAddressLength, "description": "Free-text address, used instead of lat and lon"},
					"preset":  map[string]any{"type": "string", "enum": presets},
					"categories": map[string]any{
						"type": "array", "items": map[string]any{"type": "string", "enum": ids},
						"description": "Category ids, used instead of a preset",
					},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxLimit, "default": defaultLimit, "description": "Places per category"},
				},
			},
			"annotations": readOnly,
		},
		{
			"name":        "list_categories",
			"title":       "Categories and presets",
			"description": "Lists the category ids with their search radius and the presets with their categories.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": readOnly,
		},
	}
}

func (s *server) mcpCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{jsonrpcInvalidParams, "invalid tools/call params"}
	}
	switch p.Name {
	case "list_categories":
		return toolResult(s.categoryList()), nil
	case "get_surroundings":
		q, err := toolQuery(p.Arguments)
		if err != nil {
			return toolError(err.Error()), nil
		}
		resp, err := s.lookup(ctx, q)
		var reqErr *requestError
		if errors.As(err, &reqErr) {
			return toolError(reqErr.msg), nil
		}
		if err != nil {
			slog.Error("lookup failed", "err", err)
			return toolError("internal error"), nil
		}
		return toolResult(resp), nil
	}
	return nil, &rpcError{jsonrpcInvalidParams, "unknown tool: " + p.Name}
}

// toolQuery converts tool arguments to API query parameters, so that the
// tool validates exactly like the HTTP API.
func toolQuery(args map[string]json.RawMessage) (url.Values, error) {
	q := url.Values{}
	for key, raw := range args {
		switch key {
		case "lat", "lon", "limit":
			var n float64
			if err := json.Unmarshal(raw, &n); err != nil {
				return nil, fmt.Errorf("%s must be a number", key)
			}
			q.Set(key, strconv.FormatFloat(n, 'f', -1, 64))
		case "address", "preset":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("%s must be a string", key)
			}
			q.Set(key, v)
		case "categories":
			var ids []string
			if err := json.Unmarshal(raw, &ids); err != nil {
				return nil, errors.New("categories must be an array of category ids")
			}
			q.Set(key, strings.Join(ids, ","))
		default:
			return nil, fmt.Errorf("unknown argument %q", key)
		}
	}
	return q, nil
}

func toolResult(v any) map[string]any {
	text, _ := json.Marshal(v)
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(text)}},
		"structuredContent": v,
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": msg}},
		"isError": true,
	}
}
