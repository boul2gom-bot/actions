package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/sethvargo/go-githubactions"
	"golang.org/x/oauth2"
)

func main() {
	a := githubactions.New()

	token := a.GetInput("token")
	file_path := a.GetInput("file_path")
	dropdown_id := a.GetInput("dropdown_id")
	tag_pattern_raw := a.GetInput("tag_pattern")
	commit_branch := a.GetInput("commit_branch")
	committer_name := a.GetInput("committer_name")
	committer_email := a.GetInput("committer_email")
	gpg_key := a.GetInput("gpg_key")
	gpg_passphrase := a.GetInput("gpg_passphrase")

	tag_pattern, err := regexp.Compile(tag_pattern_raw)
	if err != nil {
		a.Fatalf("Invalid tag_pattern regex: %v", err)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	gh := github.NewClient(oauth2.NewClient(ctx, ts))

	action_ctx, err := a.Context()
	if err != nil {
		a.Fatalf("Failed to read repository context: %v", err)
	}

	owner, repo := action_ctx.Repo()

	versions, err := fetch_versions(ctx, a, gh, owner, repo, tag_pattern)
	if err != nil {
		a.Fatalf("Failed to fetch releases: %v", err)
	}

	if len(versions) == 0 {
		a.Warningf("No matching releases found, skipping update")
		return
	}

	options := build_dropdown_options(versions)
	a.Infof("🤖 New dropdown options: %s", strings.Join(options, ", "))

	work_dir, err := os.MkdirTemp("", "dropdown-*")
	if err != nil {
		a.Fatalf("Failed to create work dir: %v", err)
	}
	defer os.RemoveAll(work_dir)

	clone_url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repo)
	if err := run(work_dir, "git", "clone", "--branch", commit_branch, "--depth", "1", clone_url, "."); err != nil {
		a.Fatalf("Failed to clone repository: %v", err)
	}

	full_path := filepath.Join(work_dir, file_path)
	content, err := os.ReadFile(full_path)
	if err != nil {
		a.Fatalf("Failed to read %s: %v", file_path, err)
	}

	updated, err := apply_dropdown_options(string(content), dropdown_id, options)
	if err != nil {
		a.Fatalf("Failed to update dropdown in %s: %v", file_path, err)
	}

	if updated == string(content) {
		a.Infof("✅ No changes needed, dropdown is already up to date")
		return
	}

	if err := os.WriteFile(full_path, []byte(updated), 0o644); err != nil {
		a.Fatalf("Failed to write %s: %v", file_path, err)
	}

	key_id, err := import_gpg_key(work_dir, gpg_key, gpg_passphrase)
	if err != nil {
		a.Fatalf("Failed to import GPG key: %v", err)
	}

	if err := configure_git(work_dir, committer_name, committer_email, key_id, gpg_passphrase); err != nil {
		a.Fatalf("Failed to configure git: %v", err)
	}

	commit_msg := "🤖 Update crate version dropdown in bug report [skip ci]"
	if err := commit_and_push(work_dir, file_path, commit_msg, gpg_passphrase); err != nil {
		a.Fatalf("Failed to commit and push: %v", err)
	}

	a.Infof("✅ File %s updated and committed successfully", file_path)
}

// fetch_versions paginates all releases and returns version strings matching the tag pattern,
// sorted newest first.
func fetch_versions(ctx context.Context, a *githubactions.Action, gh *github.Client, owner, repo string, tag_pattern *regexp.Regexp) ([]string, error) {
	var versions []string

	opts := &github.ListOptions{PerPage: 100}
	for {
		releases, resp, err := gh.Repositories.ListReleases(ctx, owner, repo, opts)
		if err != nil {
			return nil, err
		}

		for _, r := range releases {
			tag := r.GetTagName()
			if tag_pattern.MatchString(tag) {
				versions = append(versions, strings.TrimPrefix(tag, "v"))
			}
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	sort.Slice(versions, func(i, j int) bool {
		return semver_greater(versions[i], versions[j])
	})

	return versions, nil
}

// build_dropdown_options constructs the final option list:
// 3 most recent versions (first tagged as "(latest)"), older major series as "X.x", then "Other".
func build_dropdown_options(versions []string) []string {
	recent := versions
	if len(recent) > 3 {
		recent = versions[:3]
	}

	options := make([]string, len(recent))
	for i, v := range recent {
		if i == 0 {
			options[i] = fmt.Sprintf("%s (latest)", v)
		} else {
			options[i] = v
		}
	}

	covered_majors := map[string]bool{}
	for _, v := range recent {
		covered_majors[major_of(v)] = true
	}

	seen_majors := map[string]bool{}
	for _, v := range versions {
		maj := major_of(v)
		if !covered_majors[maj] && !seen_majors[maj] {
			options = append(options, maj+".x")
			seen_majors[maj] = true
		}
	}

	return append(options, "Other")
}

// apply_dropdown_options replaces the options block of the given dropdown_id in the YAML content.
func apply_dropdown_options(content, dropdown_id string, options []string) (string, error) {
	pattern := fmt.Sprintf(`(  - type: dropdown\n    id: %s[\s\S]*?      options:\n)((?:        - [^\n]*\n)*)`, regexp.QuoteMeta(dropdown_id))
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compile pattern: %w", err)
	}

	options_block := ""
	for _, opt := range options {
		options_block += fmt.Sprintf("        - %s\n", opt)
	}

	updated := re.ReplaceAllStringFunc(content, func(match string) string {
		loc := re.FindStringSubmatchIndex(match)
		if loc == nil {
			return match
		}
		prefix := match[loc[2]:loc[3]]
		return prefix + options_block
	})

	if updated == content && !re.MatchString(content) {
		return "", fmt.Errorf("pattern not matched — verify dropdown_id and file structure")
	}

	return updated, nil
}

// import_gpg_key imports the GPG key and returns its fingerprint.
func import_gpg_key(dir, gpg_key, passphrase string) (string, error) {
	key_file, err := os.CreateTemp("", "gpg-key-*.asc")
	if err != nil {
		return "", fmt.Errorf("create temp key file: %w", err)
	}
	defer os.Remove(key_file.Name())

	if _, err := key_file.WriteString(gpg_key); err != nil {
		return "", fmt.Errorf("write key file: %w", err)
	}
	key_file.Close()

	if err := run(dir, "gpg", "--batch", "--import", key_file.Name()); err != nil {
		return "", fmt.Errorf("gpg import: %w", err)
	}

	out, err := output(dir, "gpg", "--batch", "--list-secret-keys", "--with-colons")
	if err != nil {
		return "", fmt.Errorf("gpg list keys: %w", err)
	}

	key_id := parse_gpg_fingerprint(out)
	if key_id == "" {
		return "", fmt.Errorf("could not find fingerprint in gpg output")
	}

	return key_id, nil
}

// parse_gpg_fingerprint extracts the fingerprint from `gpg --list-secret-keys --with-colons` output.
func parse_gpg_fingerprint(out string) string {
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 10 && parts[0] == "fpr" {
			return parts[9]
		}
	}
	return ""
}

// configure_git sets user identity and GPG signing in the work directory.
func configure_git(dir, name, email, key_id, passphrase string) error {
	wrapper_script := filepath.Join(dir, "gpg-wrapper.sh")
	wrapper_content := "#!/bin/sh\nexec gpg --batch --pinentry-mode loopback --passphrase \"$GPG_PASSPHRASE\" \"$@\"\n"
	if err := os.WriteFile(wrapper_script, []byte(wrapper_content), 0o755); err != nil {
		return fmt.Errorf("write wrapper: %w", err)
	}

	configs := [][2]string{
		{"user.name", name},
		{"user.email", email},
		{"user.signingkey", key_id},
		{"commit.gpgsign", "true"},
		{"gpg.program", wrapper_script},
	}
	for _, kv := range configs {
		if err := run(dir, "git", "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("git config %s: %w", kv[0], err)
		}
	}
	return nil
}

// commit_and_push stages the file, creates a signed commit, and pushes.
func commit_and_push(dir, file_path, message, passphrase string) error {
	if err := run(dir, "git", "add", file_path); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	cmd := exec.Command("git", "commit", "-S", "-m", message)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GPG_PASSPHRASE="+passphrase)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w\n%s", err, out)
	}

	if err := run(dir, "git", "push"); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	return nil
}

// run executes a command in the given directory, returning an error with combined output on failure.
func run(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", name, err, out)
	}
	return nil
}

// output executes a command and returns its combined output as a string.
func output(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// semver_greater returns true if version a is newer than version b.
func semver_greater(a, b string) bool {
	a_parts := semver_parts(a)
	b_parts := semver_parts(b)

	for i := range a_parts {
		if a_parts[i] != b_parts[i] {
			return a_parts[i] > b_parts[i]
		}
	}

	return false
}

// semver_parts splits a version string "X.Y.Z" into [X, Y, Z] as integers.
func semver_parts(v string) [3]int {
	var major, minor, patch int
	fmt.Sscanf(v, "%d.%d.%d", &major, &minor, &patch)

	return [3]int{major, minor, patch}
}

// major_of returns the major version component of a version string.
func major_of(v string) string {
	if idx := strings.IndexByte(v, '.'); idx >= 0 {
		return v[:idx]
	}

	return v
}
