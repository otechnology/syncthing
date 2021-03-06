package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/calmh/syncthing/scanner"
	"github.com/codegangsta/martini"
	"github.com/codegangsta/martini-contrib/auth"
)

type guiError struct {
	Time  time.Time
	Error string
}

var (
	configInSync = true
	guiErrors    = []guiError{}
	guiErrorsMut sync.Mutex
)

func startGUI(cfg GUIConfiguration, m *Model) {
	router := martini.NewRouter()
	router.Get("/", getRoot)
	router.Get("/rest/version", restGetVersion)
	router.Get("/rest/model/:repo", restGetModel)
	router.Get("/rest/connections", restGetConnections)
	router.Get("/rest/config", restGetConfig)
	router.Get("/rest/config/sync", restGetConfigInSync)
	router.Get("/rest/need/:repo", restGetNeed)
	router.Get("/rest/system", restGetSystem)
	router.Get("/rest/errors", restGetErrors)

	router.Post("/rest/config", restPostConfig)
	router.Post("/rest/restart", restPostRestart)
	router.Post("/rest/reset", restPostReset)
	router.Post("/rest/error", restPostError)

	go func() {
		mr := martini.New()
		if len(cfg.User) > 0 && len(cfg.Password) > 0 {
			mr.Use(auth.Basic(cfg.User, cfg.Password))
		}
		mr.Use(embeddedStatic())
		mr.Use(martini.Recovery())
		mr.Use(restMiddleware)
		mr.Action(router.Handle)
		mr.Map(m)
		err := http.ListenAndServe(cfg.Address, mr)
		if err != nil {
			warnln("GUI not possible:", err)
		}
	}()
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/index.html", 302)
}

func restMiddleware(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Path) >= 6 && r.URL.Path[:6] == "/rest/" {
		w.Header().Set("Cache-Control", "no-cache")
	}
}

func restGetVersion() string {
	return Version
}

func restGetModel(m *Model, w http.ResponseWriter, params martini.Params) {
	var repo = params["repo"]
	var res = make(map[string]interface{})

	globalFiles, globalDeleted, globalBytes := m.GlobalSize(repo)
	res["globalFiles"], res["globalDeleted"], res["globalBytes"] = globalFiles, globalDeleted, globalBytes

	localFiles, localDeleted, localBytes := m.LocalSize(repo)
	res["localFiles"], res["localDeleted"], res["localBytes"] = localFiles, localDeleted, localBytes

	needFiles, needBytes := m.NeedSize(repo)
	res["needFiles"], res["needBytes"] = needFiles, needBytes

	res["inSyncFiles"], res["inSyncBytes"] = globalFiles-needFiles, globalBytes-needBytes

	res["state"] = m.State(repo)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func restGetConnections(m *Model, w http.ResponseWriter) {
	var res = m.ConnectionStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func restGetConfig(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(cfg)
}

func restPostConfig(req *http.Request) {
	err := json.NewDecoder(req.Body).Decode(&cfg)
	if err != nil {
		log.Println(err)
	} else {
		saveConfig()
		configInSync = false
	}
}

func restGetConfigInSync(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]bool{"configInSync": configInSync})
}

func restPostRestart(req *http.Request) {
	go restart()
}

func restPostReset(req *http.Request) {
	resetRepositories()
	go restart()
}

type guiFile scanner.File

func (f guiFile) MarshalJSON() ([]byte, error) {
	type t struct {
		Name     string
		Size     int64
		Modified int64
		Flags    uint32
	}
	return json.Marshal(t{
		Name:     f.Name,
		Size:     scanner.File(f).Size,
		Modified: f.Modified,
		Flags:    f.Flags,
	})
}

func restGetNeed(m *Model, w http.ResponseWriter, params martini.Params) {
	repo := params["repo"]
	files := m.NeedFilesRepo(repo)
	gfs := make([]guiFile, len(files))
	for i, f := range files {
		gfs[i] = guiFile(f)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(gfs)
}

var cpuUsagePercent [10]float64 // The last ten seconds
var cpuUsageLock sync.RWMutex

func restGetSystem(w http.ResponseWriter) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	res := make(map[string]interface{})
	res["myID"] = myID
	res["goroutines"] = runtime.NumGoroutine()
	res["alloc"] = m.Alloc
	res["sys"] = m.Sys
	cpuUsageLock.RLock()
	var cpusum float64
	for _, p := range cpuUsagePercent {
		cpusum += p
	}
	cpuUsageLock.RUnlock()
	res["cpuPercent"] = cpusum / 10

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func restGetErrors(w http.ResponseWriter) {
	guiErrorsMut.Lock()
	json.NewEncoder(w).Encode(guiErrors)
	guiErrorsMut.Unlock()
}

func restPostError(req *http.Request) {
	bs, _ := ioutil.ReadAll(req.Body)
	req.Body.Close()
	showGuiError(string(bs))
}

func showGuiError(err string) {
	guiErrorsMut.Lock()
	guiErrors = append(guiErrors, guiError{time.Now(), err})
	if len(guiErrors) > 5 {
		guiErrors = guiErrors[len(guiErrors)-5:]
	}
	guiErrorsMut.Unlock()
}
