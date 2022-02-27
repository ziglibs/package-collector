package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

func main() {

	raw_packages := make([]Package, 0)

	{
		log.Print("Fetching packages from ziglibs/repository...")

		git_executable, err := exec.LookPath("git")
		if err != nil {
			log.Fatal(err)
		}

		_, err = (&exec.Cmd{
			Path: git_executable,
			Args: []string{"git", "submodule", "update", "--init", "--recursive"},
			Dir:  ".",
		}).Output()

		if err != nil {
			log.Fatal(err)
		}

		_, err = (&exec.Cmd{
			Path: git_executable,
			Args: []string{"git", "pull"},
			Dir:  "repository",
		}).Output()

		if err != nil {
			log.Fatal(err)
		}

		err = filepath.Walk("repository/packages", func(path string, info os.FileInfo, err error) error {

			if info.IsDir() {
				return nil
			}

			log.Printf("loading %s...\n", path)
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
			})

			return nil
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	{
		log.Print("Fetching packages from github...")

		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: os.Getenv("GITHUB_API_TOKEN")},
		)
		tc := oauth2.NewClient(ctx, ts)

		client := github.NewClient(tc)

		page := 1
		count := 0

		for {
			repos, _, err := client.Search.Repositories(
				context.Background(),
				"topic:zig-package",
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

			// fmt.Printf("%d %d %d %v\n", count, repos.GetTotal(), len(repos.Repositories), repos.GetIncompleteResults())

			for _, repo := range repos.Repositories {
				raw_packages = append(raw_packages, Package{
					GitRepo:     *repo.CloneURL,
					DisplayName: *repo.Name,
					Tags:        filterAndSortTags(repo.Topics),
					Author:      *repo.Owner.Login,
					Source:      SRC_GITHUB,
				})
			}

			count += len(repos.Repositories)
			if len(repos.Repositories) == 0 || count >= repos.GetTotal() {
				break
			}

			page += 1
		}
	}

	log.Printf("Collected %d source packages, merging...", len(raw_packages))
	for i, pkg := range raw_packages {
		fmt.Printf("[%d] = %+v\n", i, pkg)
	}
}

type Package struct {
	GitRepo     string // used for deduplication
	DisplayName string
	Tags        []string
	Author      string // first come, first serve
	Source      int
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
