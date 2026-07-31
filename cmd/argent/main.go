package main

import (
	"log"
	"os"
)

func main() {
	// 12-factor：端口可由环境变量覆盖。
	port := os.Getenv("ARGENT_PORT")
	if port == "" {
		port = "8889"
	}

	r := Build()

	addr := ":" + port
	log.Printf("argent-go listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
