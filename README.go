### Tyk Project management and Release management tools

## Installation
```
go get github.com/TykTechnologies/pm
```
Now you can run it by calling `pm` or if your $GOPATH not exposed to $PATH, you can run it via `$GOPATH/bin/pm`

## Getting Github auth token

In order to communicate with Github API, you need to create a new Personal access token https://github.com/settings/tokens with Adming org read permissions (to access GH projects), and read access to private repos.

Once you got the token, set global $GITHUB_TOKEN env variable, or pass --auth argument to `pm` command on each run

## Usage

### Releasing a pull request to multiple branches

Example syntax: `pm release <pr-url> --to <branch1> --to <branch2>`.
Before running the command it expects that pull requests was merged to the master.
Internally it goes though specified branches, and cherry-picks PR related changes to it. In case of error you need to manually solve the conflicts. Can be used with `--dry-run` to just perform mergeability check.


### Releasing a Github project (based on specifed column) to multiple branches

Example syntax: `pm project-release --dry-run <project-url> <column-name> --to <repo1>:<branch1> --to <repo2>:<branch2> ...`
This command will get list of columns from specified project column, for each Github Issue it will try to find all associated pull requests, and will check if they are already merged, can be merged or conflicts are expected.
Since project may contain tickets from multiple repos, `--to` syntax extended to specify target branches per repo.
When used with `--dry-run` (recommended), it will output full report for all tickets and their PRs.
It works similarry if you would manually went though each PR and run `pm release` command.


Example code command which tells release status of Patch release project, and specify that for PRs related to `tyk` repo, check should be done against `release-2.7` and `release-2.8` projects, for `tyk-analytics` against `release-1.7` and `release-1.8` projects, and so on:

```
pm project-release https://github.com/orgs/TykTechnologies/projects/29 Merged --to tyk:release-2.8 --to tyk:release-2.7 --to tyk-analytics:release-1.8 --to tyk-analytics:release-1.7 --to tyk-analytics-ui:release-1.7 --to tyk-analytics-ui:release-1.8 --dry-run 
```
