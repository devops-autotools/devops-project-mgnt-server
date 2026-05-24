// Agent binary for mgnt-server — runs on monitored Linux servers.
// Sends telemetry every 5s, executes queued commands, and handles
// PTY shell sessions via WebSocket.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
)

// Linux ioctl constants for PTY management (/dev/ptmx).
// These are not exported by the syscall package but are stable ABI on Linux.
const (
	ioctlTIOCGPTN   uintptr = 0x80045430 // get pts slave number
	ioctlTIOCSPTLCK uintptr = 0x40045431 // unlock pts slave
)

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// AgentConfig is loaded from ~/.mgnt-agent/config.json at startup.
type AgentConfig struct {
	ServerHost    string `json:"server_host"`
	ServerTLSPort string `json:"server_tls_port"`
	Token         string `json:"token"`
}

// cmdResponse is the JSON body returned by the server's command poll endpoint.
type cmdResponse struct {
	Type        string `json:"type"`                   // "exec" | "shell"
	Command     string `json:"command"`                // exec only
	CommandID   string `json:"command_id"`             // exec only
	SessionID   string `json:"session_id"`             // shell only
	ContainerID string `json:"container_id,omitempty"` // shell only — docker exec target
	ShellMode   string `json:"shell_mode,omitempty"`   // "exec" | "logs"
}

// telemetryPayload mirrors AgentReport on the server.
type telemetryPayload struct {
	Token      string          `json:"token"`
	Hostname   string          `json:"hostname"`
	IP         string          `json:"ip"`
	OS         string          `json:"os"`
	CPUModel   string          `json:"cpu_model"`
	CPUCores   int             `json:"cpu_cores"`
	CPUUsage   float64         `json:"cpu_usage"`
	RAMTotal   float64         `json:"ram_total"`
	RAMUsed    float64         `json:"ram_used"`
	RAMFree    float64         `json:"ram_free"`
	RAMUsage   float64         `json:"ram_usage"`
	DiskTotal  float64         `json:"disk_total"`
	DiskUsed   float64         `json:"disk_used"`
	DiskFree   float64         `json:"disk_free"`
	DiskUsage  float64         `json:"disk_usage"`
	Uptime     string          `json:"uptime"`
	Containers []ContainerInfo `json:"containers,omitempty"`
}

// ContainerInfo holds container metadata reported per telemetry cycle.
type ContainerInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Image     string   `json:"image"`
	Status    string   `json:"status"`
	Runtime   string   `json:"runtime"`
	Namespace string   `json:"namespace,omitempty"`
	CPU       float64  `json:"cpu"`
	RAM       int64    `json:"ram"`
	Ports     []string `json:"ports"`
	Uptime    string   `json:"uptime"`
}

var (
	cfg        AgentConfig
	agentURL   string // https://mgnt-server.local:PORT
	httpClient *http.Client
	wsDialer   *websocket.Dialer
	latestCPU  atomic.Value // float64 — updated every second by cpuSampler
	pidLockFd  *os.File     // held open to keep flock alive for the process lifetime
)

// dockerHTTP communicates with the Docker daemon via its Unix socket.
var dockerHTTP = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", "/var/run/docker.sock")
		},
	},
	Timeout: 5 * time.Second,
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	home, _ := os.UserHomeDir()

	// Prevent multiple agent instances via exclusive flock on the PID file.
	// The lock is held for the lifetime of the process (fd never closed).
	pidPath := filepath.Join(home, ".mgnt-agent", "agent.pid")
	var pidErr error
	pidLockFd, pidErr = os.OpenFile(pidPath, os.O_WRONLY|os.O_CREATE, 0644)
	if pidErr != nil {
		log.Fatalf("cannot open pid file %s: %v", pidPath, pidErr)
	}
	if err := syscall.Flock(int(pidLockFd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Fatalf("another agent instance is already running — exiting")
	}
	pidLockFd.Truncate(0)
	pidLockFd.WriteString(fmt.Sprintf("%d\n", os.Getpid()))

	cfgPath := filepath.Join(home, ".mgnt-agent", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("Cannot read config %s: %v", cfgPath, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Cannot parse config: %v", err)
	}

	agentURL = fmt.Sprintf("https://mgnt-server.local:%s", cfg.ServerTLSPort)

	caCertPath := filepath.Join(home, ".mgnt-agent", "ca.crt")
	tlsCfg, err := buildTLS(caCertPath)
	if err != nil {
		log.Fatalf("TLS setup: %v", err)
	}

	dial := resolverDial(cfg.ServerHost, cfg.ServerTLSPort)
	httpClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
			DialContext:     func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dial(network, addr)
			},
		},
		Timeout: 35 * time.Second,
	}
	wsDialer = &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		NetDial:          dial,
		HandshakeTimeout: 10 * time.Second,
	}

	latestCPU.Store(float64(0))
	go cpuSampler()

	postTelemetry()
	go telemetryLoop()

	commandLoop()
}

// buildTLS creates a TLS config trusting only the embedded CA cert.
func buildTLS(caPath string) (*tls.Config, error) {
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("parse CA cert failed")
	}
	return &tls.Config{RootCAs: pool}, nil
}

// resolverDial maps the virtual hostname "mgnt-server.local" to the real server IP.
// This lets curl-style --resolve work in pure Go without DNS tricks.
func resolverDial(serverHost, tlsPort string) func(network, addr string) (net.Conn, error) {
	real := serverHost + ":" + tlsPort
	d := &net.Dialer{Timeout: 10 * time.Second}
	return func(network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "mgnt-server.local:") {
			addr = real
		}
		return d.Dial(network, addr)
	}
}

// --- CPU sampling ---

func cpuSampler() {
	type stat struct{ user, nice, sys, idle, iowait, irq, softirq, steal uint64 }
	read := func() (stat, bool) {
		f, err := os.Open("/proc/stat")
		if err != nil {
			return stat{}, false
		}
		defer f.Close()
		var name string
		var s stat
		fmt.Fscan(f, &name, &s.user, &s.nice, &s.sys, &s.idle, &s.iowait, &s.irq, &s.softirq, &s.steal)
		return s, true
	}
	for {
		s1, ok1 := read()
		time.Sleep(time.Second)
		s2, ok2 := read()
		if !ok1 || !ok2 {
			continue
		}
		t1 := s1.user + s1.nice + s1.sys + s1.idle + s1.iowait + s1.irq + s1.softirq + s1.steal
		t2 := s2.user + s2.nice + s2.sys + s2.idle + s2.iowait + s2.irq + s2.softirq + s2.steal
		dt := float64(t2 - t1)
		didle := float64((s2.idle + s2.iowait) - (s1.idle + s1.iowait))
		var pct float64
		if dt > 0 {
			pct = (1 - didle/dt) * 100
		}
		latestCPU.Store(pct)
	}
}

// --- Metrics readers ---

func readMemInfo() (total, used, free, usage float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	var mTotal, mFree, mAvail, mBuf, mCached uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var name string
		var val uint64
		fmt.Sscanf(sc.Text(), "%s %d", &name, &val)
		switch name {
		case "MemTotal:":
			mTotal = val
		case "MemFree:":
			mFree = val
		case "MemAvailable:":
			mAvail = val
		case "Buffers:":
			mBuf = val
		case "Cached:":
			mCached = val
		}
	}
	const toGB = 1048576.0
	total = float64(mTotal) / toGB
	if mAvail > 0 {
		used = float64(mTotal-mAvail) / toGB
	} else {
		used = float64(mTotal-mFree-mBuf-mCached) / toGB
	}
	free = total - used
	if mTotal > 0 {
		usage = (used / total) * 100
	}
	return
}

func readDisk() (total, used, free, usage float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return
	}
	bs := uint64(st.Bsize)
	const toGB = 1 << 30
	total = float64(st.Blocks*bs) / toGB
	freeBytes := float64(st.Bfree*bs) / toGB
	free = float64(st.Bavail*bs) / toGB
	used = total - freeBytes
	if total > 0 {
		usage = (used / total) * 100
	}
	return
}

func readUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	var secs float64
	fmt.Sscanf(string(data), "%f", &secs)
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hrs := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hrs, mins)
	}
	return fmt.Sprintf("%dh %dm", hrs, mins)
}

func readOSName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return "Linux"
}

func readCPUModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "Unknown"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "Unknown"
}

func readCPUCores() int {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 1
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "processor") {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// --- Container collection ---

type dockerListItem struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	State   string   `json:"State"`
	Created int64    `json:"Created"`
	Ports   []struct {
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

type dockerStatsResult struct {
	CPUStats struct {
		CPUUsage       struct{ TotalUsage uint64 `json:"total_usage"` } `json:"cpu_usage"`
		SystemCPUUsage uint64                                           `json:"system_cpu_usage"`
		OnlineCPUs     int                                              `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage       struct{ TotalUsage uint64 `json:"total_usage"` } `json:"cpu_usage"`
		SystemCPUUsage uint64                                           `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
	} `json:"memory_stats"`
}

func dockerGET(path string, out interface{}) error {
	resp, err := dockerHTTP.Get("http://localhost" + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func containerAge(unixSec int64) string {
	d := time.Since(time.Unix(unixSec, 0))
	days := int(d.Hours()) / 24
	hrs := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hrs)
	}
	if hrs > 0 {
		return fmt.Sprintf("%dh %dm", hrs, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func collectDockerContainers() []ContainerInfo {
	var list []dockerListItem
	if err := dockerGET("/containers/json?all=1", &list); err != nil {
		return nil
	}
	results := make([]ContainerInfo, len(list))
	var wg sync.WaitGroup
	for i, item := range list {
		wg.Add(1)
		go func(idx int, c dockerListItem) {
			defer wg.Done()
			name := c.ID
			if len(name) > 12 {
				name = name[:12]
			}
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			seen := map[string]bool{}
			var ports []string
			for _, p := range c.Ports {
				var s string
				if p.PublicPort > 0 {
					s = fmt.Sprintf("%d:%d", p.PublicPort, p.PrivatePort)
				} else {
					s = fmt.Sprintf("%d", p.PrivatePort)
				}
				if !seen[s] {
					ports = append(ports, s)
					seen[s] = true
				}
			}
			var cpuPct float64
			var ramBytes int64
			if c.State == "running" {
				var st dockerStatsResult
				if err := dockerGET("/containers/"+c.ID+"/stats?stream=false", &st); err == nil {
					cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage - st.PreCPUStats.CPUUsage.TotalUsage)
					sysDelta := float64(st.CPUStats.SystemCPUUsage - st.PreCPUStats.SystemCPUUsage)
					cpus := st.CPUStats.OnlineCPUs
					if cpus == 0 {
						cpus = 1
					}
					if sysDelta > 0 {
						cpuPct = (cpuDelta / sysDelta) * float64(cpus) * 100
					}
					ramBytes = int64(st.MemoryStats.Usage)
				}
			}
			id := c.ID
			if len(id) > 12 {
				id = id[:12]
			}
			results[idx] = ContainerInfo{
				ID:      id,
				Name:    name,
				Image:   c.Image,
				Status:  strings.ToLower(c.State),
				Runtime: "docker",
				CPU:     cpuPct,
				RAM:     ramBytes,
				Ports:   ports,
				Uptime:  containerAge(c.Created),
			}
		}(i, item)
	}
	wg.Wait()
	return results
}

// collectK8sStats runs kubectl top pods -A and returns a map of
// "namespace/podname" → [cpuMillicores, ramBytes].
func collectK8sStats() map[string][2]int64 {
	out, err := exec.Command("kubectl", "top", "pods", "-A", "--no-headers").Output()
	if err != nil {
		return nil
	}
	stats := map[string][2]int64{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		ns, pod, cpuStr, memStr := f[0], f[1], f[2], f[3]

		var cpuMilli int64
		if strings.HasSuffix(cpuStr, "m") {
			v, _ := strconv.ParseInt(strings.TrimSuffix(cpuStr, "m"), 10, 64)
			cpuMilli = v
		} else {
			v, _ := strconv.ParseInt(cpuStr, 10, 64)
			cpuMilli = v * 1000
		}

		var memBytes int64
		switch {
		case strings.HasSuffix(memStr, "Gi"):
			v, _ := strconv.ParseFloat(strings.TrimSuffix(memStr, "Gi"), 64)
			memBytes = int64(v * 1024 * 1024 * 1024)
		case strings.HasSuffix(memStr, "Mi"):
			v, _ := strconv.ParseInt(strings.TrimSuffix(memStr, "Mi"), 10, 64)
			memBytes = v * 1024 * 1024
		case strings.HasSuffix(memStr, "Ki"):
			v, _ := strconv.ParseInt(strings.TrimSuffix(memStr, "Ki"), 10, 64)
			memBytes = v * 1024
		}
		stats[ns+"/"+pod] = [2]int64{cpuMilli, memBytes}
	}
	return stats
}

func collectK8sContainers() []ContainerInfo {
	out, err := exec.Command("kubectl", "get", "pods", "-A", "-o", "json").Output()
	if err != nil {
		return nil
	}
	var pl struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				Phase     string `json:"phase"`
				StartTime string `json:"startTime"`
			} `json:"status"`
			Spec struct {
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
					Ports []struct {
						ContainerPort int    `json:"containerPort"`
						Protocol      string `json:"protocol"`
					} `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil
	}

	stats := collectK8sStats() // may be nil if metrics-server not installed

	var results []ContainerInfo
	for _, pod := range pl.Items {
		phase := strings.ToLower(pod.Status.Phase)
		if phase == "succeeded" || phase == "failed" {
			phase = "exited"
		}
		var uptime string
		if pod.Status.StartTime != "" {
			if t, err := time.Parse(time.RFC3339, pod.Status.StartTime); err == nil {
				uptime = containerAge(t.Unix())
			}
		}

		// Per-pod CPU/RAM from kubectl top (shared by all containers in the pod)
		var podCPU float64
		var podRAM int64
		if stats != nil {
			if s, ok := stats[pod.Metadata.Namespace+"/"+pod.Metadata.Name]; ok {
				podCPU = float64(s[0]) / 10 // millicores → % of 1 core
				podRAM = s[1]
			}
		}

		for _, spec := range pod.Spec.Containers {
			var ports []string
			for _, p := range spec.Ports {
				proto := strings.ToLower(p.Protocol)
				if proto == "" {
					proto = "tcp"
				}
				ports = append(ports, fmt.Sprintf("%d/%s", p.ContainerPort, proto))
			}
			// ID encodes exec target: namespace/podname/containername
			cid := pod.Metadata.Namespace + "/" + pod.Metadata.Name + "/" + spec.Name
			results = append(results, ContainerInfo{
				ID:        cid,
				Name:      spec.Name,
				Image:     spec.Image,
				Status:    phase,
				Runtime:   "k8s",
				Namespace: pod.Metadata.Namespace,
				CPU:       podCPU,
				RAM:       podRAM,
				Ports:     ports,
				Uptime:    uptime,
			})
		}
	}
	return results
}

func collectContainers() []ContainerInfo {
	docker := collectDockerContainers()
	k8s := collectK8sContainers()
	if len(docker) == 0 && len(k8s) == 0 {
		return nil
	}
	return append(docker, k8s...)
}

// --- Telemetry ---

func postTelemetry() {
	cpu := latestCPU.Load().(float64)
	ramTotal, ramUsed, ramFree, ramUsage := readMemInfo()
	diskTotal, diskUsed, diskFree, diskUsage := readDisk()
	hostname, _ := os.Hostname()

	p := telemetryPayload{
		Token:      cfg.Token,
		Hostname:   hostname,
		IP:         localIP(),
		OS:         readOSName(),
		CPUModel:   readCPUModel(),
		CPUCores:   readCPUCores(),
		CPUUsage:   cpu,
		RAMTotal:   ramTotal,
		RAMUsed:    ramUsed,
		RAMFree:    ramFree,
		RAMUsage:   ramUsage,
		DiskTotal:  diskTotal,
		DiskUsed:   diskUsed,
		DiskFree:   diskFree,
		DiskUsage:  diskUsage,
		Uptime:     readUptime(),
		Containers: collectContainers(),
	}
	body, _ := json.Marshal(p)
	resp, err := httpClient.Post(agentURL+"/agent/report", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("telemetry: %v", err)
		return
	}
	resp.Body.Close()
}

func telemetryLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		postTelemetry()
	}
}

// --- Command poll loop ---

func commandLoop() {
	currentDir, _ := os.UserHomeDir()
	for {
		resp, err := httpClient.Get(agentURL + "/agent/cmd/wait/" + cfg.Token + "?v=2")
		if err != nil {
			log.Printf("poll: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		if resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			continue
		}
		var cmd cmdResponse
		if err := json.NewDecoder(resp.Body).Decode(&cmd); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		switch cmd.Type {
		case "shell":
			if cmd.SessionID != "" {
				go handleShellSession(cmd.SessionID, cmd.ContainerID, cmd.ShellMode)
			}
		case "exec":
			if cmd.Command != "" && cmd.CommandID != "" {
				executeCommand(cmd.Command, cmd.CommandID, &currentDir)
			}
		default:
			// backward-compat: server without type field
			if cmd.Command != "" && cmd.CommandID != "" {
				executeCommand(cmd.Command, cmd.CommandID, &currentDir)
			}
		}
	}
}

// --- Command execution ---

func executeCommand(command, commandID string, currentDir *string) {
	// Remap interactive commands to non-interactive equivalents.
	execCmd := command
	switch {
	case command == "top" || (strings.HasPrefix(command, "top ") && !strings.Contains(command, "-b")):
		execCmd = "top -b -n 1"
	case command == "htop" || strings.HasPrefix(command, "htop "):
		execCmd = "TERM=dumb htop --no-color 2>/dev/null || top -b -n 1"
	case command == "vim" || command == "vi" || command == "nano" || command == "pico" || command == "emacs":
		execCmd = "echo '[INFO] Interactive editors not supported in this mode. Use the Shell (PTY) tab.'"
	case strings.HasPrefix(command, "vim ") || strings.HasPrefix(command, "vi ") || strings.HasPrefix(command, "nano "):
		execCmd = "echo '[INFO] Interactive editors not supported. Use the Shell (PTY) tab.'"
	case command == "less" || command == "more":
		execCmd = "cat"
	case strings.HasPrefix(command, "less "):
		execCmd = "cat " + command[5:]
	case strings.HasPrefix(command, "more "):
		execCmd = "cat " + command[5:]
	case strings.HasPrefix(command, "man "):
		execCmd = "MANPAGER=cat " + command
	case command == "watch" || strings.HasPrefix(command, "watch "):
		execCmd = "echo '[INFO] watch not supported.'"
	}

	script := fmt.Sprintf(
		`cd %q 2>/dev/null; eval %q 2>&1; echo "<<<MGNTPWD:$(pwd)>>>"`,
		*currentDir, execCmd,
	)
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"DEBIAN_FRONTEND=noninteractive",
		"COLUMNS=220",
		"LINES=50",
	)

	rawOut, err := cmd.Output()
	status := "success"
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			status = "error"
			// cmd.Output() returns stdout; stderr is in exitErr.Stderr
			rawOut = append(rawOut, exitErr.Stderr...)
		}
	}

	output := string(rawOut)
	const marker = "<<<MGNTPWD:"
	if idx := strings.LastIndex(output, marker); idx >= 0 {
		end := strings.Index(output[idx:], ">>>")
		if end >= 0 {
			newDir := strings.TrimSpace(output[idx+len(marker) : idx+end])
			if info, statErr := os.Stat(newDir); statErr == nil && info.IsDir() {
				*currentDir = newDir
			}
			output = output[:idx]
		}
	}
	output = strings.TrimRight(output, "\n")

	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		if out, e := exec.Command("whoami").Output(); e == nil {
			user = strings.TrimSpace(string(out))
		} else {
			user = "agent"
		}
	}
	home, _ := os.UserHomeDir()
	dir := *currentDir
	if dir == home {
		dir = "~"
	} else if strings.HasPrefix(dir, home+"/") {
		dir = "~" + dir[len(home):]
	}
	prompt := fmt.Sprintf("%s@%s:%s$", user, hostname, dir)

	result := map[string]string{
		"token":      cfg.Token,
		"command_id": commandID,
		"output":     output,
		"status":     status,
		"prompt":     prompt,
	}
	body, _ := json.Marshal(result)
	resp, err := httpClient.Post(agentURL+"/agent/report/result", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("result post: %v", err)
		return
	}
	resp.Body.Close()
}

// --- PTY helpers ---

func openPTY() (master *os.File, slaveName string, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), ioctlTIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		return nil, "", fmt.Errorf("TIOCGPTN: %w", errno)
	}
	var zero int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), ioctlTIOCSPTLCK, uintptr(unsafe.Pointer(&zero))); errno != 0 {
		master.Close()
		return nil, "", fmt.Errorf("TIOCSPTLCK: %w", errno)
	}
	return master, fmt.Sprintf("/dev/pts/%d", n), nil
}

func resizePTY(master *os.File, rows, cols uint16) {
	ws := winsize{Row: rows, Col: cols}
	syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))
}

// --- PTY shell session ---

func handleShellSession(sessionID, containerID, shellMode string) {
	wsURL := fmt.Sprintf("wss://mgnt-server.local:%s/agent/shell/%s/ws?token=%s",
		cfg.ServerTLSPort, sessionID, cfg.Token)

	conn, _, err := wsDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("shell ws dial %s: %v", sessionID, err)
		return
	}

	master, slaveName, err := openPTY()
	if err != nil {
		log.Printf("shell pty: %v", err)
		conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"error","message":"PTY unavailable on this server"}`))
		conn.Close()
		return
	}

	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		log.Printf("shell slave: %v", err)
		conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"error","message":"Cannot open PTY slave"}`))
		conn.Close()
		return
	}

	// Build the command based on whether this is a container session.
	// K8s containers encode the target as "namespace/podname/containername".
	// Docker containers use a plain short container ID (no slashes).
	var shellCmd *exec.Cmd
	var needsCtty bool
	switch {
	case containerID != "" && strings.Count(containerID, "/") == 2:
		// K8s: namespace/podname/containername
		parts := strings.SplitN(containerID, "/", 3)
		ns, pod, ctr := parts[0], parts[1], parts[2]
		if shellMode == "logs" {
			shellCmd = exec.Command("kubectl", "logs", "-f", "--tail=200", "-n", ns, pod, "-c", ctr)
			needsCtty = false
		} else {
			shellCmd = exec.Command("kubectl", "exec", "-it", pod, "-n", ns, "-c", ctr, "--", "sh")
			needsCtty = true
		}
	case containerID != "" && shellMode == "logs":
		shellCmd = exec.Command("docker", "logs", "-f", "--tail=200", containerID)
		needsCtty = false
	case containerID != "":
		shellCmd = exec.Command("docker", "exec", "-it", containerID, "sh")
		needsCtty = true
	default:
		shellCmd = exec.Command("bash", "-i")
		needsCtty = true
	}
	shellCmd.Env = append(os.Environ(), "TERM=xterm-256color")
	shellCmd.Stdin, shellCmd.Stdout, shellCmd.Stderr = slave, slave, slave
	if needsCtty {
		shellCmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    0,
		}
	} else {
		shellCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	bash := shellCmd
	if err := bash.Start(); err != nil {
		log.Printf("shell.Start failed: %v (slave=%s)", err, slaveName)
		slave.Close()
		master.Close()
		msg := fmt.Sprintf(`{"type":"error","message":"Cannot start shell: %s"}`, err.Error())
		conn.WriteMessage(websocket.TextMessage, []byte(msg))
		conn.Close()
		return
	}
	slave.Close() // parent closes slave; child holds it

	var wsMu sync.Mutex
	wsSend := func(mt int, data []byte) {
		wsMu.Lock()
		defer wsMu.Unlock()
		conn.WriteMessage(mt, data)
	}

	// PTY → WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				wsSend(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				break
			}
		}
		conn.Close()
	}()

	// WebSocket → PTY
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		switch mt {
		case websocket.BinaryMessage:
			master.Write(data)
		case websocket.TextMessage:
			var msg struct {
				Type string `json:"type"`
				Rows uint16 `json:"rows"`
				Cols uint16 `json:"cols"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				resizePTY(master, msg.Rows, msg.Cols)
			}
		}
	}

	bash.Process.Kill()
	master.Close()
	bash.Wait()
}
