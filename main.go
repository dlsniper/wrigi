package wrigi

import (
	"appengine"
	"appengine/urlfetch"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/gorilla/mux"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"
)

type (
	Version struct {
		Name          string
		Url           string
		Size          uint32
		Date          int64
		Body          string
		DownloadCount uint32
	}

	RepositoryVersions struct {
		Alpha   Version
		Beta    Version
		Release Version
	}

	Repository struct {
		Id          string
		Name        string
		PluginName  string
		Description string
		Versions    RepositoryVersions
		Vendor      Vendor
	}

	Organization struct {
		Name         string
		Repositories []Repository
	}

	GithubReleaseAsset struct {
		DownloadCount uint32 `json:"download_count"`
		CreatedAt     string `json:"created_at"`
		Size          uint32 `json:"size"`
		URL           string `json:"browser_download_url"`
	}

	GithubRelease struct {
		Body    string               `json:"body"`
		TagName string               `json:"tag_name"`
		Assets  []GithubReleaseAsset `json:"assets"`
	}

	Vendor struct {
		Email  string `xml:"email,attr"`
		Url    string `xml:"url,attr"`
		Vendor string `xml:",chardata"`
	}

	IdeaVersion struct {
		Min        string `xml:"min,attr"`
		Max        string `xml:"max,attr"`
		SinceBuild string `xml:"since-build,attr"`
	}

	IdeaPlugin struct {
		Downloads   uint32      `xml:"downloads,attr"`
		Size        uint32      `xml:"size,attr"`
		Date        int64       `xml:"date,attr"`
		Url         string      `xml:"url,attr"`
		Name        string      `xml:"name"`
		ID          string      `xml:"id"`
		Description string      `xml:"description"`
		Version     string      `xml:"version"`
		Vendor      Vendor      `xml:"vendor"`
		IdeaVersion IdeaVersion `xml:"idea-version"`
		ChangeNotes string      `xml:"change-notes,cdata"`
		DownloadUrl string      `xml:"downloadUrl"`
		Rating      float32     `xml:"rating"`
	}

	PluginCategory struct {
		Name       string     `xml:"name,attr"`
		IdeaPlugin IdeaPlugin `xml:"idea-plugin"`
	}

	PluginRepository struct {
		Ff       string         `xml:"ff"`
		Category PluginCategory `xml:"category"`
		XMLName  struct{}       `xml:"plugin-repository"`
	}
)

const (
	userAgent string = "Wrigi 0.1 (https://github.com/dlsniper/wrigi)"
)

var (
	repositories   []Organization
	lastUpdate     time.Time
	lastUpdateLock sync.Mutex
	OAuthToken     string
)

func initConfig() {
	file, err := ioutil.ReadFile("./config.json")
	if err != nil {
		fmt.Printf("File error: %v\n", err)
		os.Exit(1)
	}

	type CFG struct {
		Oauth string
	}

	var cfg CFG
	json.Unmarshal(file, &cfg)
	OAuthToken = cfg.Oauth

	initSupportedRepositories()
}
func initSupportedRepositories() {
	organization := Organization{
		Name: "go-lang-plugin-org",
	}
	repositories = append(repositories, organization)
	repository := Repository{
		Id:          "ro.redeul.google.go",
		Name:        "go-lang-idea-plugin",
		PluginName:  "Go language (golang.org) support plugin",
		Description: "Google Go language IDE built using the Intellij Platform. Released both an integrated IDE and as a standalone Intellij IDEA plugin",
		Vendor: Vendor{
			Email:  "mtoader@gmail.com",
			Url:    "https://github.com/go-lang-plugin-org/go-lang-idea-plugin",
			Vendor: "mtoader@gmail.com",
		},
	}
	repositories[0].Repositories = append(repositories[0].Repositories, repository)
}

func updateRepository(r *http.Request, owner string, repository Repository) Repository {
	var (
		client    *http.Client
		body      []byte
		ghRelease []GithubRelease
	)

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repository.Name)

	r.Header.Set("User-Agent", userAgent)
	c := appengine.NewContext(r)
	client = urlfetch.Client(c)

	response, err := client.Get(url)
	if err != nil {
		if appengine.IsDevAppServer() {
			panic(err)
		}
		return repository
	}

	if err != nil || response.StatusCode != 200 {
		if appengine.IsDevAppServer() {
			panic(err)
		}
		return repository
	}

	body, err = ioutil.ReadAll(response.Body)
	defer response.Body.Close()

	if err = json.Unmarshal(body, &ghRelease); err != nil {
		if appengine.IsDevAppServer() {
			panic(err)
		}
		return repository
	}

	repository.Versions.Alpha = Version{}
	repository.Versions.Beta = Version{}
	repository.Versions.Release = Version{}

	relType := regexp.MustCompile("alpha|beta|release")

	for _, release := range ghRelease {

		relDate, err := time.Parse("2006-01-02T15:04:05Z", release.Assets[0].CreatedAt)
		relD := time.Now().UTC().Unix()
		if err == nil {
			relD = relDate.Unix()
		}
		relD = relD * 1000

		rel := Version{
			Name:          release.TagName,
			DownloadCount: release.Assets[0].DownloadCount,
			Url:           release.Assets[0].URL,
			Size:          release.Assets[0].Size,
			Date:          relD,
			Body:          release.Body,
		}

		if relType.FindString(release.TagName) == "alpha" && repository.Versions.Alpha.Name == "" {
			repository.Versions.Alpha = rel
		}

		if relType.FindString(release.TagName) == "beta" && repository.Versions.Beta.Name == "" {
			repository.Versions.Beta = rel
		}

		if relType.FindString(release.TagName) == "release" && repository.Versions.Release.Name == "" {
			repository.Versions.Release = rel
		}
	}

	return repository
}

func updateVersions(r *http.Request) {
	for oidx, owner := range repositories {
		for ridx, repository := range owner.Repositories {
			repositories[oidx].Repositories[ridx] = updateRepository(r, owner.Name, repository)
		}
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response, err := json.Marshal(repositories)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("%s", err)))
	}
	w.Write(response)
}

func updateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")

	lastUpdateLock.Lock()

	if time.Since(lastUpdate) < 5*time.Minute {
		w.Write([]byte("Repositories where updated less than 5 minutes ago. Please come back later."))
		lastUpdateLock.Unlock()
		return
	}

	updateVersions(r)

	lastUpdateLock.Unlock()

	w.Write([]byte("Remote repositories updated"))
}

func tokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(OAuthToken))
}
func submitErrorHandler(w http.ResponseWriter, r *http.Request) {
	var (
		client *http.Client
	)
	vars := mux.Vars(r)

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", vars["owner"], vars["repository"])

	r.Header.Set("User-Agent", userAgent)
	r.Header.Set("Authorization", "token "+OAuthToken)
	c := appengine.NewContext(r)
	client = urlfetch.Client(c)

	response, err := client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		if appengine.IsDevAppServer() {
			panic(err)
		}
	}

	_ = response
}

func ideaPluginHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	var repository Repository
	for _, owner := range repositories {
		if owner.Name == vars["owner"] {
			for _, repo := range owner.Repositories {
				if repo.Name == vars["repository"] {
					repository = repo
					break
				}
			}
			break
		}
	}

	if repository.Name == "" {
		http.Error(w, "404 page not found", 404)
		return
	}

	var version Version

	switch vars["channel"] {
	case "alpha":
		version = repository.Versions.Alpha
	case "beta":
		version = repository.Versions.Beta
	case "release":
		version = repository.Versions.Release
	default:
		{
			http.Error(w, "404 page not found", 404)
			return
		}
	}

	ideaPlugin := IdeaPlugin{
		Name:        repository.PluginName,
		ID:          repository.Id + "." + vars["channel"],
		Description: repository.Description,
		Version:     version.Name,
		Size:        version.Size,
		Date:        version.Date,
		Url:         fmt.Sprintf("https://github.com/%s/%s", vars["owner"], vars["repository"]),
		DownloadUrl: version.Url,
		Downloads:   version.DownloadCount,
		ChangeNotes: version.Body,
		Vendor:      repository.Vendor,
		IdeaVersion: IdeaVersion{
			Min:        "n/a",
			Max:        "n/a",
			SinceBuild: "122.0",
		},
	}

	pluginCategory := PluginCategory{
		Name:       "Custom Languages",
		IdeaPlugin: ideaPlugin,
	}

	plugin := PluginRepository{
		Ff:       "\"Custom Languages\"",
		Category: pluginCategory,
	}

	var response []byte
	var err error

	switch vars["format"] {
	case "xml":
		{
			w.Header().Set("Content-Type", "application/xml")
			response, err = xml.MarshalIndent(plugin, "", "    ")
			response = []byte(xml.Header + string(response))
		}
	default:
		{
			w.Header().Set("Content-Type", "application/json")
			response, err = json.MarshalIndent(plugin, "", "    ")
		}
	}

	if err != nil && appengine.IsDevAppServer() {
		panic(err)
	}

	w.Write(response)
}

func init() {
	initConfig()

	r := mux.NewRouter()
	r.HandleFunc("/", rootHandler).Methods("GET")
	r.HandleFunc("/update", updateHandler)
	r.HandleFunc("/{owner}/{repository}/submitError", submitErrorHandler).Methods("POST")
	r.HandleFunc("/{owner}/{repository}/{channel}.{format}", ideaPluginHandler).Methods("GET")
	r.HandleFunc("/{owner}/{repository}/{channel}/idea.{format}", ideaPluginHandler).Methods("GET")

	//r.HandleFunc("/{owner}/{repository}/token", tokenHandler).Methods("GET")

	http.Handle("/", r)
}
