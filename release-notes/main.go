package main

import (
	"context"
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

// categories groups commits by type for one project section.
type categories struct {
	features     []string
	bug_fixes    []string
	refactors    []string
	dependencies []string
}

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	version := a.GetInput("version")
	sub_projects_raw := a.GetInput("sub_projects")

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

	a.Infof("📝 Generating release notes since %s...", reference_date.Format(time.RFC3339))

	commits, err := list_commits_since(ctx, gh, owner, repo, reference_date)
	if err != nil {
		a.Fatalf("🛑 Failed to list commits: %v", err)
	}

	main_cats, sub_cats := classify_commits(ctx, a, gh, owner, repo, commits, sub_projects)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 🚀 Release v%s\n\n", version))
	write_categories(&sb, main_cats)

	for i, sp := range sub_projects {
		cats := sub_cats[i]
		if is_empty(cats) {
			continue
		}
		sb.WriteString(fmt.Sprintf("---\n\n### 📦 %s\n\n", sp.name))
		write_categories(&sb, cats)
	}

	a.Infof("✨ %d features, %d bug fixes, %d refactors, %d dependency updates (main)",
		len(main_cats.features), len(main_cats.bug_fixes), len(main_cats.refactors), len(main_cats.dependencies))

	a.SetOutput("release_notes", sb.String())
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

// find_reference_date returns the date of the previous tag (second-most-recent).
func find_reference_date(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string) time.Time {
	reference_date := time.Unix(0, 0)

	tags, _, err := gh.Repositories.ListTags(ctx, owner, repo, &github.ListOptions{PerPage: 2})
	if err != nil {
		a.Warningf("Failed to list tags, using epoch as reference date: %v", err)
		return reference_date
	}

	idx := 1
	if len(tags) < 2 {
		idx = 0
	}

	if len(tags) > idx {
		tag := tags[idx]
		commit, _, err := gh.Repositories.GetCommit(ctx, owner, repo, tag.GetCommit().GetSHA(), nil)
		if err == nil {
			tag_date := commit.GetCommit().GetCommitter().GetDate().Time
			a.Infof("🔖 Previous tag found: %s (%s)", tag.GetName(), tag_date.Format(time.RFC3339))
			reference_date = tag_date
		}
	}

	releases, _, err := gh.Repositories.ListReleases(ctx, owner, repo, &github.ListOptions{PerPage: 1})
	if err == nil && len(releases) > 0 {
		release_date := releases[0].GetCreatedAt().Time
		if release_date.After(reference_date) {
			reference_date = release_date
		}
	}

	return reference_date
}

// list_commits_since returns all commits pushed after the given date, fetching all pages.
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

// classify_commits splits commits between the main section and each sub-project section.
// A commit goes into a sub-project section if ALL its changed files fall under that prefix.
// Otherwise it goes into the main section.
func classify_commits(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string, commits []*github.RepositoryCommit, sub_projects []sub_project) (categories, []categories) {
	sub_cats := make([]categories, len(sub_projects))
	var main_cats categories

	refactor_tags := []string{"refactor", "format", "lint", "cleanup", "rename", "restructure", "💄", "🎨", "♻️"}
	dep_tags := []string{"bump", "update", "dependencies", "dependency", "cargo.lock", "pinning", "📌", "🔌"}
	bug_tags := []string{"fix", "bug", "error", "patch", "hotfix", "🐛", "🚑"}

	for _, c := range commits {
		message := first_line(c.GetCommit().GetMessage())

		if should_skip(message) {
			continue
		}

		entry := build_entry(c, message)
		msg_lower := strings.ToLower(message)

		// Determine which section this commit belongs to.
		target := -1 // -1 = main section
		if len(sub_projects) > 0 {
			files := commit_files(ctx, a, gh, owner, repo, c.GetSHA())
			if len(files) > 0 {
				for i, sp := range sub_projects {
					if all_under_prefix(files, sp.prefix) {
						target = i
						break
					}
				}
			}
		}

		var cats *categories
		if target >= 0 {
			cats = &sub_cats[target]
		} else {
			cats = &main_cats
		}

		switch {
		case contains_any(msg_lower, dep_tags):
			cats.dependencies = append(cats.dependencies, entry)
		case contains_any(msg_lower, bug_tags):
			cats.bug_fixes = append(cats.bug_fixes, entry)
		case contains_any(msg_lower, refactor_tags):
			cats.refactors = append(cats.refactors, entry)
		default:
			cats.features = append(cats.features, entry)
		}
	}

	return main_cats, sub_cats
}

// should_skip returns true for version-bump, formatter, and bot-update commits.
func should_skip(message string) bool {
	if strings.HasPrefix(message, "🔖") {
		return true
	}
	if version_tag_pattern(message) {
		return true
	}
	if strings.Contains(message, "[skip ci]") {
		return true
	}
	return false
}

// all_under_prefix returns true if every file in files starts with prefix.
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

// commit_files returns the list of files changed by a commit via the GitHub API.
func commit_files(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo, sha string) []string {
	detail, _, err := gh.Repositories.GetCommit(ctx, owner, repo, sha, nil)
	if err != nil {
		a.Warningf("Failed to fetch files for commit %s: %v", sha[:7], err)
		return nil
	}

	files := make([]string, 0, len(detail.Files))
	for _, f := range detail.Files {
		files = append(files, f.GetFilename())
	}
	return files
}

// build_entry formats a single commit as a release notes bullet entry.
func build_entry(c *github.RepositoryCommit, message string) string {
	author := commit_author(c)
	sha := c.GetSHA()
	pr_number := extract_pr_number(message)

	if pr_number != "" {
		return fmt.Sprintf("%s — by @%s in #%s (%s)", message, author, pr_number, sha[:7])
	}
	return fmt.Sprintf("%s — by @%s (%s)", message, author, sha[:7])
}

// write_categories appends all non-empty category sections to the builder.
func write_categories(sb *strings.Builder, cats categories) {
	if len(cats.features) > 0 {
		sb.WriteString("✨ Features\n")
		for _, e := range cats.features {
			sb.WriteString("- " + e + "\n")
		}
		sb.WriteString("\n")
	}

	if len(cats.bug_fixes) > 0 {
		sb.WriteString("🐛 Bug Fixes\n")
		for _, e := range cats.bug_fixes {
			sb.WriteString("- " + e + "\n")
		}
		sb.WriteString("\n")
	}

	if len(cats.refactors) > 0 {
		sb.WriteString("💄 Refactors\n")
		for _, e := range cats.refactors {
			sb.WriteString("- " + e + "\n")
		}
		sb.WriteString("\n")
	}

	if len(cats.dependencies) > 0 {
		sb.WriteString("📦 Dependency Updates\n")
		for _, e := range cats.dependencies {
			sb.WriteString("- " + e + "\n")
		}
		sb.WriteString("\n")
	}
}

// is_empty returns true if all category slices are empty.
func is_empty(cats categories) bool {
	return len(cats.features) == 0 && len(cats.bug_fixes) == 0 &&
		len(cats.refactors) == 0 && len(cats.dependencies) == 0
}

// version_tag_pattern returns true if the message looks like a bare version bump.
func version_tag_pattern(msg string) bool {
	return strings.Contains(msg, "v") &&
		len(msg) < 30 &&
		strings.Count(msg, ".") >= 2
}

// extract_pr_number returns the PR number from "Fix foo (#123)" style messages, or "".
func extract_pr_number(msg string) string {
	start := strings.LastIndex(msg, "(#")
	if start < 0 {
		return ""
	}
	end := strings.Index(msg[start:], ")")
	if end < 0 {
		return ""
	}
	return msg[start+2 : start+end]
}

// commit_author returns the GitHub login or fallback name for a commit.
func commit_author(c *github.RepositoryCommit) string {
	if c.Author != nil && c.Author.GetLogin() != "" {
		return c.Author.GetLogin()
	}
	return c.GetCommit().GetAuthor().GetName()
}

// contains_any returns true if s contains any of the given substrings.
func contains_any(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// first_line returns only the first line of a multi-line string.
func first_line(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
