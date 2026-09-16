// Command verify is a black-box acceptance client: it exercises a running
// raincut API over HTTP and checks exact minimum-shutdown costs, the stable
// 422 error structure, and the large-instance time budget. It talks only to
// the real HTTP API — no stubs, no hard-coded responses.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const requestTimeout = 10 * time.Second

var baseURL = func() string {
	if v := os.Getenv("API_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8080"
}()

type edge struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	Cost int64 `json:"cost"`
}

type solveRequest struct {
	N       int64   `json:"n"`
	Edges   []edge  `json:"edges"`
	Sources []int64 `json:"sources"`
	Sinks   []int64 `json:"sinks"`
}

type solveResponse struct {
	MinimumShutdownCost int64 `json:"minimum_shutdown_cost"`
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var failures int

func pass(name string) { fmt.Printf("[PASS] %s\n", name) }

func fail(name, format string, args ...any) {
	failures++
	fmt.Printf("[FAIL] %s: %s\n", name, fmt.Sprintf(format, args...))
}

func post(raw []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/mincut", bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func expectCost(name string, payload solveRequest, want int64) {
	raw, err := json.Marshal(payload)
	if err != nil {
		fail(name, "marshal: %v", err)
		return
	}
	start := time.Now()
	status, body, err := post(raw)
	elapsed := time.Since(start)
	if err != nil {
		fail(name, "request failed: %v", err)
		return
	}
	if status != http.StatusOK {
		fail(name, "status %d, body %s", status, body)
		return
	}
	var resp solveResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fail(name, "response not JSON: %v (%s)", err, body)
		return
	}
	if resp.MinimumShutdownCost != want {
		fail(name, "minimum_shutdown_cost=%d, want %d", resp.MinimumShutdownCost, want)
		return
	}
	fmt.Printf("[PASS] %s (cost=%d, %s)\n", name, want, elapsed.Round(time.Millisecond))
}

func expect422(name string, raw []byte) {
	status, body, err := post(raw)
	if err != nil {
		fail(name, "request failed: %v", err)
		return
	}
	if status != http.StatusUnprocessableEntity {
		fail(name, "status %d, want 422, body %s", status, body)
		return
	}
	var resp errorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fail(name, "error body not JSON: %v (%s)", err, body)
		return
	}
	if resp.Error.Code == "" || resp.Error.Message == "" {
		fail(name, "error structure incomplete: %s", body)
		return
	}
	pass(name)
}

// largeCase builds a deterministic 20000-node / 100000-edge network whose
// minimum cut is exactly the sum of the small edges into node 1: every edge
// into the sink is one of them, and every other edge costs 1e9, more than
// that sum, so no cheaper cut exists.
func largeCase() (solveRequest, int64) {
	const n = 20000
	const m = 100000
	const big = int64(1_000_000_000)
	state := uint64(20260916)
	next := func() uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state >> 11
	}
	edges := make([]edge, 0, m)
	var want int64
	for i := int64(2); i <= 5000; i++ {
		edges = append(edges, edge{From: 0, To: i, Cost: big})
		c := int64(next()%1000) + 1
		edges = append(edges, edge{From: i, To: 1, Cost: c})
		want += c
	}
	for len(edges) < m {
		u := int64(2 + next()%uint64(n-2))
		v := int64(2 + next()%uint64(n-2))
		edges = append(edges, edge{From: u, To: v, Cost: big})
	}
	return solveRequest{N: n, Edges: edges, Sources: []int64{0}, Sinks: []int64{1}}, want
}

func tooManyEdgesPayload() []byte {
	var b strings.Builder
	b.WriteString(`{"n":2,"edges":[`)
	for i := 0; i < 100001; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"from":0,"to":1,"cost":1}`)
	}
	b.WriteString(`],"sources":[0],"sinks":[1]}`)
	return []byte(b.String())
}

func waitForAPI() error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("API at %s not healthy after 60s", baseURL)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func main() {
	fmt.Printf("verify: exercising API at %s (request timeout %s)\n", baseURL, requestTimeout)
	if err := waitForAPI(); err != nil {
		fmt.Println("verify: " + err.Error())
		os.Exit(1)
	}

	// Exact-cost cases: parallel edges, self-loops, directedness,
	// multi-source multi-sink, and degenerate zero-cost answers.
	expectCost("single edge", solveRequest{N: 2, Edges: []edge{{0, 1, 7}}, Sources: []int64{0}, Sinks: []int64{1}}, 7)
	expectCost("parallel edges charged individually", solveRequest{N: 2, Edges: []edge{{0, 1, 3}, {0, 1, 4}}, Sources: []int64{0}, Sinks: []int64{1}}, 7)
	expectCost("self loops do not affect result", solveRequest{N: 3, Edges: []edge{{0, 0, 100}, {0, 1, 5}, {1, 1, 9}, {1, 2, 2}}, Sources: []int64{0}, Sinks: []int64{2}}, 2)
	expectCost("directed edges only", solveRequest{N: 2, Edges: []edge{{1, 0, 5}}, Sources: []int64{0}, Sinks: []int64{1}}, 0)
	expectCost("no path means zero cost", solveRequest{N: 4, Edges: []edge{{0, 1, 5}, {2, 3, 6}}, Sources: []int64{0}, Sinks: []int64{3}}, 0)
	expectCost("chain bottleneck", solveRequest{N: 5, Edges: []edge{{0, 1, 9}, {1, 2, 4}, {2, 3, 6}, {3, 4, 3}}, Sources: []int64{0}, Sinks: []int64{4}}, 3)
	expectCost("two disjoint paths", solveRequest{N: 4, Edges: []edge{{0, 1, 5}, {1, 3, 5}, {0, 2, 8}, {2, 3, 8}}, Sources: []int64{0}, Sinks: []int64{3}}, 13)
	expectCost("multi source multi sink", solveRequest{N: 6, Edges: []edge{{0, 2, 10}, {1, 2, 1}, {2, 3, 4}, {3, 4, 3}, {3, 5, 2}, {0, 4, 12}, {1, 5, 8}}, Sources: []int64{0, 1}, Sinks: []int64{4, 5}}, 24)
	expectCost("must sever every branch", solveRequest{N: 3, Edges: []edge{{0, 1, 4}, {0, 2, 6}}, Sources: []int64{0}, Sinks: []int64{1, 2}}, 10)

	// Invalid graphs: never solved, stable 422 error structure.
	expect422("n below minimum", []byte(`{"n":1,"edges":[],"sources":[0],"sinks":[1]}`))
	expect422("n above maximum", []byte(`{"n":20001,"edges":[],"sources":[0],"sinks":[1]}`))
	expect422("edge endpoint out of range", []byte(`{"n":2,"edges":[{"from":0,"to":2,"cost":1}],"sources":[0],"sinks":[1]}`))
	expect422("negative edge endpoint", []byte(`{"n":2,"edges":[{"from":-1,"to":1,"cost":1}],"sources":[0],"sinks":[1]}`))
	expect422("cost below minimum", []byte(`{"n":2,"edges":[{"from":0,"to":1,"cost":0}],"sources":[0],"sinks":[1]}`))
	expect422("cost above maximum", []byte(`{"n":2,"edges":[{"from":0,"to":1,"cost":1000000001}],"sources":[0],"sinks":[1]}`))
	expect422("non-integer cost", []byte(`{"n":2,"edges":[{"from":0,"to":1,"cost":1.5}],"sources":[0],"sinks":[1]}`))
	expect422("empty sources", []byte(`{"n":2,"edges":[],"sources":[],"sinks":[1]}`))
	expect422("missing sinks", []byte(`{"n":2,"edges":[],"sources":[0]}`))
	expect422("source/sink overlap", []byte(`{"n":3,"edges":[],"sources":[0,1],"sinks":[1,2]}`))
	expect422("sink id out of range", []byte(`{"n":2,"edges":[],"sources":[0],"sinks":[5]}`))
	expect422("malformed JSON", []byte(`{"n":2,`))
	expect422("unknown field", []byte(`{"n":2,"edges":[],"sources":[0],"sinks":[1],"foo":1}`))
	expect422("too many edges", tooManyEdgesPayload())

	// The server must still answer correctly after the invalid barrage.
	expectCost("server healthy after invalid input", solveRequest{N: 2, Edges: []edge{{0, 1, 11}}, Sources: []int64{0}, Sinks: []int64{1}}, 11)

	// Large instance: 20000 nodes, 100000 edges, exact value within the
	// 10s request timeout.
	large, want := largeCase()
	expectCost("large instance 20000 nodes / 100000 edges", large, want)

	if failures > 0 {
		fmt.Printf("verify: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("verify: all checks passed")
}
