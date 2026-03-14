package main

import (
	"context"
	"fmt"

	"github.com/google/go-github/v60/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	branch := a.GetInput("branch")
	workflow := a.GetInput("workflow_id")

	a.Infof("🔍 Checking last run of workflow '%s' on branch '%s'...", workflow, branch)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	gh := github.NewClient(oauth2.NewClient(ctx, ts))

	owner, repo, err := owner_repo(a)
	if err != nil {
		a.Fatalf("🛑 Failed to read repository context: %v", err)
	}

	runs, _, err := gh.Actions.ListWorkflowRunsByFileName(ctx, owner, repo, workflow,
		&github.ListWorkflowRunsOptions{
			Branch:      branch,
			ListOptions: github.ListOptions{PerPage: 1},
		},
	)
	if err != nil {
		a.Fatalf("🛑 Failed to list workflow runs: %v", err)
	}

	if len(runs.WorkflowRuns) == 0 {
		a.Warningf("⚠️ No runs found for workflow '%s' on branch '%s'", workflow, branch)
		a.SetOutput("ci_success", "false")
		return
	}

	run := runs.WorkflowRuns[0]
	conclusion := run.GetConclusion()

	a.Infof("✅ Last run #%d — conclusion: %s", run.GetRunNumber(), conclusion)
	a.SetOutput("ci_success", fmt.Sprintf("%t", conclusion == "success"))
}

// owner_repo returns the owner and repository name from the GitHub Actions context.
func owner_repo(a *githubactions.Action) (string, string, error) {
	ctx, err := a.Context()
	if err != nil {
		return "", "", err
	}

	owner, repo := ctx.Repo()
	return owner, repo, nil
}
