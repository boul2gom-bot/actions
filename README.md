<h2 align="center">⚙️ Reusable GitHub Actions</h2>

<div align="center">A collection of reusable, Go-powered GitHub Actions for automating release workflows, issue management, and code quality tasks across projects.</div>

<br>

<div align="center">
  <a href="https://github.com/boul2gom/actions/issues/new/choose">Report a Bug</a>
  ·
  <a href="https://github.com/boul2gom/actions/discussions/new?category=ideas">Request a Feature</a>
  ·
  <a href="https://github.com/boul2gom/actions/discussions/new?category=q-a">Ask a Question</a>
</div>

---

<p align="center">
  <a href="https://github.com/boul2gom/actions/actions/workflows/ci.yml">
    <img src="https://img.shields.io/github/actions/workflow/status/boul2gom/actions/ci.yml?label=CI&logo=Github" alt="CI"/>
  </a>
  <a href="https://github.com/boul2gom/actions/releases">
    <img src="https://img.shields.io/github/v/release/boul2gom/actions?label=Release&logo=Github" alt="Release"/>
  </a>
  <a href="https://github.com/boul2gom/actions/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/boul2gom/actions?label=License&logo=Github" alt="License"/>
  </a>
</p>

---

## 📋 Available actions

| Action | Type | Description |
|--------|------|-------------|
| [`✅ ci-status`](#-ci-status) | Docker (Go) | Returns true if the last CI workflow run succeeded |
| [`✅ unreleased-commits`](#-unreleased-commits) | Docker (Go) | Detects unreleased commits classified per project |
| [`📝 release-notes`](#-release-notes) | Docker (Go) | Generates categorized Markdown release notes |
| [`🤖 versions-dropdown`](#-versions-dropdown) | Docker (Go) | Updates the version dropdown in the bug report template |
| [`🏷️ label-pr-issue`](#️-label-pr-issue) | Docker (Go) | Analyzes and applies labels to PRs and issues |
| [`🐛 fix-notifier`](#-fix-notifier) | Docker (Go) | Comments and closes issues referenced in commit messages |

---

## ✅ ci-status

Returns `true` if the last run of a given workflow on a given branch concluded with success.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `actions:read` permission |
| `workflow_id` | ❌ | `ci-dev.yml` | Workflow filename to check |
| `branch` | ❌ | `develop` | Branch to check the last run on |

### Outputs

| Name | Description |
|------|-------------|
| `ci_success` | `'true'` if the last run concluded with success |

### Usage

```yaml
- name: ✅ Check CI status
  id: check_ci
  uses: boul2gom/actions/ci-status@v1
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    # workflow_id: ci-dev.yml  # optional — already the default
    # branch: develop          # optional — already the default
```

---

## ✅ unreleased-commits

Detects commits since the last tag and classifies them between the main project and any number of sub-projects. Used to decide whether a release is needed and which components are affected.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `contents:read` permission |
| `force_release` | ❌ | `false` | Force a release even if no unreleased commits are found |
| `sub_projects` | ❌ | `""` | Newline-separated `name:path_prefix` pairs for sub-projects |
| `exclude_patterns` | ❌ | `[Github Actions],[skip ci]` | Comma-separated commit message patterns to exclude |

### Outputs

| Name | Description |
|------|-------------|
| `one_has` | `'true'` if at least one project has unreleased updates |
| `main_has` | `'true'` if the main project has unreleased commits |
| `sub_results` | JSON map of sub-project name to `'true'`/`'false'` |

### Usage

```yaml
- name: ✅ Check unreleased commits
  id: check_commits
  uses: boul2gom/actions/unreleased-commits@v1
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    sub_projects: |
      media-seek:crates/media-seek/

# steps.check_commits.outputs.main_has                                    → main project
# fromJSON(steps.check_commits.outputs.sub_results)['media-seek']         → sub-project
```

---

## 📝 release-notes

Generates categorized Markdown release notes from commits since the previous tag. Commits are grouped into **Features**, **Bug Fixes**, **Refactors**, and **Dependency Updates**. Sub-project commits get their own section.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `contents:read` permission |
| `version` | ✅ | — | New version being released (e.g. `2.7.0`) |
| `sub_projects` | ❌ | `""` | Newline-separated `name:path_prefix` pairs for sub-projects |

### Outputs

| Name | Description |
|------|-------------|
| `release_notes` | Generated release notes in Markdown |

### Usage

```yaml
- name: 📝 Generate release notes
  id: notes
  uses: boul2gom/actions/release-notes@v1
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    version: ${{ steps.get_version.outputs.version }}
    sub_projects: |
      media-seek:crates/media-seek/

- name: 📝 Create GitHub Release
  uses: softprops/action-gh-release@v2
  with:
    body: ${{ steps.notes.outputs.release_notes }}
```

---

## 🤖 versions-dropdown

Updates the version dropdown options in a GitHub issue template (e.g. `bug_report.yml`) after a new release. Automatically paginates all releases, builds the dropdown list, and commits the change with GPG signing.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `contents:write` permission |
| `app_id` | ✅ | — | GitHub App ID for generating an ephemeral commit token |
| `app_key` | ✅ | — | GitHub App private key |
| `gpg_key` | ✅ | — | GPG private key for signing the commit |
| `gpg_passphrase` | ✅ | — | GPG key passphrase |
| `file_path` | ❌ | `.github/ISSUE_TEMPLATE/bug_report.yml` | Path to the issue template YAML |
| `dropdown_id` | ❌ | `crate_version` | ID of the dropdown field to update |
| `tag_pattern` | ❌ | `^v\d+\.\d+\.\d+$` | Regex to filter relevant releases by tag name |
| `commit_branch` | ❌ | `develop` | Branch to commit the updated file to |
| `committer_name` | ❌ | `boul2gom-bot` | Git committer name |
| `committer_email` | ❌ | `actions-bot@boul2gom.com` | Git committer email |

### Usage

```yaml
- name: 🤖 Update version dropdown
  uses: boul2gom/actions/versions-dropdown@v1
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    app_id: ${{ secrets.CI_APP_ID }}
    app_key: ${{ secrets.CI_APP_KEY }}
    gpg_key: ${{ secrets.GPG_PRIVATE_KEY }}
    gpg_passphrase: ${{ secrets.GPG_PASSPHRASE }}
    # All other inputs already have the correct defaults
```

---

## 🏷️ label-pr-issue

Automatically analyzes PRs and issues, then applies labels based on emoji prefixes, conventional commit format, branch name, touched file paths, title keywords, and body content. Creates missing labels on the fly with configurable colors.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `issues:write` and `pull-requests:write` |
| `path_mappings` | ❌ | `{}` | JSON map of file path prefixes to label arrays |
| `label_definitions` | ❌ | `{}` | JSON map of label name to `{"color": "hex", "description": "..."}` |

### Outputs

| Name | Description |
|------|-------------|
| `is_new` | `'true'` if this is the author's first contribution |
| `is_pr` | `'true'` if the event is a pull request |
| `issue_number` | PR or issue number |
| `labels_applied` | JSON array of applied label names |

### Usage

```yaml
- name: 🏷️ Label issues and PRs
  uses: boul2gom/actions/label-pr-issue@v1
  with:
    token: ${{ steps.app-token.outputs.token }}
    path_mappings: |
      {
        "src/cache/": ["cache"],
        "src/download/": ["download"],
        "crates/media-seek/": ["media-seek"]
      }
    label_definitions: |
      {
        "cache":      {"color": "bfd4f2", "description": "Related to the cache layer"},
        "download":   {"color": "bfd4f2", "description": "Related to the download engine"},
        "media-seek": {"color": "c5def5", "description": "Related to the media-seek crate"}
      }
```

---

## 🐛 fix-notifier

Scans pushed commits for issue references matching a configurable pattern (default: `(to solve #N)`), posts a comment on the referenced issue with the fix commit SHA, and closes it.

### Inputs

| Name | Required | Default | Description |
|------|:--------:|---------|-------------|
| `token` | ✅ | — | GitHub token with `issues:write` permission |
| `commit_pattern` | ❌ | `\(to solve #(\d+)\)` | Regex to extract issue numbers from commit messages |

### Usage

```yaml
- name: 🐛 Notify fixed issues
  uses: boul2gom/actions/fix-notifier@v1
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    # commit_pattern: \(to solve #(\d+)\)  # optional — already the default
```

---

## 🔧 Local development

### Building all actions

```bash
# Vet all modules
go vet ./...

# Test all modules
go test ./...

# Build and test a single action image locally
docker build -t test ./ci-status
docker run \
  -e INPUT_TOKEN=ghp_... \
  -e INPUT_WORKFLOW_ID=ci-dev.yml \
  -e INPUT_BRANCH=develop \
  -e GITHUB_REPOSITORY=boul2gom/yt-dlp \
  -e GITHUB_OUTPUT=/tmp/output \
  test
```

### Releasing

Push a tag matching `v*` to trigger the [release workflow](.github/workflows/release.yml), which builds all Docker images and pushes them to `ghcr.io/boul2gom/actions/<action-name>`.

```bash
git tag v1.0.0
git push origin v1.0.0
```
