package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

// sub_project describes a named sub-project identified by its path prefix.
type sub_project struct {
	name   string
	prefix string
}

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	force_release := a.GetInput("force_release") == "true"
	sub_projects_raw := a.GetInput("sub_projects")
	exclude_patterns := parse_patterns(a.GetInput("exclude_patterns"))

	sub_projects := parse_sub_projects(sub_projects_raw)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	gh := github.NewClient(oauth2.NewClient(ctx, ts))

	action_ctx, err := a.Context()
	if err != nil {
		a.Fatalf("🛑 Failed to read repository context: %v", err)
	}

	owner, repo := action_ctx.Repo()
	reference_date := find_reference_date(ctx, a, gh, owner, repo)

	commits, err := list_commits_since(ctx, gh, owner, repo, reference_date)
	if err != nil {
		a.Fatalf("🛑 Failed to list commits: %v", err)
	}

	relevant := filter_commits(commits, exclude_patterns)
	if len(relevant) == 0 && !force_release {
		a.Infof("✅ No unreleased updates found")
		set_outputs(a, false, false, sub_projects, map[string]bool{})
		return
	}

	main_has, sub_has := classify_commits(ctx, a, gh, owner, repo, relevant, sub_projects)

	if force_release {
		a.Infof("🚀 Force release enabled — overriding commit analysis results")
		main_has = true
		for _, sp := range sub_projects {
			sub_has[sp.name] = true
		}
	}

	one_has := main_has
	for _, v := range sub_has {
		if v {
			one_has = true
			break
		}
	}

	a.Infof("✅ main: %t", main_has)
	for _, sp := range sub_projects {
		a.Infof("✅ %s: %t", sp.name, sub_has[sp.name])
	}

	set_outputs(a, one_has, main_has, sub_projects, sub_has)
}

// find_reference_date returns the date of the most recent tag or release.
func find_reference_date(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string) time.Time {
	reference_date := time.Unix(0, 0)

	tags, _, err := gh.Repositories.ListTags(ctx, owner, repo, &github.ListOptions{PerPage: 1})
	if err != nil {
		a.Warningf("Failed to list tags, using epoch as reference date: %v", err)
	} else if len(tags) > 0 {
		tag := tags[0]
		commit, _, err := gh.Repositories.GetCommit(ctx, owner, repo, tag.GetCommit().GetSHA(), nil)
		if err == nil {
			tag_date := commit.GetCommit().GetCommitter().GetDate().Time
			a.Infof("🔖 Last tag found: %s (%s)", tag.GetName(), tag_date.Format(time.RFC3339))
			if tag_date.After(reference_date) {
				reference_date = tag_date
			}
		}
	}

	releases, _, err := gh.Repositories.ListReleases(ctx, owner, repo, &github.ListOptions{PerPage: 1})
	if err != nil {
		a.Warningf("Failed to list releases: %v", err)
	} else if len(releases) > 0 {
		release_date := releases[0].GetCreatedAt().Time
		if release_date.After(reference_date) {
			reference_date = release_date
		}
	}

	a.Infof("📅 Reference date: %s", reference_date.Format(time.RFC3339))
	return reference_date
}

// list_commits_since returns all commits after the given date, fetching all pages.
func list_commits_since(ctx context.Context, gh *github.Client, owner, repo string, since time.Time) ([]*github.RepositoryCommit, error) {
	var all []*github.RepositoryCommit

	opts := &github.CommitsListOptions{
		Since:       since,
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		commits, resp, err := gh.Repositories.ListCommits(ctx, owner, repo, opts)
		if err != nil {
			return nil, err
		}

		all = append(all, commits...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return all, nil
}

// filter_commits removes commits whose messages match any of the exclude patterns.
func filter_commits(commits []*github.RepositoryCommit, exclude_patterns []string) []*github.RepositoryCommit {
	var filtered []*github.RepositoryCommit

	for _, c := range commits {
		message := c.GetCommit().GetMessage()
		excluded := false

		for _, pattern := range exclude_patterns {
			if strings.Contains(message, pattern) {
				excluded = true
				break
			}
		}

		if !excluded {
			filtered = append(filtered, c)
		}
	}

	return filtered
}

// classify_commits fetches file details for each commit and determines whether
// it touches the main project and/or each sub-project.
// A commit is attributed to a sub-project only if ALL its files fall under that prefix.
// Otherwise it is attributed to the main project.
func classify_commits(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string, commits []*github.RepositoryCommit, sub_projects []sub_project) (main_has bool, sub_has map[string]bool) {
	sub_has = make(map[string]bool)

	for _, c := range commits {
		message := first_line(c.GetCommit().GetMessage())
		sha := c.GetSHA()

		detail, _, err := gh.Repositories.GetCommit(ctx, owner, repo, sha, nil)
		if err != nil {
			a.Warningf("Could not fetch files for %s — assuming all projects affected: %v", sha[:7], err)
			main_has = true
			for _, sp := range sub_projects {
				sub_has[sp.name] = true
			}
			continue
		}

		files := make([]string, 0, len(detail.Files))
		for _, f := range detail.Files {
			files = append(files, f.GetFilename())
		}

		// Check if this commit belongs exclusively to one sub-project.
		attributed := false
		for _, sp := range sub_projects {
			if all_under_prefix(files, sp.prefix) {
				a.Infof("📦 [%s] %s", sp.name, message)
				sub_has[sp.name] = true
				attributed = true
				break
			}
		}

		if !attributed {
			a.Infof("📦 [main] %s", message)
			main_has = true
		}

		// Short-circuit once everything is true.
		if main_has && all_sub_true(sub_projects, sub_has) {
			break
		}
	}

	return main_has, sub_has
}

// set_outputs writes all action outputs.
func set_outputs(a *githubactions.Action, one_has, main_has bool, sub_projects []sub_project, sub_has map[string]bool) {
	a.SetOutput("one_has", fmt.Sprintf("%t", one_has))
	a.SetOutput("main_has", fmt.Sprintf("%t", main_has))

	result := make(map[string]string, len(sub_projects))
	for _, sp := range sub_projects {
		result[sp.name] = fmt.Sprintf("%t", sub_has[sp.name])
	}

	serialized, _ := json.Marshal(result)
	a.SetOutput("sub_results", string(serialized))
}

// parse_sub_projects parses newline-separated "name:path_prefix" entries.
func parse_sub_projects(raw string) []sub_project {
	var result []sub_project
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		prefix := strings.TrimSpace(line[idx+1:])
		if name == "" || prefix == "" {
			continue
		}
		result = append(result, sub_project{name: name, prefix: prefix})
	}
	return result
}

// parse_patterns splits a comma-separated string into a slice of non-empty patterns.
func parse_patterns(raw string) []string {
	var patterns []string
	for _, p := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

// all_under_prefix returns true if every file starts with prefix.
func all_under_prefix(files []string, prefix string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !strings.HasPrefix(f, prefix) {
			return false
		}
	}
	return true
}

// all_sub_true returns true if every sub-project is already marked true in the map.
func all_sub_true(sub_projects []sub_project, sub_has map[string]bool) bool {
	for _, sp := range sub_projects {
		if !sub_has[sp.name] {
			return false
		}
	}
	return true
}

// first_line returns only the first line of a multi-line string.
func first_line(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
