package wrigi

import (
	"appengine"
	"appengine/urlfetch"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/gorilla/mux"
	"io/ioutil"
	"net/http"
	"regexp"
	"sync"
	"time"
)

type (
	Version struct {
		Name string
		Url  string
	}

	RepositoryVersions struct {
		Alpha   Version
		Beta    Version
		Release Version
	}

	Repository struct {
		Id       string
		Name     string
		Versions RepositoryVersions
	}

	Organization struct {
		Name         string
		Repositories []Repository
	}

	GithubReleaseAsset struct {
		URL string `json:"browser_download_url"`
	}

	GithubRelease struct {
		TagName string               `json:"tag_name"`
		Assets  []GithubReleaseAsset `json:"assets"`
	}

	IdeaPlugin struct {
		Id      string `json:"id" xml:"id,attr"`
		Url     string `json:"url" xml:"url,attr"`
		Version string `json:"version" xml:"version,attr"`
	}

	plugins struct {
		Plugins []IdeaPlugin `json:"plugin" xml:"plugin"`
	}
)

const (
	userAgent string = "Wrigi 0.1 (https://github.com/dlsniper/wrigi)"
)

var (
	repositories   []Organization
	lastUpdate     time.Time
	lastUpdateLock sync.Mutex
)

func initSupportedRepositories() {
	repositories = append(repositories, Organization{Name: "go-lang-plugin-org"})
	repositories[0].Repositories = append(repositories[0].Repositories, Repository{Id: "ro.redeul.google.go", Name: "go-lang-idea-plugin"})
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

		if relType.FindString(release.TagName) == "alpha" && repository.Versions.Alpha.Name == "" {
			repository.Versions.Alpha.Name = release.TagName
			repository.Versions.Alpha.Url = release.Assets[0].URL
		}

		if relType.FindString(release.TagName) == "beta" && repository.Versions.Beta.Name == "" {
			repository.Versions.Beta.Name = release.TagName
			repository.Versions.Beta.Url = release.Assets[0].URL
		}

		if relType.FindString(release.TagName) == "release" && repository.Versions.Release.Name == "" {
			repository.Versions.Release.Name = release.TagName
			repository.Versions.Release.Url = release.Assets[0].URL
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

	plugin := IdeaPlugin{
		Id:      repository.Id,
		Url:     version.Url,
		Version: version.Name,
	}
	plugins := plugins{}
	plugins.Plugins = append(plugins.Plugins, plugin)

	var response []byte
	var err error

	switch vars["format"] {
	case "xml":
		{
			w.Header().Set("Content-Type", "application/xml")

			response, err = xml.Marshal(plugins)
			if err != nil && appengine.IsDevAppServer() {
				panic(err)
			}
		}
	default:
		{
			w.Header().Set("Content-Type", "application/json")

			response, err = json.Marshal(plugins)
			if err != nil && appengine.IsDevAppServer() {
				panic(err)
			}
		}
	}

	w.Write(response)
	w.WriteHeader(200)
}

func init() {
	initSupportedRepositories()

	r := mux.NewRouter()
	r.HandleFunc("/", rootHandler)
	r.HandleFunc("/update", updateHandler)
	r.HandleFunc("/{owner}/{repository}/{channel}/idea.{format}", ideaPluginHandler)
	http.Handle("/", r)
}
