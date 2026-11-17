package capella

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// simCluster is one cluster held by clusterSim.
type simCluster struct {
	ID      string
	Name    string
	State   string
	Conn    string
	Deleted bool
}

// clusterSim serves the clusters collection of project p1 with state.
type clusterSim struct {
	mu           sync.Mutex
	order        []string
	clusters     map[string]*simCluster
	created      int
	createStates []string // state of the k-th created cluster; default healthy
	postStatus   int      // non-zero: POST fails with this status
	deleteStatus int      // non-zero: DELETE fails with this status
	listStatus   int      // non-zero: list fails with this status
	// getHook may override a single-cluster GET; n counts GETs of that id from 1.
	getHook func(id string, n int) (int, any, bool)
	gets    map[string]int
}

func newClusterSim(f *fakeAPI, existing ...*simCluster) *clusterSim {
	s := &clusterSim{clusters: map[string]*simCluster{}, gets: map[string]int{}}
	for _, c := range existing {
		s.clusters[c.ID] = c
		s.order = append(s.order, c.ID)
	}
	f.handle(http.MethodGet, fakeClustersPath, s.list)
	f.handle(http.MethodPost, fakeClustersPath, s.create)
	f.setFallback(s.item)
	return s
}

func (s *clusterSim) view(c *simCluster) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "currentState": c.State, "connectionString": c.Conn}
}

func (s *clusterSim) list(*http.Request, []byte) (int, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listStatus != 0 {
		return s.listStatus, errBody(s.listStatus)
	}
	var items []any
	for _, id := range s.order {
		if c := s.clusters[id]; !c.Deleted {
			items = append(items, s.view(c))
		}
	}
	return http.StatusOK, listPage(items...)
}

func (s *clusterSim) create(_ *http.Request, body []byte) (int, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.postStatus != 0 {
		return s.postStatus, errBody(s.postStatus)
	}
	state := "healthy"
	if s.created < len(s.createStates) {
		state = s.createStates[s.created]
	}
	s.created++
	id := "n" + strconv.Itoa(s.created)
	var req CreateClusterRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return http.StatusBadRequest, errBody(400)
	}
	s.clusters[id] = &simCluster{ID: id, Name: req.Name, State: state, Conn: "cb." + id + ".cloud.couchbase.com"}
	s.order = append(s.order, id)
	return http.StatusAccepted, map[string]any{"id": id}
}

func (s *clusterSim) item(r *http.Request, _ []byte) (int, any) {
	rest, ok := strings.CutPrefix(r.URL.Path, fakeClustersPath+"/")
	if !ok {
		return http.StatusNotFound, errBody(404)
	}
	id, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clusters[id]
	if sub == "certificates" {
		return http.StatusOK, map[string]any{"certificate": "PEM-" + id}
	}
	if sub != "" {
		return http.StatusNotFound, errBody(404)
	}
	switch r.Method {
	case http.MethodDelete:
		if s.deleteStatus != 0 {
			return s.deleteStatus, errBody(s.deleteStatus)
		}
		if c == nil || c.Deleted {
			return http.StatusNotFound, errBody(404)
		}
		c.Deleted = true
		return http.StatusAccepted, nil
	case http.MethodGet:
		s.gets[id]++
		if s.getHook != nil {
			if status, body, ok := s.getHook(id, s.gets[id]); ok {
				return status, body
			}
		}
		if c == nil || c.Deleted {
			return http.StatusNotFound, errBody(404)
		}
		return http.StatusOK, s.view(c)
	}
	return http.StatusMethodNotAllowed, errBody(405)
}

func (s *clusterSim) count() (created, deleted int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clusters {
		if c.Deleted {
			deleted++
		}
	}
	return s.created, deleted
}
