/*
Copyright Lathishbabu Ganesan

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
/*
The Helm webclient which enables to communicated to the helm client over REST API. The normal helm operations like add/remove/delete/list
repo's & Install/Uninstall charts in Kubernetes Cluster.
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"

	"github.com/gofrs/flock"
	"github.com/pkg/errors"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
	_ "helm.sh/helm/v3/pkg/strvals"
)

type RepoElement struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

type chartElement struct {
	Name        string `json:"name"`
	ReleaseName string `json:"releaseName"`
	RepoName    string `json:"repoName"`
	FilePath    string `json:"filePath"`
	Version     string `json:"version"`
	Args        string `json:"args"`
}

var settings *cli.EnvSettings

// 10MB
const MAX_MEMORY = 10 * 1024 * 1024

var localChartPath = "/opt/app/helmclient/chart/"

func init() {
	lvl, ok := os.LookupEnv("LOG_LEVEL")
	// LOG_LEVEL not set, let's default to debug
	if !ok {
		lvl = "debug"
	}
	// parse string, this is built-in feature of logrus
	ll, err := logrus.ParseLevel(lvl)
	if err != nil {
		ll = logrus.DebugLevel
	}
	// set global log level
	logrus.SetLevel(ll)
}
func main() {
	prepareRepo()
	router := mux.NewRouter().StrictSlash(true)
	router.Use(contentType)
	router.HandleFunc("/repo", addRepo).Methods("PUT")                // add & update repo
	router.HandleFunc("/repo", listRepo).Methods("GET")               // list repo
	router.HandleFunc("/repo", deleteRepo).Methods("DELETE")          // remove repo
	router.HandleFunc("/install", installChart).Methods("PUT")        // install chart
	router.HandleFunc("/uninstall", unInstallChart).Methods("DELETE") // uninstall chart
	log.Fatal(http.ListenAndServe(":9090", router))
}

// Adds repo with given name and url
func addRepo(response http.ResponseWriter, request *http.Request) {
	logrus.Debug("Add Repo Start")
	var repoElement RepoElement
	reqBody, err := ioutil.ReadAll(request.Body)
	if err != nil {
		logrus.Error("Invalid Input", response)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Invalid Input")
		return
	}
	err = json.Unmarshal(reqBody, &repoElement)
	if err != nil {
		panic(err)
	}
	logrus.Debug("Repo Name: ", repoElement.Name)
	logrus.Debug("Repo Url: ", repoElement.Url)
	repoFileList, path := prepareRepo()
	log.Println(repoFileList)
	if repoFileList.Has(repoElement.Name) {
		logrus.Error("repository name (%s) already exists\n", repoElement.Name)
		response.WriteHeader(http.StatusConflict)
		json.NewEncoder(response).Encode("Repository already exists")
		return
	}
	c := repo.Entry{
		Name: repoElement.Name,
		URL:  repoElement.Url,
	}
	r, err := repo.NewChartRepository(&c, getter.All(settings))
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Error in calling New Chart Repository")
		return
	}

	if _, err := r.DownloadIndexFile(); err != nil {
		err := errors.Wrapf(err, "looks like %q is not a valid chart repository or cannot be reached", repoElement.Url)
		logrus.Error(err)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Invalid Chart Repository")
		return
	}

	repoFileList.Update(&c)

	if err := repoFileList.WriteFile(path, 0644); err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error in writing to File")
		return
	}
	logrus.Debug("%q has been added to your repositories\n", repoElement.Name)
	updateRepo(path)
	response.WriteHeader(http.StatusCreated)
	logrus.Debug("Add Repo End")
	json.NewEncoder(response).Encode("Added Repository")
}

// List repo
func listRepo(response http.ResponseWriter, request *http.Request) {
	logrus.Debug("List Repo Start")
	repoList := make([]RepoElement, 0)
	repoFileList, path := prepareRepo()
	for i := 0; i < len(repoFileList.Repositories); i++ {
		repoList = append(repoList, RepoElement{Name: repoFileList.Repositories[i].Name, Url: repoFileList.Repositories[i].URL})

	}
	logrus.Debug("Repositories Path: ", path)
	logrus.Debug("Repo's List: ", repoFileList)
	response.WriteHeader(http.StatusOK)
	json.NewEncoder(response).Encode(repoList)
}

// Delete repo
func deleteRepo(response http.ResponseWriter, request *http.Request) {
	var repoElement RepoElement
	reqBody, err := ioutil.ReadAll(request.Body)
	if err != nil {
		logrus.Error("Invalid Input", response)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Invalid Input")
		return
	}
	err = json.Unmarshal(reqBody, &repoElement)
	if err != nil {
		panic(err)
	}
	logrus.Debug("Repo Name: ", repoElement.Name)
	logrus.Debug("Repo Url: ", repoElement.Url)

	repoFileList, path := prepareRepo()
	f, err := repo.LoadFile(path)
	if os.IsNotExist(errors.Cause(err)) || len(f.Repositories) == 0 {
		logrus.Error("no repositories configured")
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Repositories Not Configured")
		return
	}

	if !f.Remove(repoElement.Name) {
		logrus.Error("no repo named %q found", repoElement.Name)
		response.WriteHeader(http.StatusNotFound)
		json.NewEncoder(response).Encode("Repository Not found")
		return
	}
	if err := f.WriteFile(path, 0644); err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error in writing to File")
		return
	}

	// if err := removeRepoCache(o.repoCache, name); err != nil {
	// 	return err
	// }
	logrus.Debug("%q has been removed from your repositories\n", repoElement.Name)
	logrus.Trace("Repositories Path: ", path)
	logrus.Trace("Repo's List: ", repoFileList)
	response.WriteHeader(http.StatusOK)
	logrus.Debug("Delete Repo End")
	json.NewEncoder(response).Encode("Repository Removed")
}

// InstallChart
func installChart(response http.ResponseWriter, request *http.Request) {
	logrus.Debug("Install Chart Start")
	var chartElement chartElement
	request.ParseMultipartForm(MAX_MEMORY) // limit your max input length!
	reqBody := request.FormValue("data")
	err := json.Unmarshal([]byte(reqBody), &chartElement)
	if err != nil {
		logrus.Error("Error in reading input******", reqBody)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Invalid Input")
		return
	}
	logrus.Debug("Chart Name: ", chartElement.Name)
	logrus.Debug("Chart ReleaseName: ", chartElement.ReleaseName)
	logrus.Debug("Chart RepoName: ", chartElement.RepoName)
	logrus.Debug("Chart Args: ", chartElement.Args)
	logrus.Debug("Chart File Path: ", chartElement.FilePath)
	os.Setenv("HELM_NAMESPACE", chartElement.ReleaseName)

	var _repoName string
	var _chartName string
	var fileName string
	if chartElement.RepoName == "" {
		file, header, err := request.FormFile("file")
		if err != nil {
			logrus.Error("Error in uploaded File", response)
			response.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(response).Encode("Error in uploaded File")
			return
		}
		defer file.Close()
		fileName = header.Filename
		logrus.Debug("File name: ", fileName)
		// This is path which we want to save the chart temporarily
		path, err := os.OpenFile(localChartPath+fileName, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			logrus.Error(err)
		}
		logrus.Trace(path)

		// Copy the file to the destination path
		io.Copy(path, file)
		_repoName = "/opt/app/helmclient/chart/"
		_chartName = fileName
	} else {
		_repoName = chartElement.RepoName
		_chartName = chartElement.Name
	}

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), debug); err != nil {
		log.Fatal(err)
	}
	client := action.NewInstall(actionConfig)

	if client.Version == "" && client.Devel {
		client.Version = ">0.0.0-0"
	}
	//name, chart, err := client.NameAndChart(args)
	client.ReleaseName = chartElement.Name

	cp, err := client.ChartPathOptions.LocateChart(fmt.Sprintf("%s/%s", _repoName, _chartName), settings)
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error Locating Chart")
		return
	}
	logrus.Debug("CHART PATH: %s\n", cp)

	p := getter.All(settings)
	valueOpts := &values.Options{}
	vals, err := valueOpts.MergeValues(p)
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error getting Chart Options")
		return
	}

	// Add args
	// if err := strvals.ParseInto(chartElement.Args, vals); err != nil {
	// 	log.Fatal(errors.Wrap(err, "failed parsing --set data"))
	// }

	// Check chart dependencies to make sure all are present in /charts
	chartRequested, err := loader.Load(cp)
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error loading Chart")
		return
	}

	validInstallableChart, err := isChartInstallable(chartRequested)
	if !validInstallableChart {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Invalid Chart")
		return
	}

	if req := chartRequested.Metadata.Dependencies; req != nil {
		// If CheckDependencies returns an error, we have unfulfilled dependencies.
		// As of Helm 2.4.0, this is treated as a stopping condition:
		// https://github.com/helm/helm/issues/2209
		if err := action.CheckDependencies(chartRequested, req); err != nil {
			if client.DependencyUpdate {
				man := &downloader.Manager{
					Out:              os.Stdout,
					ChartPath:        cp,
					Keyring:          client.ChartPathOptions.Keyring,
					SkipUpdate:       false,
					Getters:          p,
					RepositoryConfig: settings.RepositoryConfig,
					RepositoryCache:  settings.RepositoryCache,
				}
				if err := man.Update(); err != nil {
					logrus.Error(err)
					response.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(response).Encode("Error in Chart Dependency")
					return
				}
			} else {
				logrus.Error(err)
				response.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(response).Encode("Error in Chart Dependency")
				return
			}
		}
	}
	logrus.Debug("Settings Namespace: ", settings.Namespace())
	client.Namespace = chartElement.ReleaseName
	logrus.Debug("Client Namespace: ", client.Namespace)
	release, err := client.Run(chartRequested, vals)
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error installing Chart")
		return
	}
	logrus.Trace("Release Manifest: ", release.Manifest)
	err = os.Remove("/opt/app/helmclient/" + fileName)
	if err != nil {
		logrus.Error("Error deleting the Chart from local directory", err)
	}
	response.WriteHeader(http.StatusCreated)
	logrus.Debug("Install Chart End")
	json.NewEncoder(response).Encode("Chart Installed Successfully")
}

// UninstallChart
func unInstallChart(response http.ResponseWriter, request *http.Request) {
	logrus.Debug("Uninstall Chart Start")
	var chartElement chartElement
	reqBody, err := ioutil.ReadAll(request.Body)
	if err != nil {
		logrus.Error("Invalid Input", response)
		response.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(response).Encode("Invalid Input")
		return
	}
	err = json.Unmarshal(reqBody, &chartElement)
	if err != nil {
		panic(err)
	}
	logrus.Debug("Chart Name: ", chartElement.Name)
	logrus.Debug("Chart ReleaseName: ", chartElement.ReleaseName)
	os.Setenv("HELM_NAMESPACE", chartElement.ReleaseName)
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"), debug); err != nil {
		log.Fatal(err)
	}
	client := action.NewUninstall(actionConfig)
	release, err := client.Run(chartElement.Name)
	if err != nil {
		logrus.Error(err)
		response.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(response).Encode("Error uninstalling Chart")
		return
	}
	logrus.Trace("Release Manifest: ", release.Info)
	response.WriteHeader(http.StatusOK)
	logrus.Debug("Uninstall Chart End")
	json.NewEncoder(response).Encode("Chart Uninstalled Successfully")
}

func prepareRepo() (f repo.File, path string) {
	logrus.Debug("Prepare Repo Start")
	settings = cli.New()
	repoFile := settings.RepositoryConfig
	error := os.MkdirAll(filepath.Dir(repoFile), os.ModePerm)
	if error != nil && !os.IsExist(error) {
		logrus.Error(error)
	}
	// Acquire a file lock for process synchronization
	fileLock := flock.New(strings.Replace(repoFile, filepath.Ext(repoFile), ".lock", 1))
	lockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, time.Second)
	if err == nil && locked {
		defer fileLock.Unlock()
	}
	if err != nil {
		logrus.Error(err)
	}

	b, err := ioutil.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		logrus.Error(err)
	}

	if err := yaml.Unmarshal(b, &f); err != nil {
		logrus.Error(err)
	}

	path = repoFile
	logrus.Debug("Prepare Repo End")
	return f, path
}

// updates charts for all helm repos
func updateRepo(repoFile string) {
	logrus.Debug("Update Repo Start")
	f, err := repo.LoadFile(repoFile)
	if os.IsNotExist(errors.Cause(err)) || len(f.Repositories) == 0 {
		logrus.Error(errors.New("no repositories found. You must add one before updating"))
	}
	var repos []*repo.ChartRepository
	for _, cfg := range f.Repositories {
		r, err := repo.NewChartRepository(cfg, getter.All(settings))
		if err != nil {
			log.Fatal(err)
		}
		repos = append(repos, r)
	}

	logrus.Debug("Hang tight while we grab the latest from your chart repositories...\n")
	var wg sync.WaitGroup
	for _, re := range repos {
		wg.Add(1)
		go func(re *repo.ChartRepository) {
			defer wg.Done()
			if _, err := re.DownloadIndexFile(); err != nil {
				logrus.Debug("...Unable to get an update from the %q chart repository (%s):\n\t%s\n", re.Config.Name, re.Config.URL, err)
			} else {
				logrus.Debug("...Successfully got an update from the %q chart repository\n", re.Config.Name)
			}
		}(re)
	}
	wg.Wait()
	logrus.Debug("Update Repo End")
}

func isChartInstallable(ch *chart.Chart) (bool, error) {
	switch ch.Metadata.Type {
	case "", "application":
		return true, nil
	}
	return false, errors.Errorf("%s charts are not installable", ch.Metadata.Type)
}

func debug(format string, v ...interface{}) {
	format = fmt.Sprintf("[debug] %s\n", format)
	log.Output(2, fmt.Sprintf(format, v...))
}

func contentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}
