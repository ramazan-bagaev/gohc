package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
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

func SetLog() {
	file, err := os.OpenFile("gohc.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
	}

	log.SetOutput(file)

	log.Println("set up logger")
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
			statuses += app + " - can't read file: " + statusFile + "\n"
		} else {
			statuses += string(content) + "\n"
		}

	}

	w.Write([]byte(statuses))
}

func UpdateStatus(ticker time.Ticker) {
	for range ticker.C {
		log.Printf("update status by ticker")
		ioutil.WriteFile("status.gohc", []byte("gohc - ok; last-update(utc): "+time.Now().UTC().Format(time.StampMilli)), 0666)
	}
}

func main() {
	SetLog()
	log.Printf("starting health check service")
	SetConf("app.json")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	go UpdateStatus(*ticker)

	http.HandleFunc("/", HealthCheck)

	err := http.ListenAndServe(conf.Address+":"+strconv.Itoa(conf.Port), nil)

	if errors.Is(err, http.ErrServerClosed) {
		log.Println("server closed")
	} else if err != nil {
		log.Fatalf("error starting server: %s", err)
		os.Exit(1)
	}
}
