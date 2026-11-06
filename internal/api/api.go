// Package api serves the HTTP endpoints.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/dpkingii/relay-authz/internal/cache"
	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

func New(s *store.Store, rs rules.Rules, checker cache.Checker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tuples", func(w http.ResponseWriter, r *http.Request) {
		t, ok := readTuple(w, r, rs)
		if !ok {
			return
		}
		rev, err := s.Write(r.Context(), t)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, map[string]int64{"revision": rev})
	})
	mux.HandleFunc("DELETE /v1/tuples", func(w http.ResponseWriter, r *http.Request) {
		t, ok := readTuple(w, r, rs)
		if !ok {
			return
		}
		rev, err := s.Delete(r.Context(), t)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, map[string]int64{"revision": rev})
	})
	mux.HandleFunc("GET /v1/check", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		object, err := tuple.ParseObject(q.Get("object"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err := tuple.ParseObject(q.Get("user"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		relation := q.Get("relation")
		if !rs.HasRelation(object.Type, relation) {
			http.Error(w, fmt.Sprintf("unknown relation %s#%s", object.Type, relation), http.StatusBadRequest)
			return
		}
		if !rs.HasType(user.Type) {
			http.Error(w, fmt.Sprintf("unknown type %s", user.Type), http.StatusBadRequest)
			return
		}

		atLeast := cache.Latest
		if q.Has("at_least") {
			atLeast, err = strconv.ParseInt(q.Get("at_least"), 10, 64)
			if err != nil || atLeast < 0 {
				http.Error(w, "at_least must be a revision number", http.StatusBadRequest)
				return
			}
		}

		res, err := checker.Check(r.Context(), object, relation, user, atLeast)
		switch {
		case errors.Is(err, cache.ErrFutureRevision):
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		case errors.Is(err, check.ErrTooDeep):
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		case err != nil:
			internalError(w, err)
			return
		}
		// Lets a load test measure the cache hit rate.
		if res.Cached {
			w.Header().Set("X-Cache", "hit")
		} else {
			w.Header().Set("X-Cache", "miss")
		}
		writeJSON(w, map[string]any{"allowed": res.Allowed, "revision": res.Revision})
	})
	return mux
}

// readTuple decodes a {"tuple": "type:id#relation@type:id"} body, answering 400 if it's
// malformed or names a type or relation the rules don't define.
func readTuple(w http.ResponseWriter, r *http.Request, rs rules.Rules) (tuple.Tuple, bool) {
	var body struct {
		Tuple string `json:"tuple"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `body must be {"tuple": "..."}`, http.StatusBadRequest)
		return tuple.Tuple{}, false
	}
	t, err := tuple.Parse(body.Tuple)
	if err == nil {
		err = rs.Validate(t)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return tuple.Tuple{}, false
	}
	return t, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func internalError(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
