package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type App struct {
	Path string
	Name string
}

type Config struct {
	Address string
	Port    int
	Apps    []App
}

var Conf Config

func SetConf(name string) {
	log.Printf("parse config file %s", name)
	file, _ := os.Open(name)
	defer file.Close()

	decoder := json.NewDecoder(file)
	Conf = Config{}
	err := decoder.Decode(&Conf)
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
	log.Printf("recieved http request on /")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	apps := Conf.Apps

	w.Write([]byte(GetStatuses(apps)))
}

func Log(w http.ResponseWriter, r *http.Request) {
	log.Printf("recieved http request on /log")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	r.ParseForm()

	apps := Conf.Apps

	logName := r.Form.Get("log_name")
	appName := r.Form.Get("app_name")

	for _, app := range apps {
		if app.Name == appName {
			w.Write([]byte(getLogContent(logName, app)))
			break
		}
	}
}

func GetStatuses(apps []App) string {
	statuses := ""
	for _, app := range apps {

		if app.Name == "//self" {
			app.Path = "."
		}

		statuses += "<h1>" + app.Name + "</h1>"

		appFiles, err := ioutil.ReadDir(app.Path)

		if err != nil {
			log.Fatalf("can't open dir=%s; %s", app.Name, err)
		}

		for _, file := range appFiles {
			if filepath.Ext(file.Name()) == ".gohc" {
				statuses += getStatus(file.Name(), app)
			}
			if filepath.Ext(file.Name()) == ".log" {
				statuses += getShortLog(file.Name(), app, Conf)
			}
		}

		statuses += "------------------------------------------<br>"
	}

	return statuses
}

func getStatus(file string, app App) string {
	content, err := ioutil.ReadFile(app.Path + "/" + file)

	if err != nil {
		log.Fatal("error:", err)
		return file + ": can't read file <br>"
	} else {
		return "<h3>" + file + "</h3>" + string(content) + "<br><br>*******************<br>"
	}
}

func getShortLog(file string, app App, conf Config) string {
	content, err := ioutil.ReadFile(app.Path + "/" + file)

	if err != nil {
		log.Fatal("error:", err)
		return file + ": can't read file <br>"
	} else {
		res := ""
		splits := strings.Split(string(content), "\n")
		for i, line := range splits {
			if len(splits)-i < 7 {
				res += line + "<br>"
			}
		}

		res += "<a href=http://" + conf.Address + ":" + strconv.Itoa(conf.Port) + "/log?app_name=" + app.Name + "&log_name=" + file + ">full log</a>"
		return "<h3>" + file + "</h3>" + string(res) + "<br><br>*******************<br>"
	}
}

func getLogContent(file string, app App) string {
	content, err := ioutil.ReadFile(app.Path + "/" + file)
	if err != nil {
		log.Fatal("error:", err)
		return file + ": can't read file <br>"
	} else {
		res := ""
		splits := strings.Split(string(content), "\n")
		for _, line := range splits {
			res += line + "<br>"
		}
		return "<h3>" + file + "</h3>" + string(res)
	}
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
	http.HandleFunc("/log", Log)

	err := http.ListenAndServe(Conf.Address+":"+strconv.Itoa(Conf.Port), nil)

	if errors.Is(err, http.ErrServerClosed) {
		log.Println("server closed")
	} else if err != nil {
		log.Fatalf("error starting server: %s", err)
		os.Exit(1)
	}
}
