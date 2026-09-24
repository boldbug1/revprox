package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"io"
)

type ReverseProxy struct {
	targetURL  *url.URL
	ignoreKeys []string
	transport  http.RoundTripper
}

var defaultIgnoreKeys []string = []string{"Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Trailer", "Te", "Proxy-Authenticate", "Keep-Alive"}
var addr string = "http://127.0.0.1:9001/api/"
var port string = ":3000"

var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

func (p *ReverseProxy) delKeys(h http.Header) {
	for _, val := range h.Values("Connection") {
		for _, token := range strings.Split(val, ",") {
			token = strings.TrimSpace(token)
			if token != "" {
				h.Del(token)
			}
		}
	}
	for _, keys := range p.ignoreKeys {
		h.Del(keys)
	}

	h.Del("Connection")
}

func NewReverseProxy(target *url.URL, ignoreKeys []string) *ReverseProxy {
	if ignoreKeys == nil {
		ignoreKeys = defaultIgnoreKeys
	}

	transport := &http.Transport{ResponseHeaderTimeout: 5 * time.Second}
	return &ReverseProxy{
		targetURL:  target,
		ignoreKeys: ignoreKeys,
		transport:  transport,
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func (p *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Host = p.targetURL.Host
	u.Scheme = p.targetURL.Scheme
	u.Path = singleJoiningSlash(p.targetURL.Path, r.URL.Path)
	u.RawQuery = r.URL.RawQuery

	target := u.String()

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)

	if err != nil {
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		return
	}

	outReq.Header = r.Header.Clone()

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	existingHost := r.Header.Get("X-Forwarded-For")

	if existingHost == "" {
		outReq.Header.Set("X-Forwarded-For", host)
	} else {
		outReq.Header.Set("X-Forwarded-For", existingHost+", "+host)
	}

	outReq.Header.Set("X-Forwarded-Host", r.Host)
	proto := "http"
	if r.TLS != nil {
		proto = "https"
	}
	outReq.Header.Set("X-Forwarded-Proto", proto)

	p.delKeys(outReq.Header)

	outReq.ContentLength = r.ContentLength
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			log.Printf("upstream request timed out: %v", err)
			return
		}
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		log.Printf("upstream request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		w.Header()[key] = values
	}

	p.delKeys(w.Header())

	w.WriteHeader(resp.StatusCode)

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	flusher, _ := w.(http.Flusher)
	bufPtr := bufferPool.Get().(*[]byte)
	defer bufferPool.Put(bufPtr)
	buf := *bufPtr
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				break // Client disconnected
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
        		log.Printf("stream copy from upstream failed after partial write: %v", err)
    		}
			break 
		}
	}
}

func main() {
	target, err := url.Parse(addr)

	if err != nil {
		log.Fatalf("Invalid url: %v\n", err)
	}
	rproxy := NewReverseProxy(target, nil)

	log.Printf("proxy listening on %s, forwarding to %s", port, addr)
	server := &http.Server{
		Addr:              port,
		Handler:           rproxy,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
	go func(){
		log.Printf("proxy listening on %s", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err,http.ErrServerClosed) {
			log.Fatalf("Proxy failed: %v", err)
		}
	}();

	<-ctx.Done()
	shutCtx,cancel := context.WithTimeout(context.Background(),10*time.Second)
	defer cancel()
	log.Println("Shuting down.....")
	if err:=server.Shutdown(shutCtx);err!=nil{
		log.Printf("shutdown error: %v",err)
	}

}
