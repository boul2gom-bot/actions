package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

// bots is the set of known bot login names that should be silently skipped.
var bots = map[string]bool{
	"dependabot[bot]":  true,
	"renovate[bot]":    true,
	"boul2gom-bot":     true,
	"github-actions[bot]": true,
}

// emoji_label maps emoji prefixes in titles to label names.
var emoji_labels = map[string]string{
	"🐛": "bug",
	"✨": "enhancement",
	"📦": "dependencies",
	"🔌": "ci",
	"💄": "refactor",
	"🎨": "refactor",
	"📚": "documentation",
	"⚡": "performance",
	"💥": "breaking-change",
	"🔒": "security",
}

// prefix_label maps branch name prefixes to label names.
var prefix_labels = map[string]string{
	"fix/":      "bug",
	"feat/":     "enhancement",
	"refactor/": "refactor",
	"docs/":     "documentation",
	"perf/":     "performance",
	"ci/":       "ci",
}

// conventional matches conventional commit prefixes like "fix:", "feat(scope):", etc.
var conventional = regexp.MustCompile(`^(fix|feat|docs|perf|ci|refactor|chore|style|test)(\([^)]+\))?(!)?:`)

// keyword_label maps title keyword fragments to label names.
var keyword_labels = map[string]string{
	"crash":           "bug",
	"panic":           "bug",
	"broken":          "bug",
	"regression":      "bug",
	"feature":         "enhancement",
	"new api":         "enhancement",
	"breaking-change": "breaking-change",
	"perf":            "performance",
	"bench":           "performance",
	"optim":           "performance",
	"speed":           "performance",
	"readme":          "documentation",
	"typo":            "documentation",
	"spelling":        "documentation",
	"bump":            "dependencies",
	"refactor":        "refactor",
	"restructure":     "refactor",
	"cleanup":         "refactor",
	"vulnerability":   "security",
	"cve":             "security",
	"exploit":         "security",
	"advisory":        "security",
}

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	json_mappings := a.GetInput("path_mappings")

	path_mappings := map[string][]string{}
	if json_mappings != "{}" && json_mappings != "" {
		if err := json.Unmarshal([]byte(json_mappings), &path_mappings); err != nil {
			a.Fatalf("🛑 Invalid path_mappings JSON: %v", err)
		}
	}

	type label_def_raw struct {
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	label_defs := map[string]LabelType{}
	if raw := a.GetInput("label_definitions"); raw != "{}" && raw != "" {
		tmp := map[string]label_def_raw{}
		if err := json.Unmarshal([]byte(raw), &tmp); err != nil {
			a.Fatalf("🛑 Invalid label_definitions JSON: %v", err)
		}
		for name, d := range tmp {
			label_defs[name] = LabelType{color: d.Color, description: d.Description}
		}
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	gh := github.NewClient(oauth2.NewClient(ctx, ts))

	action_ctx, err := a.Context()
	if err != nil {
		a.Fatalf("🛑 Failed to read repository context: %v", err)
	}

	owner, repo := action_ctx.Repo()

	_, number, is_pr, author, title, body, branch, err := extract_infos(a, action_ctx)
	if err != nil {
		a.Fatalf("🛑 Failed to extract event info: %v", err)
	}

	if bots[author] {
		a.Infof("🏷️ Skipping bot author: %s", author)
		set_outputs(a, false, is_pr, number, nil)
		return
	}

	a.Infof("🏷️ Analyzing %s #%d by @%s: %s", issue_or_pr(is_pr), number, author, title)

	is_new := action_ctx.EventName != "workflow_dispatch" && action_ctx.Action == "opened"

	to_apply := map[string]bool{}

	apply_emojis(title, to_apply)
	apply_conventional(title, to_apply)
	apply_prefix(branch, to_apply)
	apply_keywords(title, body, to_apply)

	if is_pr {
		files, err := list_files(ctx, gh, owner, repo, number)
		if err != nil {
			a.Warningf("⚠️ Could not list PR files: %v", err)
		} else {
			apply_path(files, path_mappings, to_apply)
		}
	}

	labels := map_labels(to_apply)
	if err := ensure_labels_exist(ctx, gh, owner, repo, labels, label_defs); err != nil {
		a.Warningf("⚠️ Could not ensure labels exist: %v", err)
	}

	if len(labels) > 0 {
		if err := apply_labels(ctx, gh, owner, repo, number, labels); err != nil {
			a.Fatalf("🛑 Failed to apply labels: %v", err)
		}
		a.Infof("🏷️ Labels applied: %s", strings.Join(labels, ", "))
	} else {
		a.Infof("🏷️ No labels matched for this %s", issue_or_pr(is_pr))
	}

	set_outputs(a, is_new, is_pr, number, labels)

	if is_new {
		if err := post_welcome_comment(ctx, gh, owner, repo, number, is_pr, len(labels) > 0); err != nil {
			a.Warningf("⚠️ Could not post welcome comment: %v", err)
		}
	}
}

// post_welcome_comment posts a greeting on a newly opened PR or issue.
func post_welcome_comment(ctx context.Context, gh *github.Client, owner, repo string, number int, is_pr bool, labels_applied bool) error {
	var body string

	if is_pr {
		body = "Hi there! 👋\n"
		if labels_applied {
			body += "I have automatically applied some labels to categorize this pull request.\n"
		}
		body += "Your pull request will be reviewed by a maintainer as soon as possible. Thanks for your contribution!"
	} else {
		body = "Hi there! 👋\n"
		if labels_applied {
			body += "I have automatically applied some labels to categorize this issue.\n"
		}
		body += "Your issue will be reviewed by a maintainer as soon as possible. Thanks for opening it!"
	}

	_, _, err := gh.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{
		Body: github.String(body),
	})
	return err
}

// extract_infos reads the event payload and returns the relevant fields.
func extract_infos(a *githubactions.Action, ctx *githubactions.GitHubContext) (event string, number int, is_pr bool, author, title, body, branch string, err error) {
	event = ctx.EventName

	switch event {
	case "pull_request", "pull_request_target":
		is_pr = true
		pr, ok := ctx.Event["pull_request"].(map[string]any)
		if !ok {
			err = fmt.Errorf("missing pull_request in event payload")
			return
		}

		number = int(float_field(pr, "number"))
		title = string_field(pr, "title")
		body = string_field(pr, "body")
		branch = string_field(nested_map(pr, "head"), "ref")
		author = string_field(nested_map(pr, "user"), "login")

	case "issues":
		is_pr = false
		issue, ok := ctx.Event["issue"].(map[string]any)
		if !ok {
			err = fmt.Errorf("missing issue in event payload")
			return
		}

		number = int(float_field(issue, "number"))
		title = string_field(issue, "title")
		body = string_field(issue, "body")
		author = string_field(nested_map(issue, "user"), "login")

	default:
		// workflow_dispatch with pr_number input
		prNum := a.GetInput("pr_number")
		if prNum == "" {
			err = fmt.Errorf("unsupported event: %s", event)
			return
		}

		fmt.Sscanf(prNum, "%d", &number)
		is_pr = true
	}

	return
}

// list_files returns the filenames touched by a pull request, fetching all pages.
func list_files(ctx context.Context, gh *github.Client, owner, repo string, prNumber int) ([]string, error) {
	var names []string

	opts := &github.ListOptions{PerPage: 100}
	for {
		files, resp, err := gh.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, err
		}

		for _, f := range files {
			names = append(names, f.GetFilename())
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return names, nil
}

// ensure_labels_exist creates any labels that don't yet exist in the repo.
// It resolves colors and descriptions from label_defs first, then predefined, then defaults to grey.
func ensure_labels_exist(ctx context.Context, gh *github.Client, owner, repo string, names []string, label_defs map[string]LabelType) error {
	existing_set := map[string]bool{}

	opts := &github.ListOptions{PerPage: 100}
	for {
		page, resp, err := gh.Issues.ListLabels(ctx, owner, repo, opts)
		if err != nil {
			return err
		}
		for _, l := range page {
			existing_set[l.GetName()] = true
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	for _, name := range names {
		if existing_set[name] {
			continue
		}

		def, ok := label_defs[name]
		if !ok {
			def, ok = predefined[name]
		}
		if !ok {
			def = LabelType{color: "ededed", description: ""}
		}

		_, _, err := gh.Issues.CreateLabel(ctx, owner, repo, &github.Label{
			Name:        github.String(name),
			Color:       github.String(def.color),
			Description: github.String(def.description),
		})
		if err != nil {
			return fmt.Errorf("create label %q: %w", name, err)
		}
	}

	return nil
}

// apply_labels adds the given label names to an issue or PR.
func apply_labels(ctx context.Context, gh *github.Client, owner, repo string, number int, labels []string) error {
	_, _, err := gh.Issues.AddLabelsToIssue(ctx, owner, repo, number, labels)
	return err
}

// set_outputs writes the four action outputs.
func set_outputs(a *githubactions.Action, isNew, isPR bool, number int, labels []string) {
	serialized, _ := json.Marshal(labels)

	a.SetOutput("is_new", fmt.Sprintf("%t", isNew))
	a.SetOutput("is_pr", fmt.Sprintf("%t", isPR))
	a.SetOutput("issue_number", fmt.Sprintf("%d", number))
	a.SetOutput("labels_applied", string(serialized))
}

func apply_emojis(title string, out map[string]bool) {
	for emoji, label := range emoji_labels {
		if strings.HasPrefix(title, emoji) {
			out[label] = true
		}
	}
}

func apply_conventional(title string, out map[string]bool) {
	m := conventional.FindStringSubmatch(title)
	if m == nil {
		return
	}

	prefix := m[1]
	isBreaking := m[3] == "!"

	switch prefix {
	case "fix":
		out["bug"] = true
	case "feat":
		out["enhancement"] = true
	case "docs":
		out["documentation"] = true
	case "perf":
		out["performance"] = true
	case "ci":
		out["ci"] = true
	case "refactor", "style", "chore":
		out["refactor"] = true
	}

	if isBreaking {
		out["breaking-change"] = true
	}
}

func apply_prefix(branch string, out map[string]bool) {
	for prefix, label := range prefix_labels {
		if strings.HasPrefix(branch, prefix) {
			out[label] = true
			return
		}
	}
}

func apply_keywords(title, body string, out map[string]bool) {
	// Keywords are matched against title only — body is excluded because it frequently
	// contains words like "security", "bug reports", "dependencies" in a context that
	// does not warrant a label (e.g. bot-generated PR descriptions, issue templates).
	lower := strings.ToLower(title)

	for keyword, label := range keyword_labels {
		if strings.Contains(lower, keyword) {
			out[label] = true
		}
	}
}

func apply_path(files []string, mappings map[string][]string, out map[string]bool) {
	for _, file := range files {
		for prefix, labels := range mappings {
			if strings.HasPrefix(file, prefix) {
				for _, l := range labels {
					out[l] = true
				}
			}
		}
	}
}

func map_labels(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}

func issue_or_pr(is_pr bool) string {
	if is_pr {
		return "PR"
	}

	return "issue"
}

func string_field(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}

	return ""
}

func float_field(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}

	return 0
}

func nested_map(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}

	return map[string]any{}
}
