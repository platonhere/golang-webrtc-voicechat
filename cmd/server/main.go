package main

import (
	"log"
	"net/http"

	"voicechat/internal/ws"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/ws", ws.HandleWebSocket)
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("static")))

	addr := ":8080"
	log.Printf("Starting server on %s\n", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
