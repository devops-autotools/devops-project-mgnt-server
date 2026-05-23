package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Struct representing a queued console shell command
type ConsoleCommand struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Output    string    `json:"output"`
	Status    string    `json:"status"` // "pending", "success", "error"
	Prompt    string    `json:"prompt"`
	CreatedAt time.Time `json:"created_at"`
}

// Tag is a label that can be assigned to servers for grouping and filtering.
type Tag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Struct representing application config
type Config struct {
	Port          string
	AgentTLSPort  string
	AdminUsername string
	AdminPassword string
	SessionSecret string
}

// Struct representing a target server
type Server struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Token            string            `json:"token"`
	IP               string            `json:"ip"`
	OS               string            `json:"os"`
	CPUModel         string            `json:"cpu_model"`
	CPUCores         int               `json:"cpu_cores"`
	CPUUsage         float64           `json:"cpu_usage"`
	RAMTotal         float64           `json:"ram_total"`
	RAMUsed          float64           `json:"ram_used"`
	RAMFree          float64           `json:"ram_free"`
	RAMUsage         float64           `json:"ram_usage"`
	DiskTotal        float64           `json:"disk_total"`
	DiskUsed         float64           `json:"disk_used"`
	DiskFree         float64           `json:"disk_free"`
	DiskUsage        float64           `json:"disk_usage"`
	Uptime           string            `json:"uptime"`
	LastReport       time.Time         `json:"last_report"`
	Tags             []string          `json:"tags,omitempty"`
	Connected        bool              `json:"connected"`
	ActiveConsole    bool              `json:"active_console"`
	LastTerminalPoll time.Time         `json:"-"`
	PendingCmds      []*ConsoleCommand `json:"-"`
	ExecutedCmds     []*ConsoleCommand `json:"-"`
	// cmdNotify signals a waiting long-poll agent when a command is queued.
	// Buffered(1): non-blocking send, agent always checks PendingCmds on wake.
	cmdNotify        chan struct{}      `json:"-"`
}

// Struct for the JSON request sent by target agents
type AgentReport struct {
	Token     string  `json:"token"`
	Hostname  string  `json:"hostname"`
	IP        string  `json:"ip"`
	OS        string  `json:"os"`
	CPUModel  string  `json:"cpu_model"`
	CPUCores  int     `json:"cpu_cores"`
	CPUUsage  float64 `json:"cpu_usage"`
	RAMTotal  float64 `json:"ram_total"`
	RAMUsed   float64 `json:"ram_used"`
	RAMFree   float64 `json:"ram_free"`
	RAMUsage  float64 `json:"ram_usage"`
	DiskTotal float64 `json:"disk_total"`
	DiskUsed  float64 `json:"disk_used"`
	DiskFree  float64 `json:"disk_free"`
	DiskUsage float64 `json:"disk_usage"`
	Uptime    string  `json:"uptime"`
}

// Global Variables
var (
	config      Config
	servers     = make(map[string]*Server)
	sessions    = make(map[string]time.Time)
	serverMutex sync.RWMutex
	sessionMtx  sync.Mutex
	dataFile    = "data/servers.json"

	// SSE subscribers: serverID → list of channels (one per open browser terminal)
	sseClients   = make(map[string][]chan []byte)
	sseClientsMu sync.RWMutex

	// CA cert PEM — embedded in every agent install script for TLS verification
	caCertPEM []byte

	tags     = []Tag{}
	tagsMu   sync.RWMutex
	tagsFile = "data/tags.json"
)

// initServerRuntime initialises in-memory-only fields that are not persisted.
// Must be called for every Server after creation or load.
func initServerRuntime(s *Server) {
	if s.cmdNotify == nil {
		s.cmdNotify = make(chan struct{}, 1)
	}
}

func main() {
	log.Println("Starting Secure Server Manager Backend...")

	loadConfig()
	loadServers()
	loadTags()
	ensureDataDir()
	tlsConfig := ensureTLSCerts()

	// --- Browser mux: SPA + API (HTTP, no TLS required for dashboard) ---
	browserMux := http.NewServeMux()
	browserMux.Handle("/", http.FileServer(http.Dir("./public")))
	browserMux.HandleFunc("GET /agent/install/{token}", handleAgentInstallScript)
	browserMux.HandleFunc("POST /api/auth/login", handleLogin)
	browserMux.HandleFunc("POST /api/auth/logout", handleLogout)
	browserMux.HandleFunc("GET /api/auth/check", handleAuthCheck)
	browserMux.HandleFunc("GET /api/servers", authMiddleware(handleGetServers))
	browserMux.HandleFunc("POST /api/servers/add", authMiddleware(handleAddServer))
	browserMux.HandleFunc("DELETE /api/servers/{id}", authMiddleware(handleDeleteServer))
	browserMux.HandleFunc("POST /api/servers/execute/{id}", authMiddleware(handleQueueCommand))
	browserMux.HandleFunc("GET /api/servers/terminal/{id}", authMiddleware(handleGetTerminalLogs))
	browserMux.HandleFunc("GET /api/servers/terminal/stream/{id}", authMiddleware(handleTerminalStream))
	browserMux.HandleFunc("GET /api/tags", authMiddleware(handleGetTags))
	browserMux.HandleFunc("POST /api/tags", authMiddleware(handleCreateTag))
	browserMux.HandleFunc("DELETE /api/tags/{name}", authMiddleware(handleDeleteTag))
	browserMux.HandleFunc("PUT /api/servers/{id}/tags", authMiddleware(handleSetServerTags))

	// --- Agent mux: telemetry + long-poll (HTTPS only) ---
	agentMux := http.NewServeMux()
	agentMux.HandleFunc("POST /agent/report", handleAgentReport)
	agentMux.HandleFunc("POST /agent/report/result", handleAgentCommandResult)
	agentMux.HandleFunc("GET /agent/cmd/wait/{token}", handleAgentWaitCommand)

	go sessionCleanupLoop()

	// Start agent HTTPS listener in background
	agentAddr := ":" + config.AgentTLSPort
	go func() {
		ln, err := net.Listen("tcp", agentAddr)
		if err != nil {
			log.Fatalf("Agent TLS listen failed: %v", err)
		}
		log.Printf("Agent TLS listener on https://localhost%s", agentAddr)
		tlsLn := tls.NewListener(ln, tlsConfig)
		if err := http.Serve(tlsLn, logRequest(agentMux)); err != nil {
			log.Fatalf("Agent TLS server error: %v", err)
		}
	}()

	// Browser HTTP listener (foreground)
	browserAddr := ":" + config.Port
	log.Printf("Browser HTTP listener on http://localhost%s", browserAddr)
	if err := http.ListenAndServe(browserAddr, logRequest(browserMux)); err != nil {
		log.Fatalf("Browser server error: %v", err)
	}
}

// --- Middlewares & Utilities ---

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s from %s in %v", r.Method, r.URL.Path, r.Proto, r.RemoteAddr, time.Since(start))
	})
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_token")
		if err != nil {
			http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}

		sessionMtx.Lock()
		expiry, ok := sessions[cookie.Value]
		sessionMtx.Unlock()

		if !ok || time.Now().After(expiry) {
			// clean invalid session
			if ok {
				sessionMtx.Lock()
				delete(sessions, cookie.Value)
				sessionMtx.Unlock()
			}
			http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func getBaseURL(r *http.Request) string {
	proto := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	return proto + "://" + host
}

// --- Configuration & Persistence ---

// loadDotEnv parses .env into process environment. Existing env vars take priority.
func loadDotEnv() {
	f, err := os.Open("data/.env")
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() {
	os.MkdirAll("data", 0755)
	loadDotEnv()

	port := envOrDefault("MGNT_PORT", "8080")
	agentTLSPort := envOrDefault("MGNT_AGENT_TLS_PORT", "8443")
	username := envOrDefault("MGNT_USERNAME", "admin")
	password := envOrDefault("MGNT_PASSWORD", "")
	secret := envOrDefault("MGNT_SESSION_SECRET", "")

	firstRun := false
	if _, err := os.Stat("data/.env"); os.IsNotExist(err) {
		firstRun = true
	}

	if password == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		password = hex.EncodeToString(b)
		if !firstRun {
			log.Printf("WARNING: MGNT_PASSWORD not set — generated temporary password: %s", password)
		}
	}
	if secret == "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		secret = hex.EncodeToString(b)
	}

	config = Config{
		Port:          port,
		AgentTLSPort:  agentTLSPort,
		AdminUsername: username,
		AdminPassword: password,
		SessionSecret: secret,
	}

	if firstRun {
		writeDotEnv()
		log.Printf("First run — data/.env created. Admin password: %s", config.AdminPassword)
	}

	log.Printf("Configuration loaded. HTTP port: %s  Agent TLS port: %s", config.Port, config.AgentTLSPort)
}

func writeDotEnv() {
	content := fmt.Sprintf("# mgnt-server configuration — keep this file private\n\nMGNT_PORT=%s\nMGNT_AGENT_TLS_PORT=%s\nMGNT_USERNAME=%s\nMGNT_PASSWORD=%s\nMGNT_SESSION_SECRET=%s\n",
		config.Port, config.AgentTLSPort, config.AdminUsername, config.AdminPassword, config.SessionSecret)
	if err := os.WriteFile("data/.env", []byte(content), 0600); err != nil {
		log.Printf("Warning: could not write data/.env: %v", err)
	}
}

// ensureTLSCerts generates a self-signed CA + server cert on first run (stored in
// data/tls/) and returns a tls.Config ready for the agent HTTPS listener.
// The server cert uses DNS SAN "mgnt-server.local"; agents connect via
// --resolve to map that name to the real IP without needing the IP in the cert.
func ensureTLSCerts() *tls.Config {
	tlsDir := filepath.Join("data", "tls")
	if err := os.MkdirAll(tlsDir, 0700); err != nil {
		log.Fatalf("Failed to create TLS dir: %v", err)
	}

	caCertPath := filepath.Join(tlsDir, "ca.crt")
	caKeyPath := filepath.Join(tlsDir, "ca.key")
	srvCertPath := filepath.Join(tlsDir, "server.crt")
	srvKeyPath := filepath.Join(tlsDir, "server.key")

	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		log.Println("Generating TLS certificates for agent channel...")
		generateTLSCerts(caKeyPath, caCertPath, srvKeyPath, srvCertPath)
		log.Println("TLS certificates generated in data/tls/")
	}

	var err error
	caCertPEM, err = os.ReadFile(caCertPath)
	if err != nil {
		log.Fatalf("Failed to read CA cert: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(srvCertPath, srvKeyPath)
	if err != nil {
		log.Fatalf("Failed to load server cert/key: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}

func generateTLSCerts(caKeyPath, caCertPath, srvKeyPath, srvCertPath string) {
	// CA key + self-signed cert
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("CA key gen: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mgnt-server CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatalf("CA cert create: %v", err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	// Server key + cert signed by CA
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("Server key gen: %v", err)
	}

	// Collect local IPs for SANs (useful for direct LAN access)
	localIPs := []net.IP{net.ParseIP("127.0.0.1")}
	if ifaces, err2 := net.Interfaces(); err2 == nil {
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok {
					if ip4 := ipNet.IP.To4(); ip4 != nil {
						localIPs = append(localIPs, ip4)
					}
				}
			}
		}
	}

	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "mgnt-server"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		// mgnt-server.local is the fixed hostname agents use via --resolve
		DNSNames:    []string{"mgnt-server.local", "localhost"},
		IPAddresses: localIPs,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		log.Fatalf("Server cert create: %v", err)
	}

	writePEM := func(path string, blockType string, der []byte, mode os.FileMode) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), mode); err != nil {
			log.Fatalf("Write %s: %v", path, err)
		}
	}
	writeKeyPEM := func(path string, key *ecdsa.PrivateKey) {
		der, _ := x509.MarshalECPrivateKey(key)
		writePEM(path, "EC PRIVATE KEY", der, 0600)
	}

	writePEM(caCertPath, "CERTIFICATE", caDER, 0644)
	writeKeyPEM(caKeyPath, caKey)
	writePEM(srvCertPath, "CERTIFICATE", srvDER, 0644)
	writeKeyPEM(srvKeyPath, srvKey)
}

// getServerHost returns just the hostname/IP from the request, stripping the port.
func getServerHost(r *http.Request) string {
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func ensureDataDir() {
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}
	if err := os.MkdirAll("public/css", 0755); err != nil {
		log.Fatalf("Failed to create public/css directory: %v", err)
	}
	if err := os.MkdirAll("public/js", 0755); err != nil {
		log.Fatalf("Failed to create public/js directory: %v", err)
	}
}

func loadServers() {
	serverMutex.Lock()
	defer serverMutex.Unlock()

	file, err := os.Open(dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("servers.json does not exist. Starting with 0 servers.")
			return
		}
		log.Printf("Failed to load servers.json: %v", err)
		return
	}
	defer file.Close()

	var list []*Server
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&list); err != nil {
		log.Printf("Failed to parse servers.json: %v", err)
		return
	}

	for _, s := range list {
		servers[s.ID] = s
		initServerRuntime(s)
	}
	log.Printf("Successfully loaded %d servers from storage.\n", len(servers))
}

func saveServersNoLock() {
	list := make([]*Server, 0, len(servers))
	for _, s := range servers {
		list = append(list, s)
	}

	dir := filepath.Dir(dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Failed to create folder for server data: %v", err)
		return
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal servers data: %v", err)
		return
	}

	if err := os.WriteFile(dataFile, data, 0644); err != nil {
		log.Printf("Failed to save servers.json: %v", err)
	}
}

// --- Tag persistence ---

func loadTags() {
	data, err := os.ReadFile(tagsFile)
	if err != nil {
		return
	}
	tagsMu.Lock()
	defer tagsMu.Unlock()
	json.Unmarshal(data, &tags)
	log.Printf("Loaded %d tags from storage.", len(tags))
}

func saveTagsNoLock() {
	data, _ := json.MarshalIndent(tags, "", "  ")
	os.WriteFile(tagsFile, data, 0644)
}

// --- Tag API handlers ---

func handleGetTags(w http.ResponseWriter, r *http.Request) {
	tagsMu.RLock()
	defer tagsMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}
	if req.Color == "" {
		req.Color = "#6366f1"
	}

	tagsMu.Lock()
	defer tagsMu.Unlock()
	for _, t := range tags {
		if t.Name == req.Name {
			http.Error(w, `{"error":"tag already exists"}`, http.StatusConflict)
			return
		}
	}
	newTag := Tag{Name: req.Name, Color: req.Color}
	tags = append(tags, newTag)
	saveTagsNoLock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(newTag)
}

func handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	tagsMu.Lock()
	found := false
	var newTags []Tag
	for _, t := range tags {
		if t.Name == name {
			found = true
		} else {
			newTags = append(newTags, t)
		}
	}
	if !found {
		tagsMu.Unlock()
		http.Error(w, `{"error":"tag not found"}`, http.StatusNotFound)
		return
	}
	tags = newTags
	saveTagsNoLock()
	tagsMu.Unlock()

	// Remove deleted tag from all servers
	serverMutex.Lock()
	for _, s := range servers {
		var kept []string
		for _, st := range s.Tags {
			if st != name {
				kept = append(kept, st)
			}
		}
		s.Tags = kept
	}
	saveServersNoLock()
	serverMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func handleSetServerTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()
	s, ok := servers[id]
	if !ok {
		http.Error(w, `{"error":"server not found"}`, http.StatusNotFound)
		return
	}
	s.Tags = req.Tags
	saveServersNoLock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func sessionCleanupLoop() {
	for {
		time.Sleep(10 * time.Minute)
		sessionMtx.Lock()
		now := time.Now()
		for token, expiry := range sessions {
			if now.After(expiry) {
				delete(sessions, token)
			}
		}
		sessionMtx.Unlock()
	}
}

// --- Handler Functions ---

func handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	// Direct match validation
	if creds.Username != config.AdminUsername || creds.Password != config.AdminPassword {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Tên đăng nhập hoặc mật khẩu không chính xác"}`))
		return
	}

	// Generate secure session ID
	sessionToken := generateToken()
	expiry := time.Now().Add(24 * time.Hour)

	sessionMtx.Lock()
	sessions[sessionToken] = expiry
	sessionMtx.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // Change to true if deploying behind HTTPS
		Expires:  expiry,
	})

	_, _ = w.Write([]byte(`{"status":"success","message":"Login successful"}`))
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cookie, err := r.Cookie("session_token")
	if err == nil {
		sessionMtx.Lock()
		delete(sessions, cookie.Value)
		sessionMtx.Unlock()
	}

	// Clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})

	_, _ = w.Write([]byte(`{"status":"success"}`))
}

func handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cookie, err := r.Cookie("session_token")
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated":false}`))
		return
	}

	sessionMtx.Lock()
	expiry, ok := sessions[cookie.Value]
	sessionMtx.Unlock()

	if !ok || time.Now().After(expiry) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated":false}`))
		return
	}

	_, _ = w.Write([]byte(`{"authenticated":true}`))
}

func handleGetServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	serverMutex.RLock()
	defer serverMutex.RUnlock()

	// Prepare list and recalculate connection status based on reporting window
	list := make([]*Server, 0, len(servers))
	for _, s := range servers {
		// If agent reported within last 15 seconds, it's considered online
		if s.Connected && time.Since(s.LastReport) < 15*time.Second {
			// server is active
		} else if s.Connected {
			// Server checked in before, but offline now
		}
		list = append(list, s)
	}

	_ = json.NewEncoder(w).Encode(list)
}

func handleAddServer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		http.Error(w, `{"error":"Invalid server name"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()

	id := generateUUID()
	token := generateToken()

	newServer := &Server{
		ID:        id,
		Name:      strings.TrimSpace(req.Name),
		Token:     token,
		Connected: false,
	}
	initServerRuntime(newServer)

	servers[id] = newServer
	saveServersNoLock()

	_ = json.NewEncoder(w).Encode(newServer)
}

func handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"Server ID required"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()

	if _, exists := servers[id]; !exists {
		http.Error(w, `{"error":"Server not found"}`, http.StatusNotFound)
		return
	}

	delete(servers, id)
	saveServersNoLock()

	_, _ = w.Write([]byte(`{"status":"success","message":"Server removed"}`))
}

// --- Agent Protocol Handlers ---

func handleAgentReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var report AgentReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	if report.Token == "" {
		http.Error(w, `{"error":"Agent token is required"}`, http.StatusUnauthorized)
		return
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()

	// Find server by agent token
	var targetServer *Server
	for _, s := range servers {
		if s.Token == report.Token {
			targetServer = s
			break
		}
	}

	if targetServer == nil {
		http.Error(w, `{"error":"Unauthorized: Invalid Agent Token"}`, http.StatusUnauthorized)
		return
	}

	// Update telemetry
	targetServer.IP = report.IP
	targetServer.OS = report.OS
	targetServer.CPUModel = report.CPUModel
	targetServer.CPUCores = report.CPUCores
	targetServer.CPUUsage = report.CPUUsage
	targetServer.RAMTotal = report.RAMTotal
	targetServer.RAMUsed = report.RAMUsed
	targetServer.RAMFree = report.RAMFree
	targetServer.RAMUsage = report.RAMUsage
	targetServer.DiskTotal = report.DiskTotal
	targetServer.DiskUsed = report.DiskUsed
	targetServer.DiskFree = report.DiskFree
	targetServer.DiskUsage = report.DiskUsage
	targetServer.Uptime = report.Uptime
	targetServer.LastReport = time.Now()
	targetServer.Connected = true

	// Save to JSON
	saveServersNoLock()

	// Check if dynamic web console session has gone stale (no active frontend poll in 12s)
	if targetServer.ActiveConsole && time.Since(targetServer.LastTerminalPoll) > 12*time.Second {
		targetServer.ActiveConsole = false
	}

	// Dynamic poll interval switching
	interval := 5 // standard 5 seconds
	if targetServer.ActiveConsole {
		interval = 1 // short-poll mode
	}

	// Dequeue next command if present
	var nextCmd *ConsoleCommand
	if len(targetServer.PendingCmds) > 0 {
		nextCmd = targetServer.PendingCmds[0]
		targetServer.PendingCmds = targetServer.PendingCmds[1:]
		nextCmd.Status = "running"
		targetServer.ExecutedCmds = append(targetServer.ExecutedCmds, nextCmd)

		// Restrict executed commands backlog to prevent memory bloat
		if len(targetServer.ExecutedCmds) > 30 {
			targetServer.ExecutedCmds = targetServer.ExecutedCmds[len(targetServer.ExecutedCmds)-30:]
		}
	}

	resp := map[string]interface{}{
		"status":   "success",
		"interval": interval,
	}
	if nextCmd != nil {
		resp["command"] = nextCmd.Command
		resp["command_id"] = nextCmd.ID
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func handleAgentInstallScript(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	// Check if this token is valid
	serverMutex.RLock()
	found := false
	var serverName string
	for _, s := range servers {
		if s.Token == token {
			found = true
			serverName = s.Name
			break
		}
	}
	serverMutex.RUnlock()

	if !found {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	baseURL := getBaseURL(r)
	serverHost := getServerHost(r)

	scriptContent := fmt.Sprintf(`#!/bin/bash
# =========================================================================
#  Secure Server Manager Agent Installer  —  Server: %s
# =========================================================================
echo "========================================================="
echo " Installing Secure Server Manager Agent"
echo " Target Server: %s"
echo " Connecting to: %s"
echo "========================================================="

INSTALL_DIR="$HOME/.mgnt-agent"
mkdir -p "$INSTALL_DIR"

# Write pinned CA certificate for TLS verification
cat << 'CACERT_EOF' > "$INSTALL_DIR/ca.crt"
%s
CACERT_EOF
chmod 600 "$INSTALL_DIR/ca.crt"

AGENT_FILE="$INSTALL_DIR/agent.sh"

cat << 'AGENT_EOF' > "$AGENT_FILE"
#!/bin/bash
TOKEN="%s"
SERVER_IP="%s"
AGENT_TLS_PORT="%s"
AGENT_HOST="mgnt-server.local"
CA_CERT="$HOME/.mgnt-agent/ca.crt"

# Secure curl wrapper: verifies server cert via pinned CA, connects via --resolve
# so the real IP is used but hostname verification matches the cert SAN.
agent_curl() {
    curl -s --cacert "$CA_CERT" \
         --resolve "${AGENT_HOST}:${AGENT_TLS_PORT}:${SERVER_IP}" \
         "$@"
}

# ---- Helpers ----
get_os() {
    if [ -f /etc/os-release ]; then . /etc/os-release; echo "$NAME $VERSION"
    elif type lsb_release >/dev/null 2>&1; then lsb_release -d -s
    elif [ -f /etc/redhat-release ]; then cat /etc/redhat-release
    else uname -sr; fi
}
get_cpu_model() {
    model=$(grep -m 1 'model name' /proc/cpuinfo | awk -F: '{print $2}' | xargs)
    [ -z "$model" ] && model=$(uname -m); echo "$model"
}
get_cpu_cores() {
    cores=$(grep -c '^processor' /proc/cpuinfo)
    [ "$cores" -eq 0 ] && cores=1; echo "$cores"
}
get_ip() {
    ip=$(hostname -I | awk '{print $1}'); [ -z "$ip" ] && ip="127.0.0.1"; echo "$ip"
}
get_uptime() { uptime -p | sed 's/up //'; }
parse_json() {
    echo "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*[^,}]*" | head -1 | \
        cut -d: -f2- | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//;s/^null$//'
}
escape_json_string() {
    if type python3 >/dev/null 2>&1; then
        echo -n "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null
    else
        echo -n "$1" | sed 's/\\/\\\\/g;s/"/\\"/g' | xargs -0 printf '"%%s"'
    fi
}

# ---- TELEMETRY LOOP (background, every 5 s) ----
run_telemetry() {
    local LAST_CPU="0.0" CPU_PID="" CPU_F="/tmp/.mgnt_cpu_$$" LAST_T=0

    _cpu_bg() {
        ( read -r _ a b c d e f g h i j < /proc/stat
          p=$((a+b+c+f+g+h+i+j)); pt=$((p+d+e))
          sleep 1
          read -r _ a b c d e f g h i j < /proc/stat
          n=$((a+b+c+f+g+h+i+j)); nt=$((n+d+e))
          da=$((n-p)); dt=$((nt-pt))
          [ $dt -gt 0 ] && awk "BEGIN{printf \"%%.1f\",(${da}/${dt})*100}" > "$CPU_F" \
                        || echo "0.0" > "$CPU_F"
        ) & CPU_PID=$!
    }
    _cpu_bg

    while true; do
        local NOW; NOW=$(date +%%s)
        if [ -n "$CPU_PID" ] && ! kill -0 "$CPU_PID" 2>/dev/null; then
            [ -f "$CPU_F" ] && LAST_CPU=$(cat "$CPU_F"); CPU_PID=""
        fi
        [ -z "$CPU_PID" ] && [ $((NOW - LAST_T)) -ge 5 ] && { LAST_T=$NOW; _cpu_bg; }

        local rtk rak rfk ruk
        rtk=$(grep MemTotal    /proc/meminfo | awk '{print $2}')
        rak=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
        if [ -z "$rak" ]; then
            rfk=$(grep MemFree    /proc/meminfo | awk '{print $2}')
            local rb rc
            rb=$(grep Buffers     /proc/meminfo | awk '{print $2}')
            rc=$(grep ^Cached     /proc/meminfo | awk '{print $2}')
            ruk=$((rtk - rfk - rb - rc))
        else
            ruk=$((rtk - rak))
        fi

        local dtk duk dfk dp
        dtk=$(df -k / | tail -1 | awk '{print $2}')
        duk=$(df -k / | tail -1 | awk '{print $3}')
        dfk=$(df -k / | tail -1 | awk '{print $4}')
        dp=$(df -k  / | tail -1 | awk '{print $5}' | sed 's/%%//')

        agent_curl -X POST -H "Content-Type: application/json" \
            -d @- "https://${AGENT_HOST}:${AGENT_TLS_PORT}/agent/report" >/dev/null <<TELEM
{
  "token": "$TOKEN",
  "hostname": "$(hostname)",
  "ip": "$(get_ip)",
  "os": "$(get_os)",
  "cpu_model": "$(get_cpu_model)",
  "cpu_cores": $(get_cpu_cores),
  "cpu_usage": $LAST_CPU,
  "ram_total": $(awk "BEGIN{printf \"%%.2f\",$rtk/1048576}"),
  "ram_used":  $(awk "BEGIN{printf \"%%.2f\",$ruk/1048576}"),
  "ram_free":  $(awk "BEGIN{printf \"%%.2f\",($rtk-$ruk)/1048576}"),
  "ram_usage": $(awk "BEGIN{printf \"%%.1f\",($ruk/$rtk)*100}"),
  "disk_total": $(awk "BEGIN{printf \"%%.2f\",$dtk/1048576}"),
  "disk_used":  $(awk "BEGIN{printf \"%%.2f\",$duk/1048576}"),
  "disk_free":  $(awk "BEGIN{printf \"%%.2f\",$dfk/1048576}"),
  "disk_usage": $dp,
  "uptime": "$(get_uptime)"
}
TELEM
        sleep 5
    done
    rm -f "$CPU_F"
}

# ---- COMMAND LOOP (long-poll: ~0 ms delivery latency) ----
run_commands() {
    local CURRENT_DIR="$HOME"
    export TERM=xterm-256color DEBIAN_FRONTEND=noninteractive COLUMNS=220 LINES=50

    while true; do
        # Block at server until a command is queued (or 25 s timeout → empty/204)
        local response
        response=$(agent_curl --max-time 30 "https://${AGENT_HOST}:${AGENT_TLS_PORT}/agent/cmd/wait/$TOKEN" 2>/dev/null)
        [ -z "$response" ] && continue

        local command command_id
        command=$(parse_json "$response" "command")
        command_id=$(parse_json "$response" "command_id")
        [ -z "$command" ] || [ -z "$command_id" ] && continue

        # Convert known interactive commands to batch equivalents
        local proc_command="$command"
        case "$command" in
            top|"top "*)
                echo "$command" | grep -q '\-b' || proc_command="top -b -n 1 ${command#top}" ;;
            htop|"htop "*)
                proc_command="TERM=dumb htop --no-color 2>/dev/null || top -b -n 1" ;;
            vim|vi|nano|pico|emacs)
                proc_command="echo '[INFO] Interactive editor not supported. Use cat/echo/sed.'" ;;
            "vim "*|"vi "*|"nano "*|"pico "*|"emacs "*)
                proc_command="echo '[INFO] Interactive editors cannot run in web terminal.'" ;;
            less|more) proc_command="cat" ;;
            "less "*|"more "*) proc_command="cat ${command#* }" ;;
            "man "*) proc_command="MANPAGER=cat man ${command#man }" ;;
            watch|"watch "*) proc_command="echo '[INFO] watch not supported.'" ;;
        esac

        local RS="/tmp/ar_$$" EF="/tmp/ae_$$" OF="/tmp/ao_$$"
        printf '#!/bin/bash\nexport TERM=xterm-256color\nexport DEBIAN_FRONTEND=noninteractive\nexport COLUMNS=220\nexport LINES=50\ncd "$1" 2>/dev/null\neval "$2" > "$4" 2>&1\necho $? > "$3"\npwd >> "$3"' > "$RS"
        chmod +x "$RS"
        bash "$RS" "$CURRENT_DIR" "$proc_command" "$EF" "$OF"
        local cmd_output; cmd_output=$(cat "$OF" 2>/dev/null)

        local cmd_status="success"
        if [ -f "$EF" ]; then
            local ex nd; ex=$(head -1 "$EF"); nd=$(tail -1 "$EF")
            [ "$ex" -ne 0 ] 2>/dev/null && cmd_status="error"
            [ -d "$nd" ] && CURRENT_DIR="$nd"
            rm -f "$EF"
        fi
        rm -f "$RS" "$OF"

        local dd="$CURRENT_DIR"
        [ "$dd" = "$HOME" ] && dd="~" || case "$dd" in "$HOME/"*) dd="~${dd#$HOME}" ;; esac
        local UN; UN=$(whoami 2>/dev/null || echo root)
        local HN; HN=$(hostname -s 2>/dev/null || hostname)

        agent_curl -X POST -H "Content-Type: application/json" \
            -d @- "https://${AGENT_HOST}:${AGENT_TLS_PORT}/agent/report/result" >/dev/null <<RESULT
{
  "token": "$TOKEN",
  "command_id": "$command_id",
  "output": $(escape_json_string "$cmd_output"),
  "status": "$cmd_status",
  "prompt": $(escape_json_string "${UN}@${HN}:${dd}$")
}
RESULT
    done
}

# Start telemetry in background; run command loop in foreground
run_telemetry &
TELEM_PID=$!
trap "kill $TELEM_PID 2>/dev/null; exit" INT TERM EXIT
run_commands
AGENT_EOF

chmod +x "$AGENT_FILE"

LOG_FILE="$INSTALL_DIR/agent.log"
PID_FILE="$INSTALL_DIR/agent.pid"

if [ -f "$PID_FILE" ]; then
    old_pid=$(cat "$PID_FILE")
    if kill -0 "$old_pid" >/dev/null 2>&1; then
        echo "Updating agent process (killing previous PID $old_pid)..."
        kill "$old_pid"; sleep 1
    fi
fi

nohup "$AGENT_FILE" > "$LOG_FILE" 2>&1 &
agent_pid=$!
echo "$agent_pid" > "$PID_FILE"

echo "========================================================="
echo " Agent installed and started successfully!"
echo " PID: $agent_pid"
echo " Logs: $LOG_FILE"
echo " Dashboard will update in seconds."
echo "========================================================="
`, serverName, baseURL, token, strings.TrimSpace(string(caCertPEM)), token, serverHost, config.AgentTLSPort)

	w.Header().Set("Content-Type", "text/x-shellscript")
	_, _ = w.Write([]byte(scriptContent))
}

func handleQueueCommand(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"Server ID required"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	cmdText := strings.TrimSpace(req.Command)
	if cmdText == "" {
		http.Error(w, `{"error":"Command cannot be empty"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()

	targetServer, exists := servers[id]
	if !exists {
		http.Error(w, `{"error":"Server not found"}`, http.StatusNotFound)
		return
	}

	// Mark active terminal session and timestamp to boost agent polling to 1 second
	targetServer.ActiveConsole = true
	targetServer.LastTerminalPoll = time.Now()

	cmd := &ConsoleCommand{
		ID:        generateUUID(),
		Command:   cmdText,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	targetServer.PendingCmds = append(targetServer.PendingCmds, cmd)

	// Wake any agent waiting on the long-poll endpoint immediately.
	select {
	case targetServer.cmdNotify <- struct{}{}:
	default: // already has a pending signal; agent will see PendingCmds on next check
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "queued",
		"command_id": cmd.ID,
	})
}

func handleGetTerminalLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"Server ID required"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()
	defer serverMutex.Unlock()

	targetServer, exists := servers[id]
	if !exists {
		http.Error(w, `{"error":"Server not found"}`, http.StatusNotFound)
		return
	}

	// Refresh polling timestamp
	targetServer.ActiveConsole = true
	targetServer.LastTerminalPoll = time.Now()

	// Return all executed and pending commands
	allCmds := make([]*ConsoleCommand, 0, len(targetServer.ExecutedCmds)+len(targetServer.PendingCmds))
	allCmds = append(allCmds, targetServer.ExecutedCmds...)
	allCmds = append(allCmds, targetServer.PendingCmds...)

	_ = json.NewEncoder(w).Encode(allCmds)
}

// handleAgentWaitCommand is a long-poll endpoint for the agent.
// It blocks until a command is queued (responding immediately) or 25s elapses (204).
// This replaces the 200ms sleep loop, giving ~0ms command delivery latency.
func handleAgentWaitCommand(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, `{"error":"token required"}`, http.StatusBadRequest)
		return
	}

	serverMutex.RLock()
	var target *Server
	for _, s := range servers {
		if s.Token == token {
			target = s
			break
		}
	}
	serverMutex.RUnlock()

	if target == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	deadline := time.After(25 * time.Second)

	for {
		serverMutex.Lock()
		if len(target.PendingCmds) > 0 {
			cmd := target.PendingCmds[0]
			target.PendingCmds = target.PendingCmds[1:]
			cmd.Status = "running"
			target.ExecutedCmds = append(target.ExecutedCmds, cmd)
			if len(target.ExecutedCmds) > 30 {
				target.ExecutedCmds = target.ExecutedCmds[len(target.ExecutedCmds)-30:]
			}
			serverMutex.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{
				"command":    cmd.Command,
				"command_id": cmd.ID,
			})
			return
		}
		serverMutex.Unlock()

		// Wait for a new command signal, timeout, or client disconnect.
		select {
		case <-target.cmdNotify:
			// Command was queued — loop back to dequeue it.
		case <-deadline:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// handleTerminalStream is an SSE endpoint for the browser terminal.
// It pushes completed command results instantly instead of the browser polling.
func handleTerminalStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()
	srv, exists := servers[id]
	if !exists {
		serverMutex.Unlock()
		http.Error(w, `{"error":"server not found"}`, http.StatusNotFound)
		return
	}
	srv.ActiveConsole = true
	srv.LastTerminalPoll = time.Now()
	serverMutex.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan []byte, 32)

	sseClientsMu.Lock()
	sseClients[id] = append(sseClients[id], ch)
	sseClientsMu.Unlock()

	defer func() {
		sseClientsMu.Lock()
		list := sseClients[id]
		for i, c := range list {
			if c == ch {
				sseClients[id] = append(list[:i], list[i+1:]...)
				break
			}
		}
		sseClientsMu.Unlock()

		serverMutex.Lock()
		if s, ok2 := servers[id]; ok2 {
			s.ActiveConsole = false
		}
		serverMutex.Unlock()
	}()

	// Confirm connection to browser, then keep alive with 5s heartbeats.
	fmt.Fprint(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "event: cmd\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			// Keep connection alive and refresh terminal-active timestamp.
			serverMutex.Lock()
			if s, ok2 := servers[id]; ok2 {
				s.LastTerminalPoll = time.Now()
			}
			serverMutex.Unlock()
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func handleAgentCommandResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		Token     string `json:"token"`
		CommandID string `json:"command_id"`
		Output    string `json:"output"`
		Status    string `json:"status"`
		Prompt    string `json:"prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	serverMutex.Lock()

	var targetServer *Server
	for _, s := range servers {
		if s.Token == req.Token {
			targetServer = s
			break
		}
	}

	if targetServer == nil {
		serverMutex.Unlock()
		http.Error(w, `{"error":"Unauthorized: Invalid Agent Token"}`, http.StatusUnauthorized)
		return
	}

	// Update command execution results, capture pointer for SSE broadcast.
	var done *ConsoleCommand
	for _, c := range targetServer.ExecutedCmds {
		if c.ID == req.CommandID {
			c.Output = req.Output
			c.Prompt = req.Prompt
			if req.Status != "" {
				c.Status = req.Status
			} else {
				c.Status = "success"
			}
			done = c
			break
		}
	}
	serverMutex.Unlock()

	// Push completed command to all open browser SSE streams for this server.
	if done != nil {
		if data, err := json.Marshal(done); err == nil {
			sseClientsMu.RLock()
			for _, ch := range sseClients[targetServer.ID] {
				select {
				case ch <- data:
				default: // slow client — skip, it will catch up via poll fallback
				}
			}
			sseClientsMu.RUnlock()
		}
	}

	_, _ = w.Write([]byte(`{"status":"success"}`))
}
