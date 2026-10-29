package examples

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/google/go-github/v65/github"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// to filter for projects which are templates for the cli
const cliExampleTopic = "app-example"

type AppExamples struct {
	GitClient   *github.Client
	ExampleSpec ExampleSpec
}

func NewApp() *AppExamples {
	httpClient := httpx.Client()
	git := github.NewClient(httpClient)

	basicAuth, err := credentials.GetBasicAuth()
	if err != nil {
		basicAuth = credentials.Credentials{}
	}
	token, err := oauth.LoadToken()
	if err == nil {
		token = oauth.WaboAuthToken{}
	}

	spec := ExampleSpec{
		InstanceInformation: InstanceInformation{
			NovaApiHost: viper.GetString("host"),
			CellName:    viper.GetString("cellname"),
			BasicAuth: BasicAuth{
				Username: basicAuth.Username,
				Password: basicAuth.Password,
			},
			AccessToken: token.OAuth2Token.AccessToken,
		},
	}

	return &AppExamples{GitClient: git, ExampleSpec: spec}
}

// TODO maybe login is required since github has some rate limit in place for unauthenticated users
func (examples *AppExamples) ListExampleApps() ([]*github.Repository, error) {
	client := examples.GitClient

	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 30},
	}

	// get all pages of results
	var allRepos []*github.Repository
	for {
		repos, resp, err := client.Repositories.ListByOrg(context.TODO(), "wandelbotsgmbh", opt)
		if err != nil {
			return nil, fmt.Errorf("failed to list repos: %w", err)
		}
		defer resp.Body.Close()

		if repos == nil {
			return nil, fmt.Errorf("failed to list repos %s", resp.Status)
		}

		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	if allRepos == nil {
		return nil, fmt.Errorf("failed to list repos or no repos found")
	}

	templateRepos := make([]*github.Repository, 0)
	for _, repo := range allRepos {
		if isAppExample(repo) {
			templateRepos = append(templateRepos, repo)
		}
	}
	return templateRepos, nil
}

func (examples *AppExamples) DownloadAppTemplate(repo *github.Repository) error {
	// TODO better response and error handling
	client := examples.GitClient
	link, resp, err := client.Repositories.GetArchiveLink(context.TODO(), "wandelbotsgmbh", repo.GetName(), github.Zipball, nil, 0)
	if err != nil || !(resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound) {
		return fmt.Errorf("failed to get archive link: %w", err)
	}

	zipResp, err := http.Get(link.String())
	if err != nil || zipResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download archive: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(zipResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read archive: %w", err)
	}

	return UnzipGitZip(data, examples.ExampleSpec)
}

func isAppExample(repo *github.Repository) bool {
	if repo != nil && repo.Topics != nil && repo.IsTemplate != nil && *repo.IsTemplate {
		for _, topic := range repo.Topics {
			if topic == cliExampleTopic {
				return true
			}
		}
	}
	return false
}
