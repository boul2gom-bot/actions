package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	raw_pattern := a.GetInput("commit_pattern")

	pattern, err := regexp.Compile(raw_pattern)
	if err != nil {
		a.Fatalf("🛑 Invalid commit pattern regex: %v", err)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	gh := github.NewClient(oauth2.NewClient(ctx, ts))

	action_ctx, err := a.Context()
	if err != nil {
		a.Fatalf("🛑 Failed to read repository context: %v", err)
	}

	owner, repo := action_ctx.Repo()

	commits := extract_commits(action_ctx)
	if len(commits) == 0 {
		a.Infof("🐛 No commits found in push event")
		return
	}

	processed := 0
	for _, commit := range commits {
		sha := commit_sha(commit)
		message := commit_message(commit)

		matches := pattern.FindAllStringSubmatch(message, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}

			issueNum, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}

			if err := notify_and_close(ctx, a, gh, owner, repo, issueNum, sha); err != nil {
				a.Warningf("🛑 Failed to notify issue #%d: %v", issueNum, err)
				continue
			}

			processed++
		}
	}

	if processed > 0 {
		a.Infof("✅ Notified and closed %d issue(s)", processed)
	} else {
		a.Infof("🐛 No issue references found in commit messages")
	}
}

// notify_and_close posts a comment on the issue referencing the fix commit, then closes it.
func notify_and_close(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string, issueNum int, sha string) error {
	shortSHA := sha
	if len(sha) > 7 {
		shortSHA = sha[:7]
	}

	comment := fmt.Sprintf("This issue has been fixed in commit %s (`%s`).", shortSHA, sha)
	_, _, err := gh.Issues.CreateComment(ctx, owner, repo, issueNum, &github.IssueComment{
		Body: github.String(comment),
	})
	if err != nil {
		return fmt.Errorf("create comment: %w", err)
	}

	state := "closed"
	stateReason := "completed"
	_, _, err = gh.Issues.Edit(ctx, owner, repo, issueNum, &github.IssueRequest{
		State:       &state,
		StateReason: &stateReason,
	})
	if err != nil {
		return fmt.Errorf("close issue: %w", err)
	}

	a.Infof("🐛 Issue #%d commented and closed (fixed in %s)", issueNum, shortSHA)
	return nil
}

// extract_commits reads the commits list from the push event payload.
func extract_commits(ctx *githubactions.GitHubContext) []map[string]any {
	raw, ok := ctx.Event["commits"]
	if !ok {
		return nil
	}

	slice, ok := raw.([]any)
	if !ok {
		return nil
	}

	commits := make([]map[string]any, 0, len(slice))
	for _, item := range slice {
		if m, ok := item.(map[string]any); ok {
			commits = append(commits, m)
		}
	}

	return commits
}

// commit_sha returns the SHA field of a commit payload map.
func commit_sha(commit map[string]any) string {
	if v, ok := commit["id"].(string); ok {
		return v
	}

	return ""
}

// commit_message returns the message field of a commit payload map.
func commit_message(commit map[string]any) string {
	if v, ok := commit["message"].(string); ok {
		return strings.Split(v, "\n")[0]
	}

	return ""
}