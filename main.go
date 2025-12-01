package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Config via flags and env
var (
	addr      = flag.String("addr", ":"+getEnv("PORT", "8080"), "address to listen on")
	assetsDir = flag.String("assets", getEnv("ASSETS_DIR", "./"), "path to assets directory (contains templates/ and static/)")
	backend   = getEnv("BACKEND_URL", "http://localhost:8081")
)

// PageData passed to templates
type PageData struct {
	BackendURL  string
	ClientIP    string
	ServerName  string
	ServerAddrs string
}

func main() {
	flag.Parse()

	// Identify this server (hostname + non-loopback addresses)
	hostname, _ := os.Hostname()
	addrs := serverAddrs()
	serverID := fmt.Sprintf("%s (%s)", hostname, addrs)

	templatesPath := filepath.Join(*assetsDir, "templates", "index.html")
	tmpl, err := template.ParseFiles(templatesPath)
	if err != nil {
		log.Fatalf("parse templates: %v (looked for %s)", err, templatesPath)
	}

	mux := http.NewServeMux()

	// Serve static files from assetsDir/static at /static/
	staticDir := filepath.Join(*assetsDir, "static")
	fileServer := http.FileServer(http.Dir(staticDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// Root handler - include client IP and server identity in template data
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Also expose server identity in a response header for easier debugging/tracing
		w.Header().Set("X-Server", serverID)

		data := PageData{
			BackendURL:  backend,
			ClientIP:    clientIP(r),
			ServerName:  hostname,
			ServerAddrs: addrs,
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
			log.Println("template execute:", err)
		}
	})

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server", serverID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Lightweight API proxy to backend to avoid CORS when running separate frontend and backend farms.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server", serverID)

		if backend == "" {
			http.Error(w, "backend not configured", http.StatusBadGateway)
			return
		}
		if !allowedMethod(r.Method) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		u, err := url.Parse(backend)
		if err != nil {
			http.Error(w, "bad backend url", http.StatusInternalServerError)
			log.Println("bad BACKEND_URL:", err)
			return
		}

		// Build backend URL: backend + remainder after /api
		relPath := strings.TrimPrefix(r.URL.Path, "/api")
		backendPath := filepath.ToSlash(filepath.Join(u.Path, relPath))
		target := url.URL{
			Scheme:   u.Scheme,
			Host:     u.Host,
			Path:     backendPath,
			RawQuery: r.URL.RawQuery,
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
		if err != nil {
			http.Error(w, "failed create request", http.StatusInternalServerError)
			return
		}
		copyHeaders(r.Header, req.Header)
		req.Header.Set("X-Forwarded-For", clientIP(r))

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "backend unavailable", http.StatusBadGateway)
			log.Println("proxy error:", err)
			return
		}
		defer resp.Body.Close()

		// propagate headers (excluding hop-by-hop)
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	server := &http.Server{
		Addr:         *addr,
		Handler:      loggingMiddleware(mux, serverID),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start listener
	log.Printf("starting frontend on %s, assets=%s, backend=%s, server=%s", *addr, *assetsDir, backend, serverID)
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	// Graceful shutdown
	idleConnsClosed := make(chan struct{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		<-c
		log.Println("shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("HTTP server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()

	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}

	<-idleConnsClosed
	log.Println("server stopped")
}

func allowedMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func loggingMiddleware(next http.Handler, serverID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// ensure server identity is always present in responses
		w.Header().Set("X-Server", serverID)
		ww := &statusResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		log.Printf("%s %s %d %s %s\n", r.Method, r.URL.Path, ww.status, time.Since(start), r.RemoteAddr)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusResponseWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func copyHeaders(from http.Header, to http.Header) {
	for k, vv := range from {
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade":
			continue
		}
		for _, v := range vv {
			to.Add(k, v)
		}
	}
}

func clientIP(r *http.Request) string {
	// Prefer X-Forwarded-For if present (common when behind load balancers or proxies).
	// Note: Only trust X-Forwarded-For when you control/know the fronting proxy.
	if x := r.Header.Get("X-Forwarded-For"); x != "" {
		parts := strings.Split(x, ",")
		return strings.TrimSpace(parts[0])
	}
	// Fallback to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// serverAddrs returns a comma-separated list of non-loopback IPv4 addresses for the host.
// If none found, returns "unknown".
func serverAddrs() string {
	var addrs []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return "unknown"
	}
	for _, iface := range ifaces {
		// skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		a, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range a {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				// skip IPv6 for now; keep output short and common
				continue
			}
			addrs = append(addrs, ip.String())
		}
	}
	if len(addrs) == 0 {
		return "unknown"
	}
	return strings.Join(addrs, ",")
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}