package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/dpkingii/relay-authz/internal/cache"
	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
)

// newServer runs the real handlers against Postgres and Redis.
func newServer(t *testing.T) *httptest.Server {
	databaseURL, redisURL := os.Getenv("DATABASE_URL"), os.Getenv("REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("DATABASE_URL or REDIS_URL not set; run `docker compose up -d` first")
	}
	s, err := store.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })
	rs, err := rules.Load("../../rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(s, rs, cache.Checker{
		Uncached: check.Checker{Reader: s, Rules: rs},
		Revision: s.Revision,
		Redis:    rdb,
		Prefix:   fmt.Sprintf("test:%x:", rand.Uint64()),
	}))
	t.Cleanup(srv.Close)
	return srv
}

func change(t *testing.T, srv *httptest.Server, method, tup string) int64 {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+"/v1/tuples", strings.NewReader(`{"tuple":"`+tup+`"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct{ Revision int64 }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		t.Fatalf("%s %s: status %d", method, tup, resp.StatusCode)
	}
	return body.Revision
}

type checkResult struct {
	Allowed  bool
	Revision int64
	Cache    string
}

func checkAt(t *testing.T, srv *httptest.Server, object, relation, user, atLeast string) checkResult {
	t.Helper()
	q := url.Values{"object": {object}, "relation": {relation}, "user": {user}}
	if atLeast != "" {
		q.Set("at_least", atLeast)
	}
	resp, err := http.Get(srv.URL + "/v1/check?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res checkResult
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&res) != nil {
		t.Fatalf("check %s#%s@%s: status %d", object, relation, user, resp.StatusCode)
	}
	res.Cache = resp.Header.Get("X-Cache")
	return res
}

// TestRevokedAccessIsNeverServedFromCache is the "new enemy" case from the Zanzibar paper:
// once a revoke has returned, no check may answer from a result cached before it.
func TestRevokedAccessIsNeverServedFromCache(t *testing.T) {
	srv := newServer(t)
	id := fmt.Sprintf("t%x", rand.Uint64())
	doc, folder, group, user := "document:"+id, "folder:"+id, "group:"+id, "user:"+id

	change(t, srv, "POST", doc+"#parent@"+folder)
	change(t, srv, "POST", folder+"#viewer@"+group+"#member")
	granted := change(t, srv, "POST", group+"#member@"+user)

	if res := checkAt(t, srv, doc, "viewer", user, ""); !res.Allowed || res.Cache != "miss" {
		t.Fatalf("first check = %+v, want an uncached allow", res)
	}
	// at_least rather than the latest revision, since other tests writing in parallel would bump it.
	if res := checkAt(t, srv, doc, "viewer", user, fmt.Sprint(granted)); !res.Allowed || res.Cache != "hit" {
		t.Fatalf("repeat check = %+v, want a cached allow", res)
	}

	revoked := change(t, srv, "DELETE", group+"#member@"+user)
	if revoked <= granted {
		t.Fatalf("revoke revision %d is not after grant revision %d", revoked, granted)
	}

	if res := checkAt(t, srv, doc, "viewer", user, ""); res.Allowed {
		t.Errorf("check after revoke = %+v, want deny", res)
	}
	if res := checkAt(t, srv, doc, "viewer", user, fmt.Sprint(revoked)); res.Allowed || res.Revision < revoked {
		t.Errorf("check at the revoke's revision = %+v, want deny at revision >= %d", res, revoked)
	}
}

func TestCheckRejectsBadRevisions(t *testing.T) {
	srv := newServer(t)
	for _, atLeast := range []string{"abc", "-1", "999999999999"} {
		q := "object=document:x&relation=viewer&user=user:x&at_least=" + atLeast
		resp, err := http.Get(srv.URL + "/v1/check?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("at_least=%s: status %d, want 400", atLeast, resp.StatusCode)
		}
	}
}
