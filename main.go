package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
)

type Config struct {
	Address string
	Port    int
	Apps    []string
}

var conf Config

func SetConf(name string) {
	log.Printf("parse config file %s", name)
	file, _ := os.Open(name)
	defer file.Close()

	decoder := json.NewDecoder(file)
	conf = Config{}
	err := decoder.Decode(&conf)
	if err != nil {
		log.Fatal("error:", err)
		os.Exit(1)
	}
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	log.Printf("recieved http request")

	apps := conf.Apps

	statuses := ""
	for _, app := range apps {
		statusFile := app + "/status.gohc"

		if app == "//self" {
			statusFile = "status.gohc"
		}

		content, err := ioutil.ReadFile(statusFile)

		if err != nil {
			log.Fatal("error:", err)
			statuses += "can't read file: " + statusFile + "\n"
		} else {
			statuses += string(content) + "\n"
		}

	}

	w.Write([]byte(statuses))
}

func main() {
	log.Printf("starting health check service")

	SetConf("app.json")

	http.HandleFunc("/", HealthCheck)

	err := http.ListenAndServe(conf.Address+":"+strconv.Itoa(conf.Port), nil)

	if errors.Is(err, http.ErrServerClosed) {
		log.Println("server closed")
	} else if err != nil {
		log.Fatalf("error starting server: %s", err)
		os.Exit(1)
	}
}
