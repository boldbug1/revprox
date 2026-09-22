package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ReverseProxy struct {
	targetURL *url.URL
	ignoreKeys []string
	transport http.RoundTripper
}

var defaultIgnoreKeys []string = []string{"Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Trailer", "Te", "Proxy-Authenticate", "Keep-Alive"}
var addr string = "http://127.0.0.1:9001/"
var port string = ":3000"

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
		transport: transport,
	}
}

func (p *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Host = p.targetURL.Host
	u.Scheme = p.targetURL.Scheme

	target := u.String()

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)

	if err != nil {
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		return
	}

	for key, values := range r.Header {
		for _, value := range values {
			outReq.Header.Add(key, value)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host= r.RemoteAddr
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
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	p.delKeys(w.Header())

	w.WriteHeader(resp.StatusCode)

	io.Copy(w, resp.Body)
}

func main() {
	target, err := url.Parse(addr)

	if err != nil {
		log.Fatalf("Invalid url: %v\n", err)
	}
	rproxy := NewReverseProxy(target,nil)

	log.Printf("proxy listening on %s, forwarding to %s", port, addr)
	if err := http.ListenAndServe(port, rproxy); err != nil {
		log.Fatalf("Proxy failed: %v", err)
	}

}
