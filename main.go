package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// version 是编译期可覆盖的版本号。必须是变量（不能是 const），这样 build.cmd 与
// GitHub Actions 才能用 -ldflags "-X main.version=x.y.z" 把版本号写进二进制。
// 不带 ldflags 直接 go build 时使用下面的默认值。
var version = "1.0.1"

//go:embed web/client.html
var clientPage string

//go:embed web/admin.html
var adminPage string

//go:embed web/probe.html
var probePage string

// virtualAdapterHints 用于过滤掉不能用于局域网访问的网卡。
// 命中的一律不展示给用户，避免给出一个别人连不上的地址。
var virtualAdapterHints = []string{
	"vmware", "virtualbox", "vbox", "vethernet", "hyper-v", "loopback",
	"bluetooth", "tap-", "tun", "tap0", "docker", "wsl", "npcap", "hamachi",
	"zerotier", "tailscale", "radmin", "clash", "singbox", "sing-box",
	"openvpn", "wireguard", "vpn", "proxy", "ppp", "teredo", "isatap", "6to4",
	"virtual", "pseudo", "tunnel", "nordlynx", "utun", "vgate",
}

type App struct {
	store     *Store
	token     string
	hostname  string
	exeDir    string
	uploadDir string
	dataFile  string
	port      int
	maxUpload int64
}

func main() {
	port := flag.Int("port", 5421, "监听端口，被占用时自动向后尝试")
	maxGB := flag.Int("max-upload-gb", 4, "单个文件上传大小上限（GB）")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开管理页")
	uploadDir := flag.String("upload-dir", "", "客户端上传文件的保存目录，默认为 exe 同级 uploads")
	flag.Parse()

	exeDir := resolveExeDir()
	app := &App{
		store:     nil,
		token:     randomToken(),
		hostname:  resolveHostname(),
		exeDir:    exeDir,
		uploadDir: *uploadDir,
		dataFile:  filepath.Join(exeDir, "data", "items.json"),
		maxUpload: int64(*maxGB) << 30,
	}
	if app.uploadDir == "" {
		app.uploadDir = filepath.Join(exeDir, "uploads")
	}
	if abs, err := filepath.Abs(app.uploadDir); err == nil {
		app.uploadDir = abs
	}

	if err := os.MkdirAll(app.uploadDir, 0o755); err != nil {
		log.Fatalf("无法创建上传目录 %s: %v", app.uploadDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(app.dataFile), 0o755); err != nil {
		log.Fatalf("无法创建数据目录 %s: %v", filepath.Dir(app.dataFile), err)
	}

	app.store = NewStore(app.dataFile)
	if err := app.store.Load(); err != nil {
		log.Printf("加载共享列表失败: %v", err)
	}

	listener, actualPort, err := listenFree(*port)
	if err != nil {
		log.Fatalf("%v", err)
	}
	app.port = actualPort

	server := &http.Server{
		Handler:           app.routes(),
		ReadHeaderTimeout: 20 * time.Second,
	}

	printBanner(app)

	if !*noBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(fmt.Sprintf("http://127.0.0.1:%d/admin", app.port))
		}()
	}

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务异常退出: %v", err)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// 客户端页面与公开接口：局域网内任何设备都能访问
	mux.HandleFunc("/api/items", a.handleItems)
	mux.HandleFunc("/api/info", a.handlePublicInfo)
	mux.HandleFunc("/api/text", a.handlePublishText)
	mux.HandleFunc("/api/upload", a.handleUpload)
	mux.HandleFunc("/api/probe/upload", a.handleProbeUpload)
	// 带斜杠的是子树模式，末尾可以跟文件名（/api/probe/download/测试.apk）；
	// 不带斜杠的那条保留，让旧链接和手输地址依然能用。
	mux.HandleFunc("/api/probe/download", a.handleProbeDownload)
	mux.HandleFunc("/api/probe/download/", a.handleProbeDownload)
	mux.HandleFunc("/api/download/", a.handleDownload)
	mux.HandleFunc("/probe.html", a.page(probePage))
	mux.HandleFunc("/client.html", a.page(clientPage))

	// 浏览器总会自动请求 /favicon.ico。老安卓上这个 404 会在控制台里刷一行错误，
	// 我们直接回一个空的 204，既不报错也不额外传字节。
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// 管理端：只允许本机访问，且必须带令牌
	mux.HandleFunc("/admin", a.handleAdminPage)
	mux.HandleFunc("/admin.html", a.handleAdminPage)
	mux.HandleFunc("/api/admin/info", a.adminOnly(a.handleAdminInfo))
	mux.HandleFunc("/api/admin/pick", a.adminOnly(a.handleAdminPick))
	mux.HandleFunc("/api/admin/files", a.adminOnly(a.handleAdminFiles))
	mux.HandleFunc("/api/admin/text", a.adminOnly(a.handleAdminPublishText))
	mux.HandleFunc("/api/admin/remove", a.adminOnly(a.handleAdminRemove))
	mux.HandleFunc("/api/admin/reveal", a.adminOnly(a.handleAdminReveal))
	mux.HandleFunc("/api/admin/open-upload-dir", a.adminOnly(a.handleOpenUploadDir))

	mux.HandleFunc("/", a.handleRoot)

	return logRequests(mux)
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	a.page(clientPage)(w, r)
}

func (a *App) page(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 老安卓浏览器对缓存非常激进，页面一律不缓存，避免改了前端它还在用旧的
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		_, _ = w.Write([]byte(body))
	}
}

func (a *App) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "管理页仅允许在运行本服务的电脑上打开", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}

	page := strings.ReplaceAll(adminPage, "__FS_TOKEN__", a.token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	_, _ = w.Write([]byte(page))
}

// adminOnly 保证管理端接口只有本机浏览器能调用。
// 双重校验：来源必须是回环地址，且必须带上页面注入的令牌，防止普通网页跨站伪造请求。
func (a *App) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.Error(w, "该接口仅允许本机调用", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-FS-Token") != a.token {
			http.Error(w, "令牌校验失败，请刷新管理页重试", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/items") {
			log.Printf("%s %s %s %s", clientAddr(r), r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolveExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return "."
		}
		return cwd
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func resolveHostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "FileShare"
	}
	return strings.TrimSpace(name)
}

func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func listenFree(start int) (net.Listener, int, error) {
	var lastErr error
	for port := start; port < start+50 && port <= 65535; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			return listener, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("端口 %d 起的 50 个端口都被占用，请用 -port 指定其他端口（最后一次错误: %v）", start, lastErr)
}

func openBrowser(url string) {
	// rundll32 走系统默认浏览器，避免 cmd start 的引号转义问题
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		log.Printf("自动打开浏览器失败，请手动访问 %s", url)
	}
}

// lanURLs 枚举可用于局域网访问的地址。已在真实网卡上的地址排前面。
func lanURLs(port int) []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	type entry struct {
		url   string
		rank  int
		index int
	}
	var entries []entry

	for idx, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualAdapter(iface.Name) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			// /31 和 /32 是点对点链路（隧道、虚拟网关），不是能给平板访问的局域网地址
			if ones, bits := ipNet.Mask.Size(); bits == 32 && ones >= 31 {
				continue
			}
			entries = append(entries, entry{
				url:   fmt.Sprintf("http://%s:%d/client.html", ip.String(), port),
				rank:  privateRank(ip),
				index: idx,
			})
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rank != entries[j].rank {
			return entries[i].rank < entries[j].rank
		}
		return entries[i].index < entries[j].index
	})

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.url)
	}
	return out
}

// privateRank 越小越优先：192.168.x / 10.x 这类家用和办公网段排前面。
func privateRank(ip net.IP) int {
	switch {
	case ip[0] == 192 && ip[1] == 168:
		return 0
	case ip[0] == 10:
		return 1
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return 2
	default:
		return 3
	}
}

func isVirtualAdapter(name string) bool {
	lower := strings.ToLower(name)
	for _, hint := range virtualAdapterHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func printBanner(app *App) {
	urls := lanURLs(app.port)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Printf("  局域网文件共享  v%s\n", version)
	fmt.Println("========================================")
	fmt.Printf("  本机主机名 : %s\n", app.hostname)
	fmt.Printf("  监听端口   : %d\n", app.port)
	fmt.Printf("  上传保存到 : %s\n", app.uploadDir)
	fmt.Printf("  共享列表   : %s\n", app.dataFile)
	fmt.Println("  --------------------------------------")
	fmt.Printf("  管理页（本机）: http://127.0.0.1:%d/admin\n", app.port)
	if len(urls) == 0 {
		fmt.Println("  未检测到可用于局域网访问的网卡地址")
	} else {
		fmt.Println("  客户端访问地址（发给平板）:")
		for _, url := range urls {
			fmt.Printf("    %s\n", url)
		}
	}
	fmt.Println("  --------------------------------------")
	fmt.Println("  兼容性探测页: /probe.html （建议先在平板上打开一次）")
	fmt.Println("  按 Ctrl+C 停止分享")
	fmt.Println("========================================")
	fmt.Println()
}
