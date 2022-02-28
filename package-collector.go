package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

type FetchConfig struct {
	github     bool
	astrolabe  bool
	ziglibs    bool
	aquila_red bool
}

func main() {

	fetch_config := FetchConfig{
		github:     true,
		astrolabe:  true,
		ziglibs:    true,
		aquila_red: true,
	}

	raw_packages := make([]Package, 0)

	if fetch_config.ziglibs {
		log.Print("Fetching packages from ziglibs/repository...")

		git_executable, err := exec.LookPath("git")
		if err != nil {
			log.Fatalln(err)
		}

		_, err = (&exec.Cmd{
			Path: git_executable,
			Args: []string{"git", "submodule", "update", "--init", "--recursive"},
			Dir:  ".",
		}).Output()

		if err != nil {
			log.Fatalln(err)
		}

		_, err = (&exec.Cmd{
			Path: git_executable,
			Args: []string{"git", "pull"},
			Dir:  "repository",
		}).Output()

		if err != nil {
			log.Fatalln(err)
		}

		err = filepath.Walk("repository/packages", func(path string, info os.FileInfo, err error) error {

			if info.IsDir() {
				return nil
			}

			// log.Printf("loading %s...\n", path)
			bytes, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			var pkg = ZigPackageDef{}
			err = json.Unmarshal(bytes, &pkg)
			if err != nil {
				return err
			}

			raw_packages = append(raw_packages, Package{
				GitRepo:     pkg.Git,
				DisplayName: filepath.Base(path),
				Tags:        filterAndSortTags(pkg.Tags),
				Author:      pkg.Author,
				Source:      SRC_ZIGLIBS,
				Links: Links{
					Github: heapify(pkg.Git),
				},
				Description: pkg.Description,
				RootFile:    heapify(pkg.RootFile),
			})

			return nil
		})
		if err != nil {
			log.Fatalln(err)
		}
	}

	if fetch_config.github {
		log.Print("Fetching packages from github...")

		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: os.Getenv("GITHUB_API_TOKEN")},
		)
		tc := oauth2.NewClient(ctx, ts)

		client := github.NewClient(tc)

		// fmt.Printf("%d %d %d %v\n", count, repos.GetTotal(), len(repos.Repositories), repos.GetIncompleteResults())
		raw_packages = loadGithubTopic(client, raw_packages, "zig-package")
		raw_packages = loadGithubTopic(client, raw_packages, "zig-library")
	}

	// astrolabe and aquila must be fetched later as they might disturb the original author of the package

	if fetch_config.astrolabe {
		log.Print("Fetching packages from astrolabe.pm...")

		response, err := http.Get("https://astrolabe.pm/pkgs")
		if err != nil {
			log.Fatalln(err)
		}

		bytes, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Fatalln(err)
		}

		var astro_pkgs []AstroPackage
		err = json.Unmarshal(bytes, &astro_pkgs)

		for _, pkg := range astro_pkgs {
			raw_packages = append(raw_packages, Package{
				GitRepo:     pkg.SourceUrl,
				DisplayName: pkg.Name,
				Tags:        filterAndSortTags(pkg.Tags),
				Author:      pkg.User,
				Source:      SRC_ASTROLABE,
				Links: Links{
					Astrolabe: heapify(fmt.Sprintf("https://astrolabe.pm/#/package/%s/%s/%s", pkg.User, pkg.Name, pkg.Version)),
				},
				Description: pkg.Description,
			})
		}
	}

	if fetch_config.aquila_red {
		log.Print("Fetching packages from aquila.red...")

		client := &http.Client{}
		req, err := http.NewRequest("GET", "https://aquila.red/all/packages", nil)
		if err != nil {
			log.Fatalln(err)
		}
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}

		bytes, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Fatalln(err)
		}

		var aquila_pkgs AquilaList
		err = json.Unmarshal(bytes, &aquila_pkgs)

		for _, pkg := range aquila_pkgs.List {
			author := strings.Split(pkg.RemoteName, "/")[0]

			raw_packages = append(raw_packages, Package{
				GitRepo:     fmt.Sprintf("https://github.com/%s", pkg.RemoteName),
				DisplayName: pkg.Name,
				Tags:        make([]string, 0),
				Author:      author,
				Source:      SRC_AQUILA,
				Links: Links{
					Aquila: heapify(fmt.Sprintf("https://aquila.red/%d/%s/%s", pkg.Remote, author, pkg.Name)),
				},
				Description: pkg.Description,
			})
		}
	}

	log.Printf("Collected %d source packages, merging...", len(raw_packages))

	packages := make(map[string]Package)

	for _, pkg := range raw_packages {

		unique_path := strings.ToLower(uniqueGitPath(pkg.GitRepo))

		stored := packages[unique_path]

		if stored.GitRepo != "" {
			// log.Printf("duplicate repo: %s\n", stored.GitRepo)
			stored.Tags = mergeTags(stored.Tags, pkg.Tags)
			stored.Source |= pkg.Source // we got the package from several sources

			if stored.Links.Aquila == nil {
				stored.Links.Aquila = pkg.Links.Aquila
			}
			if stored.Links.Astrolabe == nil {
				stored.Links.Astrolabe = pkg.Links.Astrolabe
			}
			if stored.Links.Github == nil {
				stored.Links.Github = pkg.Links.Github
			}

		} else {
			stored = pkg
		}

		packages[unique_path] = stored
	}

	log.Printf("Loaded %d packages, with %d packages merged.", len(packages), len(raw_packages)-len(packages))

	package_list := make([]Package, 0)
	for _, pkg := range packages {
		package_list = append(package_list, pkg)
	}

	final, err := json.Marshal(package_list)
	if err != nil {
		log.Fatalln(err)
	}

	os.WriteFile("all-packages.json", final, 0666)
}

func loadGithubTopic(client *github.Client, raw_packages []Package, topic string) []Package {
	page := 1
	count := 0

	for {
		repos, _, err := client.Search.Repositories(
			context.Background(),
			"topic:"+topic,
			&github.SearchOptions{
				Sort:  "stars",
				Order: "desc",
				ListOptions: github.ListOptions{
					Page:    page,
					PerPage: 100,
				},
			},
		)
		if err != nil {
			log.Fatalf("failed to query repositories: %s", err)
		}

		for _, repo := range repos.Repositories {
			raw_packages = append(raw_packages, Package{
				GitRepo:     *repo.CloneURL,
				DisplayName: *repo.Name,
				Tags:        filterAndSortTags(repo.Topics),
				Author:      *repo.Owner.Login,
				Source:      SRC_GITHUB,
				Links: Links{
					Github: repo.HTMLURL,
				},
				Description: repo.GetDescription(),
			})
		}

		count += len(repos.Repositories)
		if len(repos.Repositories) == 0 || count >= repos.GetTotal() {
			break
		}

		page += 1
	}
	return raw_packages
}

func uniqueGitPath(uri string) string {
	if strings.HasPrefix(uri, "https://github.com") && strings.HasSuffix(uri, ".git") {
		return uri[0 : len(uri)-4]
	}
	return uri
}

func mergeTags(tags_list ...[]string) []string {
	set := make(map[string]*bool)
	res := make([]string, 0)

	for _, tags := range tags_list {
		for _, tag := range tags {
			if set[tag] == nil {
				res = append(res, tag)
			}
		}
	}

	return res

}

type Package struct {
	GitRepo     string   `json:"git"` // used for deduplication
	DisplayName string   `json:"name"`
	Tags        []string `json:"tags"`
	Author      string   `json:"author"` // first come, first serve
	Source      int      `json:"source"`
	Links       Links    `json:"links"`
	Description string   `json:"description"`
	RootFile    *string  `json:"root_file"`
}

type Links struct {
	Github    *string `json:"github"`
	Astrolabe *string `json:"astrolabe"`
	Aquila    *string `json:"aquila"`
}

const (
	SRC_GITHUB    = 1 << iota // https://github.com/topics/zig-package
	SRC_AQUILA    = 1 << iota // https://aquila.red/
	SRC_ASTROLABE = 1 << iota // https://astrolabe.pm/
	SRC_ZIGLIBS   = 1 << iota // https://github.com/ziglibs/repository
)

var filtered_tags []string = []string{
	"zig",
	"zig-package",
	"ziglang",
	"zig-programming-language",
	"zig-library",
	"zig-lang",
}

func filterAndSortTags(tags []string) []string {
	res := []string{}
	for _, tag := range tags {
		tag = strings.ToLower(tag)
		if !contains(filtered_tags, tag) {
			res = append(res, tag)
		}
	}
	sort.Strings(res)
	return res
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

type ZigPackageDef struct {
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Git         string   `json:"git"`
	RootFile    string   `json:"root_file"`
	Description string   `json:"description"`
}

type AstroPackage struct {
	User        string   `json:"user"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	License     string   `json:"license"`
	SourceUrl   string   `json:"source_url"`
	Downloads   int      `json:"downloads"`
	Tags        []string `json:"tags"`
	// Deps        string `json:"deps"`
	// BuildDeps   string `json:"build_deps"`
}

type AquilaList struct {
	List []AquilaPkg `json:"list"`
}

type AquilaPkg struct {
	Uuid          string `json:"uuid"`           // : "001ZFTQN5BK6P7235W2YY7TKP1",
	Owner         string `json:"owner"`          // : "0015KMZ1NDDFP4WRWSVA31N0CD",
	Name          string `json:"name"`           // : "pcre-8.45",
	CreatedOn     string `json:"created_on"`     // : "Mon, 28 Feb 2022 02:04:49 UTC",
	Remote        int    `json:"remote"`         // : 1,
	RemoteId      string `json:"remote_id"`      // : "462170122",
	RemoteName    string `json:"remote_name"`    // : "nektro/pcre-8.45",
	Description   string `json:"description"`    // : "Perl Compatible Regular Expressions",
	License       string `json:"license"`        // : "BSD-3-Clause",
	LatestVersion string `json:"latest_version"` // : "v0.1",
	StarCount     string `json:"star_count"`     // : 0
}

func heapify(str string) *string {
	ptr := new(string)
	*ptr = str
	return ptr
}
