package main

import (
	"github.com/ramazan-bagaev/gohc/client"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	reporter, err := client.Start("gohc-host-demo", client.Options{Check: func() client.Health {
		return client.Health{Status: "healthy", Message: "Go demo running directly on the host"}
	}, OnError: func(err error) { log.Print(err) }})
	if err != nil {
		log.Fatal(err)
	}
	defer reporter.Stop()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
