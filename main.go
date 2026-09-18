package main

import (
	"net/http"
	"io"
	"log"
)


type ReverseProxy struct {
	targetURL string
}

func (p *ReverseProxy) ServeHTTP(w http.ResponseWriter,r *http.Request){
	target := p.targetURL + r.URL.RequestURI()

	outReq,err:= http.NewRequestWithContext(r.Context(),r.Method,target,r.Body)

	if err != nil{
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		return
	}
	
	for key, values := range r.Header {
		for _, value := range values {
			outReq.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()


	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)

	io.Copy(w, resp.Body)
}

func main() {

	rproxy := ReverseProxy{
		targetURL:"http://127.0.0.1:9001" ,
	}

	if err:= http.ListenAndServe(":3000",&rproxy);err!=nil{
		log.Fatalf("Proxy failed: %v", err)
	}

}
