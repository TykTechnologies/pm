package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	tm "github.com/buger/goterm"
	"github.com/google/go-github/github"
	"github.com/urfave/cli"
	"golang.org/x/oauth2"
)

var prURLRe = regexp.MustCompile(`https://github.com/([^/]+)/([^/]+)/(issues|pull)/(\d+)`)
var greenCheck = tm.Bold(tm.Color("✔", tm.GREEN))
var yellowCheck = tm.Bold(tm.Color("✔", tm.YELLOW))
var redCheck = tm.Bold(tm.Color("✘", tm.RED))

var gh *github.Client

func initGithubClient(token string) {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)

	gh = github.NewClient(tc)
}

func gitExec(dir string, commands ...string) error {
	cmd := exec.Command("git", commands...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func gitExecWithOutput(dir string, commands ...string) (string, string, string, error) {
	var stdout, stderr, stdin bytes.Buffer

	cmd := exec.Command("git", commands...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = &stdin

	err := cmd.Run()

	return stdout.String(), stderr.String(), stdin.String(), err
}

func gitExecSilent(dir string, commands ...string) error {
	cmd := exec.Command("git", commands...)
	cmd.Dir = dir
	return cmd.Run()
}

func gitCloneRepo(url string, cachePath string) error {
	defer gitExecSilent(cachePath, "reset", "--hard")
	defer gitExecSilent(cachePath, "fetch", "origin")

	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		return nil
	}

	return gitExecSilent("", "clone", url, cachePath)
}

func mergePR(org string, repo string, issueNum int, mergeTo []string, embedded bool, dryRun bool, onlyMissing bool) error {
	var err error
	url := fmt.Sprintf("https://github.com/%s/%s", org, repo)
	cachePath := fmt.Sprintf("/tmp/git_cache/%s/%s", org, repo)
	prefix := ""
	if embedded {
		prefix = "\t"
	}

	if err = gitCloneRepo(url, cachePath); err != nil {
		return fmt.Errorf("Error during clonning repo: %v", err)
	}

	var pr *github.PullRequest
	pr, _, err = gh.PullRequests.Get(context.Background(), org, repo, issueNum)
	if err != nil {
		return fmt.Errorf("Error during getting info about pull request: %v", err)
	}

	if !embedded {
		fmt.Printf("\n%s\n%s\nState: %s\nMerge SHA: %s\n\n", tm.Bold("["+strconv.Itoa(issueNum)+"] "+pr.GetTitle()), pr.GetHTMLURL(), pr.GetState(), pr.GetMergeCommitSHA())
	} else {
		fmt.Printf(prefix+"%s\n\t%s\n", tm.Bold("["+strconv.Itoa(issueNum)+"] "+pr.GetTitle()), pr.GetHTMLURL())
	}

	if pr.GetState() != "closed" {
		return fmt.Errorf("Can't release unmerged PR")
	}

	var stdout, stderr string
	mergeToFound := false
	for _, branch := range mergeTo {
		if strings.Contains(branch, ":") {
			if !strings.HasPrefix(branch, repo+":") {
				continue
			}

			branch = strings.TrimPrefix(branch, repo+":")
		}

		mergeToFound = true

		gitExecSilent(cachePath, "reset", "--hard")
		gitExecSilent(cachePath, "branch", "-D", branch)
		if _, stderr, _, err = gitExecWithOutput(cachePath, "checkout", branch); err != nil {
			return fmt.Errorf("Checkout error for `%s` branch %s:", branch, stderr)
		}

		// Get merge commit info
		stdout, stderr, _, err = gitExecWithOutput(cachePath, "log", "-1", pr.GetMergeCommitSHA(), "--pretty=format:%B")
		if err != nil {
			return fmt.Errorf("Unknown commit SHA %s. %s %s", pr.GetMergeCommitSHA(), stdout, stderr, err)
		}
		commitBody := stdout
		authorEmail, _, _, _ := gitExecWithOutput(cachePath, "log", "-1", pr.GetMergeCommitSHA(), "--pretty=format:%aE")
		logCommand := []string{"log", branch, "--all-match", "--author", authorEmail}
		for _, line := range strings.Split(commitBody, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			logCommand = append(logCommand, "--grep")
			logCommand = append(logCommand, strings.TrimSpace(line))
		}
		stdout, stderr, _, err = gitExecWithOutput(cachePath, logCommand...)

        // Commit found in history
		if strings.TrimSpace(stdout) != "" && err == nil {
            if !onlyMissing {
    			fmt.Printf(prefix+"%s Already merged to %s [commit message check]\n", greenCheck, branch)
            }
		} else {
			stdout, stderr, _, _ = gitExecWithOutput(cachePath, "cherry-pick", "-x", pr.GetMergeCommitSHA())
			if strings.Contains(stderr, "allow-empty") {
                if !onlyMissing {
    				fmt.Printf(prefix+"%s Already merged to %s\n", greenCheck, branch)
                }
			} else if strings.Contains(stderr, "error: could not apply") {
				if !embedded {
					fmt.Printf("%s Conflict during cherry-picking to `%s`\n\nTo resolve: `cd %s`, fix conflict, run `git cherry-pick --continue`, and push to origin `git push origin %s`\nTip: Use `cd -` to go back into previous directory\n", redCheck, branch, cachePath, branch)
				} else {
					fmt.Printf(prefix+"%s Conflict during cherry-picking `%s` to `%s`\n", redCheck, pr.GetMergeCommitSHA(), branch)
				}
			} else {
				if stderr != "" {
					return fmt.Errorf("%s. %s", stderr, stdout)
				}
				if !dryRun {
					fmt.Printf(prefix+"%s Succesfully merged to %s\n", greenCheck, branch)
				} else {
					fmt.Printf(prefix+"%s Can be merged to %s\n", yellowCheck, branch)
				}
			}

			if !dryRun {
				stdout, stderr, _, _ = gitExecWithOutput(cachePath, "push", "origin", branch)
				if stderr != "" {
					if strings.Contains(stdout, "Everything up-to-date") {
						return fmt.Errorf("Can't push changes to origin: %s %s", stderr, stdout)
					}
				}
			} else {
				if !embedded {
					fmt.Println("Do not pushing changes because of --dry-run")
				}
			}
		}

		if !mergeToFound {
			return fmt.Errorf("Can't find merge destination for `%s` repo", repo)
		}
	}

	// Notify linked issues
	for _, match := range prURLRe.FindAllStringSubmatch(pr.GetBody(), -1) {
		if dryRun {
			break
		}
		num, _ := strconv.Atoi(match[4])
		_, _, err = gh.Issues.Get(context.Background(), org, match[2], num)
		if err != nil {
			// Broken link?
			continue
		}

		comments, _, _ := gh.Issues.ListComments(context.Background(), org, match[2], num, &github.IssueListCommentsOptions{})

		for _, branch := range mergeTo {
			if strings.Contains(branch, ":") {
				if !strings.HasPrefix(branch, repo+":") {
					continue
				}

				branch = strings.TrimPrefix(branch, repo+":")
			}

			commentBody := fmt.Sprintf("%s was merged to `%s` branch\n<details><summary></summary>Created via API</details>", pr.GetHTMLURL(), branch)
			// Check if comment was already added
			commentFound := false

			for _, c := range comments {
				if strings.Contains(c.GetBody(), commentBody) {
					commentFound = true
					break
				}
			}

			if commentFound {
				continue
			}

			_, _, err = gh.Issues.CreateComment(context.Background(), org, match[2], num, &github.IssueComment{Body: &commentBody})
		}
	}

	return nil
}

func findLinkedPRs(org string, repo string, issueNum int) (pull_requests []string) {
	timeline, _, _ := gh.Issues.ListIssueTimeline(context.Background(), org, repo, issueNum, &github.ListOptions{})
	for _, t := range timeline {
		if t.GetEvent() == "cross-referenced" {
			if issue := t.GetSource().GetIssue(); issue != nil {
				if strings.Contains(issue.GetHTMLURL(), "/pull/") {
					pull_requests = append(pull_requests, issue.GetHTMLURL())
				}
			}
		}
	}

	return pull_requests
}

func main() {
	app := cli.NewApp()
	app.Before = func(c *cli.Context) error {
		if c.GlobalString("github-token") == "" {
			log.Println("Github auth not configured")
			cli.ShowAppHelpAndExit(c, 1)
		}

		initGithubClient(c.GlobalString("github-token"))
		return nil
	}
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "github-token, auth",
			Usage:  "Github personal auth token",
			EnvVar: "GITHUB_TOKEN",
		},
		cli.StringFlag{
			Name:   "github-org, org",
			Usage:  "Github organisation name or user name. Example: TykTechnologies",
			EnvVar: "GITHUB_ORG",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:  "project-release",
			Usage: "project-release <project-url> <column-name>",
			Flags: []cli.Flag{
				cli.StringSliceFlag{
					Name:  "merge-to, to",
					Usage: "Branches where given pull request should be merged to",
				},
				cli.BoolFlag{
					Name:  "dry-run",
					Usage: "See what is going to be merged, but do not push changes",
				},
				cli.BoolFlag{
					Name:  "only-missing",
					Usage: "List only missing commits either can be merged or having issues with merging",
				},
			},
			Action: func(c *cli.Context) error {
				projectURL := c.Args().First()
				projectURLRe := regexp.MustCompile(`https://github.com/orgs/([^/]+)/projects/(\d+)`)
				match := projectURLRe.FindAllStringSubmatch(projectURL, 1)
				if len(match) != 1 {
					log.Fatal("Project url not found")
				}
				org := match[0][1]
				projectNum, _ := strconv.Atoi(match[0][2])
				projects, _, err := gh.Organizations.ListProjects(context.Background(), org, &github.ProjectListOptions{})
				if err != nil {
					log.Fatal("Error during accessing projects:", err)
				}

				// Exchange project num for id, which required for API calls
				projectID := int64(0)
				for _, project := range projects {
					if project.GetNumber() == projectNum {
						projectID = project.GetID()
					}
				}

				if projectID == 0 {
					log.Fatal("Project not found")
				}

				columnName := c.Args().Get(1)
				columnID := int64(0)
				columns, _, _ := gh.Projects.ListProjectColumns(context.Background(), projectID, &github.ListOptions{})
				for _, column := range columns {
					if column.GetName() == columnName {
						columnID = column.GetID()
					}
				}
				if columnID == 0 {
					log.Fatal("Can't find column `" + columnName + "` in project")
				}

				cardContentURLRe := regexp.MustCompile(`https://api.github.com/repos/([^/]+)/([^/]+)/(issues|pull)/(\d+)`)
				cards, _, _ := gh.Projects.ListProjectCards(context.Background(), columnID, &github.ProjectCardListOptions{})
				for _, card := range cards {
					match := cardContentURLRe.FindAllStringSubmatch(card.GetContentURL(), 1)
					if len(match) == 0 {
						continue
					}

					if match[0][3] == "issues" {
						issueNum, _ := strconv.Atoi(match[0][4])
						issue, _, _ := gh.Issues.Get(context.Background(), match[0][1], match[0][2], issueNum)
						if issue != nil {
							fmt.Printf("%s\n%s\n", tm.Bold("["+match[0][4]+"] "+issue.GetTitle()), issue.GetHTMLURL())
						} else {
							fmt.Printf("%s Can't access %s\n", redCheck, card.GetContentURL())
							continue
						}
						pullRequests := findLinkedPRs(match[0][1], match[0][2], issueNum)
						issueUrl := fmt.Sprintf("https://github.com/%s/%s/issues/%d", match[0][1], match[0][2], issueNum)

						if len(pullRequests) == 0 {
							fmt.Printf("%s Can't find PR for Issue %s\n", redCheck, issueUrl)
						} else {
							fmt.Printf("%s Found %d PRs for Issue %s\n", greenCheck, len(pullRequests), issueUrl)
							for _, pr := range pullRequests {
								prmatch := prURLRe.FindAllStringSubmatch(pr, 1)
								prID, _ := strconv.Atoi(prmatch[0][4])
								if err := mergePR(prmatch[0][1], prmatch[0][2], prID, c.StringSlice("to"), true, c.Bool("dry-run"), c.Bool("only-missing")); err != nil {
									fmt.Printf("%s %s\n", redCheck, err)
								}
							}
						}
					} else {
						// merge
					}
				}

				return err
			},
		},
		{
			Name:  "release",
			Usage: "Release a pull request to specified branches",
			Flags: []cli.Flag{
				cli.StringSliceFlag{
					Name:  "merge-to, to",
					Usage: "Branches where given pull request should be merged to",
				},
				cli.BoolFlag{
					Name:  "dry-run",
					Usage: "See what is going to be merged, but do not push changes",
				},
			},
			Action: func(c *cli.Context) error {
				var repo, issue string

				target := c.Args().First()
				org := c.GlobalString("org")

				if strings.HasPrefix(target, "http") {
					match := prURLRe.FindAllStringSubmatch(target, 1)
					if len(match) > 0 {
						org = match[0][1]
						repo = match[0][2]
						issue = match[0][4]
					}
				} else if strings.Contains(target, "/") {
					target := strings.Split(c.Args().First(), "/")
					repo, issue = target[0], target[1]
				}

				if repo == "" || issue == "" {
					log.Fatal("Target should have <repo>/<pr-num> format or be URL to pull request")
				}

				issueNum, err := strconv.Atoi(issue)
				if err != nil {
					log.Fatal("Issue ID do not look like a number:", issue)
				}

				if err := mergePR(org, repo, issueNum, c.StringSlice("to"), false, c.Bool("dry-run"), false); err != nil {
					log.Fatal(err)
				}
				return nil
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
