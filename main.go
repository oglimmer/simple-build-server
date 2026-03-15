package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// Config

type Config struct {
	Listen    string            `yaml:"listen"`
	StateDir  string            `yaml:"state_dir"`
	LogDir    string            `yaml:"log_dir"`
	AppsDir   string            `yaml:"apps_dir"`
	Apps      map[string]AppCfg `yaml:"apps"`
	Dashboard DashboardCfg      `yaml:"dashboard"`
}

type AppCfg struct {
	Token string `yaml:"token"`
}

type DashboardCfg struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
}

// Build state

type BuildState struct {
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time,omitempty"`
	GitHash    string `json:"git_hash,omitempty"`
	GitURL     string `json:"git_url,omitempty"`
	LogFile    string `json:"log_file,omitempty"`
	Status     *int   `json:"status"`
	TestStatus *int   `json:"test_status"`
	HasTest    bool   `json:"has_test"`
	NoTest     bool   `json:"no_test"`
	Running    bool   `json:"running"`
}

// Server

type Server struct {
	cfg    Config
	mu     sync.Mutex
	states map[string]*BuildState
	builds map[string]context.CancelFunc // per-app cancel
	logger *slog.Logger
	logOut *os.File
}

func main() {
	cfgPath := flag.String("config", "/etc/redeploy/config.yaml", "path to config file")
	flag.Parse()

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read config: %v\n", err)
		os.Exit(1)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse config: %v\n", err)
		os.Exit(1)
	}

	os.MkdirAll(cfg.StateDir, 0755)
	os.MkdirAll(cfg.LogDir, 0755)

	logFile, err := os.OpenFile(filepath.Join(cfg.LogDir, "redeploy.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logFile), nil))

	srv := &Server{
		cfg:    cfg,
		states: make(map[string]*BuildState),
		builds: make(map[string]context.CancelFunc),
		logger: logger,
		logOut: logFile,
	}

	srv.loadStates()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rebuild/{name}", srv.handleRebuild)
	mux.HandleFunc("GET /dashboard/", srv.handleDashboard)
	mux.HandleFunc("GET /dashboard/log/{name}", srv.handleBuildLog)
	mux.HandleFunc("GET /dashboard/log/", srv.handleEngineLog)
	mux.HandleFunc("POST /dashboard/rebuild/{name}", srv.handleDashboardRebuild)

	httpSrv := &http.Server{Addr: cfg.Listen, Handler: mux}

	go func() {
		logger.Info("starting server", "addr", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")
	httpSrv.Shutdown(context.Background())
}

// State persistence

func (s *Server) stateFile(name string) string {
	return filepath.Join(s.cfg.StateDir, "lastdeploy-"+name+".json")
}

func (s *Server) loadStates() {
	for name := range s.cfg.Apps {
		data, err := os.ReadFile(s.stateFile(name))
		if err != nil {
			continue
		}
		var st BuildState
		if json.Unmarshal(data, &st) == nil {
			st.Running = false // not running after restart
			s.states[name] = &st
		}
	}
}

func (s *Server) saveState(name string, st *BuildState) {
	data, _ := json.MarshalIndent(st, "", "  ")
	os.WriteFile(s.stateFile(name), data, 0644)
}

// Auth helpers

func (s *Server) checkBearerToken(r *http.Request, appName string) bool {
	app, ok := s.cfg.Apps[appName]
	if !ok {
		return false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	return bcrypt.CompareHashAndPassword([]byte(app.Token), []byte(token)) == nil
}

func (s *Server) checkBasicAuth(w http.ResponseWriter, r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok || user != s.cfg.Dashboard.Username ||
		bcrypt.CompareHashAndPassword([]byte(s.cfg.Dashboard.PasswordHash), []byte(pass)) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="redeploy"`)
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// Build executor

func (s *Server) triggerBuild(appName string) {
	s.mu.Lock()
	// Cancel any running build for this app
	if cancel, ok := s.builds[appName]; ok {
		cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.builds[appName] = cancel
	s.mu.Unlock()

	go s.runBuild(ctx, appName)
}

func (s *Server) runBuild(ctx context.Context, appName string) {
	appDir := filepath.Join(s.cfg.AppsDir, appName)
	startTime := time.Now()
	logFileName := fmt.Sprintf("redeploy-build-%s-%d.log", appName, startTime.Unix())
	logPath := filepath.Join(s.cfg.LogDir, logFileName)

	logFile, err := os.Create(logPath)
	if err != nil {
		s.logger.Error("failed to create build log", "app", appName, "err", err)
		return
	}
	defer logFile.Close()

	// Get git info
	gitHash := s.runScript(appDir, "./get-git-hash.sh")
	gitURL := s.runScript(appDir, "./get-git-url.sh")

	hasTest := fileExists(filepath.Join(appDir, "test.sh"))

	st := &BuildState{
		StartTime: startTime.Format(time.RFC1123),
		GitHash:   gitHash,
		GitURL:    gitURL,
		LogFile:   logPath,
		HasTest:   hasTest,
		NoTest:    !hasTest,
		Running:   true,
	}

	s.mu.Lock()
	s.states[appName] = st
	s.saveState(appName, st)
	s.mu.Unlock()

	s.logger.Info("build started", "app", appName, "log", logPath)

	// Run build.sh
	buildStatus := s.execScript(ctx, appDir, "./build.sh", logFile)

	s.mu.Lock()
	st.Status = &buildStatus
	s.saveState(appName, st)
	s.mu.Unlock()

	s.logger.Info("build finished", "app", appName, "status", buildStatus)

	// Run test.sh if it exists
	if hasTest && ctx.Err() == nil {
		testStatus := s.execScript(ctx, appDir, "./test.sh", logFile)
		s.mu.Lock()
		st.TestStatus = &testStatus
		s.saveState(appName, st)
		s.mu.Unlock()
		s.logger.Info("tests finished", "app", appName, "status", testStatus)
	}

	s.mu.Lock()
	st.EndTime = time.Now().Format(time.RFC1123)
	st.Running = false
	s.saveState(appName, st)
	delete(s.builds, appName)
	s.mu.Unlock()
}

func (s *Server) runScript(dir, script string) string {
	if !fileExists(filepath.Join(dir, script)) {
		return ""
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Server) execScript(ctx context.Context, dir, script string, logFile *os.File) int {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return -1
	}
	return 0
}

// HTTP handlers

func (s *Server) handleRebuild(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.cfg.Apps[name]; !ok {
		http.Error(w, `{"error":"unknown app"}`, http.StatusBadRequest)
		return
	}
	if !s.checkBearerToken(r, name) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	s.triggerBuild(name)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"build_triggered","app":"%s"}`, name)
}

func (s *Server) handleDashboardRebuild(w http.ResponseWriter, r *http.Request) {
	if !s.checkBasicAuth(w, r) {
		return
	}
	name := r.PathValue("name")
	if _, ok := s.cfg.Apps[name]; !ok {
		http.Error(w, "unknown app", http.StatusBadRequest)
		return
	}

	s.triggerBuild(name)
	http.Redirect(w, r, "/dashboard/", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.checkBasicAuth(w, r) {
		return
	}

	type appData struct {
		Name    string
		State   *BuildState
		HasState bool
	}

	s.mu.Lock()
	var apps []appData
	for name := range s.cfg.Apps {
		st := s.states[name]
		apps = append(apps, appData{Name: name, State: st, HasState: st != nil})
	}
	s.mu.Unlock()

	data := struct {
		DateTime string
		Apps     []appData
	}{
		DateTime: time.Now().Format(time.RFC1123),
		Apps:     apps,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dashboardTmpl.Execute(w, data)
}

func (s *Server) handleBuildLog(w http.ResponseWriter, r *http.Request) {
	if !s.checkBasicAuth(w, r) {
		return
	}
	name := r.PathValue("name")
	if _, ok := s.cfg.Apps[name]; !ok {
		http.Error(w, "unknown app", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	st := s.states[name]
	s.mu.Unlock()

	if st == nil || st.LogFile == "" {
		http.Error(w, "no build log available", http.StatusNotFound)
		return
	}

	// Validate log path is under log_dir
	resolved, err := filepath.EvalSymlinks(st.LogFile)
	if err != nil || !strings.HasPrefix(resolved, s.cfg.LogDir) {
		http.Error(w, "invalid log file", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		http.Error(w, "failed to read log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	logTmpl.Execute(w, struct {
		Title   string
		Content string
	}{
		Title:   st.LogFile,
		Content: string(content),
	})
}

func (s *Server) handleEngineLog(w http.ResponseWriter, r *http.Request) {
	if !s.checkBasicAuth(w, r) {
		return
	}

	logPath := filepath.Join(s.cfg.LogDir, "redeploy.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		http.Error(w, "failed to read log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	logTmpl.Execute(w, struct {
		Title   string
		Content string
	}{
		Title:   logPath,
		Content: string(content),
	})
}

// Helpers

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Templates

var funcMap = template.FuncMap{
	"deref": func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	},
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(funcMap).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.0.2/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-EVSTQN3/azprG1Anm3QDgpJLIm9Nao0Yz1ztcQTwFspd3yD65VohhpuuCOmLASjC" crossorigin="anonymous">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.0.2/dist/js/bootstrap.bundle.min.js" integrity="sha384-MrcW6ZMFYlzcLA8Nl+NtUVF0sA7MsXsP1UyJoMp4YLEuNSfAP+JcXn/tWtIaxVXM" crossorigin="anonymous"></script>
    <style>div { margin: 0px 4px 4px 0px; }</style>
</head>
<body>
  <main class="container">
    <div class="p-4 p-md-5 mb-4 text-white rounded bg-dark">
      <div class="col-md-6 px-0">
        <h1 class="display-4 fst-italic">Builds</h1>
        {{.DateTime}}
      </div>
    </div>
    {{range .Apps}}
    <div class="row mb-1">
      <article class="blog-post">
        <h2 class="blog-post-title">{{.Name}}</h2>
        {{if not .HasState}}
          <div class="badge bg-info">never run</div>
        {{else}}
          <div class="blog-post-meta">Build done from
            <span class="badge bg-info text-dark">{{.State.StartTime}}</span> to
            <span class="badge bg-info text-dark">{{if .State.EndTime}}{{.State.EndTime}}{{else}}still running{{end}}</span>
            {{if and .State.GitURL .State.GitHash}}
              on source <a href="{{.State.GitURL}}/commit/{{.State.GitHash}}">#{{.State.GitHash}}</a>
            {{else if .State.GitURL}}
              <a href="{{.State.GitURL}}">on source</a>
            {{else if .State.GitHash}}
              on source #{{.State.GitHash}}
            {{end}}
          </div>
          {{if .State.Running}}
            <div class="badge bg-secondary">Build running...</div>
          {{else if eq (deref .State.Status) 0}}
            <div class="badge bg-success">Build succeeded.</div>
          {{else}}
            <div class="alert alert-danger" role="alert">Build failed with status {{deref .State.Status}}.</div>
          {{end}}
          {{if .State.NoTest}}
          {{else if .State.Running}}
            <div class="badge bg-secondary">Tests running...</div>
          {{else if .State.TestStatus}}
            {{if eq (deref .State.TestStatus) 0}}
              <div class="badge bg-success">Tests succeeded.</div>
            {{else}}
              <div class="alert alert-danger" role="alert">Tests failed with status {{deref .State.TestStatus}}.</div>
            {{end}}
          {{end}}
          <div><a href="/dashboard/log/{{.Name}}">Build log</a></div>
        {{end}}
        {{if .State}}{{if .State.Running}}
          <div>Build running...</div>
        {{else}}
          <div><form method="POST" action="/dashboard/rebuild/{{.Name}}" style="display:inline"><button type="submit" class="btn btn-sm btn-outline-primary">Trigger Rebuild</button></form></div>
        {{end}}{{else}}
          <div><form method="POST" action="/dashboard/rebuild/{{.Name}}" style="display:inline"><button type="submit" class="btn btn-sm btn-outline-primary">Trigger Rebuild</button></form></div>
        {{end}}
      </article>
    </div>
    {{end}}
    <hr />
    <div><a href="/dashboard/log/">Engine log</a></div>
  </main>
</body>
</html>`))

var logTmpl = template.Must(template.New("log").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.0.2/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-EVSTQN3/azprG1Anm3QDgpJLIm9Nao0Yz1ztcQTwFspd3yD65VohhpuuCOmLASjC" crossorigin="anonymous">
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.0.2/dist/js/bootstrap.bundle.min.js" integrity="sha384-MrcW6ZMFYlzcLA8Nl+NtUVF0sA7MsXsP1UyJoMp4YLEuNSfAP+JcXn/tWtIaxVXM" crossorigin="anonymous"></script>
    <style>div { margin: 0px 4px 4px 0px; }</style>
</head>
<body>
  <main class="container">
    <div class="p-4 p-md-5 mb-4 text-white rounded bg-dark">
      <div class="col-md-6 px-0">
        <h1 class="display-4 fst-italic">Showing log:</h1>
        <h3 class="fst-italic">{{.Title}}</h3>
      </div>
    </div>
    <div><a href="/dashboard/">Back</a></div>
    <pre>{{.Content}}</pre>
  </main>
</body>
</html>`))

