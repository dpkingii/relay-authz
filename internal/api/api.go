// Package api serves the HTTP endpoints.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

func New(s *store.Store, rs rules.Rules) http.Handler {
	checker := check.Checker{Reader: s, Rules: rs}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tuples", func(w http.ResponseWriter, r *http.Request) {
		t, ok := readTuple(w, r, rs)
		if !ok {
			return
		}
		if err := s.Write(r.Context(), t); err != nil {
			internalError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/tuples", func(w http.ResponseWriter, r *http.Request) {
		t, ok := readTuple(w, r, rs)
		if !ok {
			return
		}
		if err := s.Delete(r.Context(), t); err != nil {
			internalError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
		allowed, err := checker.Check(r.Context(), object, relation, user)
		if errors.Is(err, check.ErrTooDeep) {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
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

func internalError(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
