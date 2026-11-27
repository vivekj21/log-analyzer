// Package serve is the REST API of `log-analyzer serve`.
//
// It is built to be easy first, safe by default:
//   - one field is enough to start (the namespace), and ?wait=true returns the report in the same call;
//   - one investigation runs at a time (cost and safety); a short queue waits behind it;
//   - it needs no token while it listens on this computer only; a token is required to open it to the network;
//   - errors say what to do next.
package serve

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s-log-analyzer/internal/agent"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/report"
)

// Investigator runs one investigation and waits for its result. agent.Service is the real one.
type Investigator interface {
	Investigate(ctx context.Context, id string, req agent.Request) (agent.Result, error)
}

// Config is everything the server needs to know.
type Config struct {
	Token        string        // when set, every /v1 call needs "Authorization: Bearer <token>"
	Namespaces   []string      // when set, only these namespaces may be investigated
	QueueMax     int           // investigations that may wait behind the running one (default 5)
	Timeout      time.Duration // longest one investigation may run (default 10m)
	ResultTTL    time.Duration // how long a finished report can be fetched (default 1h)
	LoopbackOnly bool          // the server listens on this computer only
	Mode         string        // one line for the home page: which model is in use
	Dedupe       time.Duration // an alert for the same service is not investigated again within this time (default 30m)
}

const maxStoredJobs = 200

// Job states.
const (
	stQueued  = "queued"
	stRunning = "running"
	stTimeout = "timeout"
)

type job struct {
	id      string
	req     agent.Request
	created time.Time
	done    chan struct{}

	mu       sync.Mutex
	status   string
	finished time.Time
	result   *agent.Result
	errMsg   string
}

func (j *job) state() (status string, res *agent.Result, errMsg string, finished time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status, j.result, j.errMsg, j.finished
}

// Server is the HTTP side. Create it with New.
type Server struct {
	cfg   Config
	inv   Investigator
	queue chan *job
	now   func() time.Time

	mu     sync.Mutex
	jobs   map[string]*job
	order  []string
	recent map[string]time.Time // alert key -> when it was last investigated
}

// New builds a server around an Investigator.
func New(cfg Config, inv Investigator) *Server {
	if cfg.QueueMax <= 0 {
		cfg.QueueMax = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.ResultTTL <= 0 {
		cfg.ResultTTL = time.Hour
	}
	if cfg.Dedupe <= 0 {
		cfg.Dedupe = 30 * time.Minute
	}
	return &Server{cfg: cfg, inv: inv, queue: make(chan *job, cfg.QueueMax), now: time.Now,
		jobs: map[string]*job{}, recent: map[string]time.Time{}}
}

// IsLoopbackAddr says whether a listen address is reachable from this computer only.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return hostIsLoopback(host)
}

func hostIsLoopback(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.Handle("POST /v1/investigations", s.guard(http.HandlerFunc(s.handleCreate)))
	mux.Handle("GET /v1/investigations", s.guard(http.HandlerFunc(s.handleList)))
	mux.Handle("GET /v1/investigations/{id}", s.guard(http.HandlerFunc(s.handleGet)))
	mux.Handle("POST /v1/alertmanager", s.guard(http.HandlerFunc(s.handleAlertmanager)))
	return mux
}

// Serve runs the worker and the HTTP server until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go s.worker(ctx)
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      s.cfg.Timeout + time.Minute, // a ?wait=true call may wait for a whole investigation
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ---- access rules ----

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" {
			h := r.Header.Get("Authorization")
			got, hasScheme := "", len(h) > 7 && strings.EqualFold(h[:7], "bearer ")
			if hasScheme {
				got = strings.TrimSpace(h[7:])
			}
			if !hasScheme || subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="log-analyzer"`)
				apiError(w, http.StatusUnauthorized, "missing or wrong token", `send the header: Authorization: Bearer <your token>`)
				return
			}
		} else {
			// No token: the server must only be reachable from this computer, and a web page open in
			// your browser must not be able to start investigations. Browsers always send Origin on
			// cross-site requests; scripts and curl do not.
			if r.Header.Get("Origin") != "" {
				apiError(w, http.StatusForbidden, "browser requests are blocked while no token is set",
					"start the server with --token-file to allow them")
				return
			}
			if s.cfg.LoopbackOnly && !hostIsLoopback(r.Host) {
				apiError(w, http.StatusForbidden, "this server only answers requests addressed to localhost",
					"use http://localhost:PORT or http://127.0.0.1:PORT")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- responses ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg, hint string) {
	body := map[string]string{"error": msg}
	if hint != "" {
		body["hint"] = hint
	}
	writeJSON(w, code, body)
}

func wantsWait(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("wait")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ---- handlers ----

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in createRequest
	if err := dec.Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			apiError(w, http.StatusRequestEntityTooLarge, "the request is too large", "a request needs only a few lines of JSON")
		case errors.Is(err, io.EOF):
			apiError(w, http.StatusBadRequest, "the request body is empty", `send JSON like {"namespace":"shop"}`)
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			apiError(w, http.StatusBadRequest, err.Error(), "the fields are: namespace, service, symptom, since")
		default:
			apiError(w, http.StatusBadRequest, "the request is not valid JSON", `send JSON like {"namespace":"shop"}`)
		}
		return
	}
	req, p := normalize(in, s.cfg.Namespaces)
	if p != nil {
		code := http.StatusBadRequest
		if strings.Contains(p.Message, "not allowed") {
			code = http.StatusForbidden
		}
		apiError(w, code, p.Message, p.Hint)
		return
	}
	j, err := s.submit(req)
	if err != nil {
		w.Header().Set("Retry-After", "30")
		apiError(w, http.StatusTooManyRequests, "too many investigations are waiting",
			fmt.Sprintf("one runs at a time and up to %d may wait. Try again in about 30 seconds", s.cfg.QueueMax))
		return
	}
	log.Printf("[Serve] investigation %s accepted (namespace %s)", j.id, req.Namespace)
	if wantsWait(r) {
		select {
		case <-j.done:
			s.writeJob(w, r, j)
		case <-r.Context().Done():
		}
		return
	}
	w.Header().Set("Location", "/v1/investigations/"+j.id)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": j.id, "status": stQueued, "queue_position": s.position(j),
		"url":  "/v1/investigations/" + j.id,
		"hint": "fetch the url to get the report, or add ?wait=true to this POST to wait for it",
	})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	j := s.lookup(r.PathValue("id"))
	if j == nil {
		apiError(w, http.StatusNotFound, "unknown or expired investigation id",
			fmt.Sprintf("finished reports are kept for %s. List the recent ones with GET /v1/investigations", s.cfg.ResultTTL))
		return
	}
	s.writeJob(w, r, j)
}

type listItem struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Namespace string `json:"namespace"`
	Service   string `json:"service,omitempty"`
	Category  string `json:"category,omitempty"`
	Created   string `json:"created"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.purgeLocked()
	ids := append([]string(nil), s.order...)
	s.mu.Unlock()
	items := []listItem{}
	for i := len(ids) - 1; i >= 0 && len(items) < 50; i-- {
		j := s.lookup(ids[i])
		if j == nil {
			continue
		}
		st, res, _, _ := j.state()
		it := listItem{ID: j.id, Status: st, Namespace: j.req.Namespace, Service: j.req.Service, Created: j.created.UTC().Format(time.RFC3339)}
		if res != nil && res.Report != nil {
			it.Category = res.Report.Category
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"investigations": items})
}

// reportBody is the finished report as JSON: the report fields, plus the status.
type reportBody struct {
	Status string `json:"status"`
	report.Report
}

func (s *Server) writeJob(w http.ResponseWriter, r *http.Request, j *job) {
	st, res, errMsg, _ := j.state()
	md := r.URL.Query().Get("format") == "md"
	switch st {
	case stQueued, stRunning:
		w.Header().Set("Retry-After", "5")
		body := map[string]any{"id": j.id, "status": st, "hint": "not finished yet: ask again in a few seconds, or use ?wait=true"}
		if st == stQueued {
			body["queue_position"] = s.position(j)
		}
		writeJSON(w, http.StatusAccepted, body)
	case agent.StatusDone, agent.StatusPartial:
		if md {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			fmt.Fprint(w, res.Markdown)
			return
		}
		if res.Report == nil {
			writeJSON(w, http.StatusOK, map[string]string{"id": j.id, "status": st, "markdown": res.Markdown})
			return
		}
		writeJSON(w, http.StatusOK, reportBody{Status: st, Report: *res.Report})
	default: // failed or timeout
		code := http.StatusInternalServerError
		if st == stTimeout {
			code = http.StatusGatewayTimeout
		}
		if md {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(code)
			fmt.Fprintf(w, "Investigation %s %s: %s\n", j.id, st, errMsg)
			return
		}
		writeJSON(w, code, map[string]string{"id": j.id, "status": st, "error": errMsg})
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	host := r.Host
	auth, tokenFlag := "", ""
	if s.cfg.Token != "" {
		auth, tokenFlag = ` -H "Authorization: Bearer $TOKEN"`, " --token-file TOKENFILE"
	}
	fmt.Fprintf(w, `log-analyzer serve
Model: %s

From a terminal, investigate a namespace and get the report:
  log-analyzer --server %s%s --namespace shop

From a script or any other program, the same thing over HTTP:
  curl -X POST "http://%s/v1/investigations?wait=true"%s -d '{"namespace":"shop"}'

Fields (only namespace is required): namespace, service, symptom, since (for example 30m)
Add ?format=md to a report URL for text you can paste into a chat or a ticket.

  POST /v1/investigations         start one (add ?wait=true to get the report in the same call)
  GET  /v1/investigations/{id}    fetch a report (202 while it is still running)
  GET  /v1/investigations         the recent ones
  POST /v1/alertmanager           Prometheus Alertmanager webhook
  GET  /healthz                   health check
`, s.cfg.Mode, host, tokenFlag, host, auth)
}

func (s *Server) handleAlertmanager(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var p alertPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		apiError(w, http.StatusBadRequest, "this is not an Alertmanager webhook body", "point an Alertmanager webhook_configs url at this address")
		return
	}
	type started struct {
		ID        string `json:"id"`
		Alert     string `json:"alert"`
		Namespace string `json:"namespace"`
		Service   string `json:"service,omitempty"`
	}
	type skipped struct {
		Alert  string `json:"alert"`
		Reason string `json:"reason"`
	}
	out := struct {
		Started []started `json:"started"`
		Skipped []skipped `json:"skipped"`
	}{[]started{}, []skipped{}}
	full := false
	for i, a := range p.Alerts {
		if i >= maxAlertsPerPost {
			out.Skipped = append(out.Skipped, skipped{"", fmt.Sprintf("only the first %d alerts of one message are used", maxAlertsPerPost)})
			break
		}
		cr, name, reason, ok := alertRequest(a, s.cfg.Namespaces)
		if ok {
			req, prob := normalize(cr, s.cfg.Namespaces)
			switch {
			case prob != nil:
				ok, reason = false, prob.Message
			case s.seenRecently(req, name):
				ok, reason = false, fmt.Sprintf("already investigated in the last %s", s.cfg.Dedupe)
			default:
				j, err := s.submit(req)
				if err != nil {
					ok, reason, full = false, "the queue is full", true
					s.forgetRecent(req, name)
				} else {
					log.Printf("[Serve] investigation %s accepted from alert %q (namespace %s)", j.id, name, req.Namespace)
					out.Started = append(out.Started, started{j.id, name, req.Namespace, req.Service})
				}
			}
		}
		if !ok {
			out.Skipped = append(out.Skipped, skipped{name, reason})
		}
	}
	if len(out.Started) == 0 && full {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, out) // Alertmanager retries on this
		return
	}
	code := http.StatusOK
	if len(out.Started) > 0 {
		code = http.StatusAccepted
	}
	writeJSON(w, code, out)
}

// ---- jobs ----

var errQueueFull = errors.New("queue full")

func (s *Server) submit(req agent.Request) (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	j := &job{id: protocol.GenerateID(), req: req, created: s.now(), status: stQueued, done: make(chan struct{})}
	select {
	case s.queue <- j:
	default:
		return nil, errQueueFull
	}
	s.jobs[j.id] = j
	s.order = append(s.order, j.id)
	return j, nil
}

// purgeLocked drops expired finished jobs, and the oldest finished ones when too many are stored.
func (s *Server) purgeLocked() {
	now := s.now()
	keep := s.order[:0]
	for _, id := range s.order {
		j := s.jobs[id]
		_, _, _, fin := j.state()
		if !fin.IsZero() && now.Sub(fin) > s.cfg.ResultTTL {
			delete(s.jobs, id)
			continue
		}
		keep = append(keep, id)
	}
	s.order = keep
	for len(s.order) >= maxStoredJobs {
		dropped := false
		for i, id := range s.order {
			if _, _, _, fin := s.jobs[id].state(); !fin.IsZero() {
				delete(s.jobs, id)
				s.order = append(s.order[:i], s.order[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
	for k, t := range s.recent {
		if now.Sub(t) > s.cfg.Dedupe {
			delete(s.recent, k)
		}
	}
}

func (s *Server) lookup(id string) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil {
		return nil
	}
	if _, _, _, fin := j.state(); !fin.IsZero() && s.now().Sub(fin) > s.cfg.ResultTTL {
		delete(s.jobs, id)
		for i, o := range s.order {
			if o == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
		return nil
	}
	return j
}

// position is how many investigations are ahead of j in the queue, plus one.
func (s *Server) position(j *job) int {
	s.mu.Lock()
	ids := append([]string(nil), s.order...)
	s.mu.Unlock()
	pos := 1
	for _, id := range ids {
		if id == j.id {
			break
		}
		if o := s.lookupNoPurge(id); o != nil {
			if st, _, _, _ := o.state(); st == stQueued {
				pos++
			}
		}
	}
	return pos
}

func (s *Server) lookupNoPurge(id string) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

func alertKey(req agent.Request, name string) string {
	return req.Namespace + "|" + req.Service + "|" + name
}

func (s *Server) seenRecently(req agent.Request, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := alertKey(req, name)
	if t, ok := s.recent[k]; ok && s.now().Sub(t) < s.cfg.Dedupe {
		return true
	}
	s.recent[k] = s.now()
	return false
}

func (s *Server) forgetRecent(req agent.Request, name string) {
	s.mu.Lock()
	delete(s.recent, alertKey(req, name))
	s.mu.Unlock()
}

// worker runs the queued investigations one after another.
func (s *Server) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			s.run(ctx, j)
		}
	}
}

func (s *Server) run(ctx context.Context, j *job) {
	j.mu.Lock()
	j.status = stRunning
	j.mu.Unlock()

	rctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	res, err := s.inv.Investigate(rctx, j.id, j.req)
	cancel()

	j.mu.Lock()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		j.status = stTimeout
		j.errMsg = fmt.Sprintf("the investigation took longer than %s and was stopped. Raise --timeout, or narrow it with a service name", s.cfg.Timeout)
	case err != nil:
		j.status = agent.StatusFailed
		j.errMsg = err.Error()
	default:
		j.status = res.Status
		j.result = &res
		j.errMsg = res.Error
	}
	j.finished = s.now()
	status := j.status
	j.mu.Unlock()
	close(j.done)

	if status == agent.StatusDone || status == agent.StatusPartial {
		cat := ""
		if res.Report != nil {
			cat = res.Report.Category
		}
		log.Printf("[Serve] investigation %s finished: %s %s", j.id, status, cat)
	} else {
		log.Printf("[Serve] investigation %s %s", j.id, status)
	}
}
