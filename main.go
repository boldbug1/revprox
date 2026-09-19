package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
)

type ReverseProxy struct {
	targetURL *url.URL
}

var ignoreKeys []string = []string{"Connection", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Trailer", "Te", "Proxy-Authenticate", "Keep-Alive"}
var addr string = "http://127.0.0.1:9001/"
var port string = ":3000"

func delKeys(h http.Header) {

	for _, keys := range ignoreKeys {
		h.Del(keys)
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
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		log.Printf("Error while extracting host: %v", err)
		return
	}

	existingHost := r.Header.Get("X-Forwarded-For")

	if existingHost == "" {
		outReq.Header.Set("X-Forwarded-For", host)
	} else {
		outReq.Header.Set("X-Forwarded-For", existingHost+", "+host)
	}

	delKeys(outReq.Header)

	outReq.ContentLength = r.ContentLength
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
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

	delKeys(w.Header())

	w.WriteHeader(resp.StatusCode)

	io.Copy(w, resp.Body)
}

func main() {

	target, err := url.Parse(addr)

	if err != nil {
		log.Fatalf("Invalid url: %v\n", err)
	}
	rproxy := ReverseProxy{
		targetURL: target,
	}

	log.Printf("proxy listening on %s, forwarding to %s", port, addr)
	if err := http.ListenAndServe(port, &rproxy); err != nil {
		log.Fatalf("Proxy failed: %v", err)
	}

}
