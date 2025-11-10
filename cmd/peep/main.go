package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiclient "github.com/splax/localvercel/pkg/api/client"
	"golang.org/x/term"
)

type cliConfig struct {
	APIBaseURL  string `json:"api_base_url"`
	AccessToken string `json:"access_token"`
}

var buildVersion = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "login":
		err = commandLogin(args)
	case "project":
		err = commandProject(args)
	case "deploy":
		err = commandDeploy(args)
	case "version", "--version", "-v":
		printVersion()
		return
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func commandLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	email := fs.String("email", "", "Email address")
	password := fs.String("password", "", "Password (supply to avoid prompt)")
	apiBase := fs.String("api", "", "API base URL (default http://localhost:4000)")
	useDevice := fs.Bool("device", true, "Use device-code flow (set to false for legacy login)")
	fs.Parse(args)

	if strings.TrimSpace(*email) == "" {
		return errors.New("--email is required")
	}

	secret := strings.TrimSpace(*password)
	if secret == "" {
		fmt.Print("Password: ")
		bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Print("\n")
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		secret = string(bytes)
	}

	cfg, _ := loadConfig()
	if strings.TrimSpace(*apiBase) != "" {
		cfg.APIBaseURL = *apiBase
	} else if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "http://localhost:4000"
	}

	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}

	if !*useDevice {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := client.Login(ctx, *email, secret)
		if err != nil {
			return err
		}
		cfg.AccessToken = resp.Tokens.AccessToken
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Println("login successful")
		return nil
	}

	tokens, err := deviceLogin(context.Background(), client, *email, secret)
	if err != nil {
		return err
	}
	cfg.AccessToken = tokens.AccessToken
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Println("login successful")
	return nil
}

func deviceLogin(ctx context.Context, client *apiclient.Client, email, password string) (apiclient.TokenPair, error) {
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	start, err := client.StartDeviceAuthorization(startCtx)
	cancel()
	if err != nil {
		return apiclient.TokenPair{}, err
	}
	expires := time.Duration(start.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(expires)
	fmt.Printf("Device verification code: %s\n", start.UserCode)
	fmt.Printf("Verification URL: %s\n", start.VerificationURL)
	fmt.Println("Approving device...")

	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := client.VerifyDeviceAuthorization(verifyCtx, start.UserCode, email, password); err != nil {
		cancel()
		return apiclient.TokenPair{}, err
	}
	cancel()
	fmt.Println("Authorization pending, polling for tokens...")

	for {
		if time.Now().After(deadline) {
			return apiclient.TokenPair{}, errors.New("authorization timed out")
		}
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := client.PollDeviceAuthorization(pollCtx, start.DeviceCode)
		cancel()
		if err != nil {
			return apiclient.TokenPair{}, err
		}
		switch strings.ToLower(resp.Status) {
		case "approved":
			if resp.Tokens == nil {
				return apiclient.TokenPair{}, errors.New("authorization approved but tokens unavailable")
			}
			return *resp.Tokens, nil
		case "pending":
			if resp.Interval > 0 {
				interval = time.Duration(resp.Interval) * time.Second
			}
		case "expired":
			return apiclient.TokenPair{}, errors.New("device code expired")
		case "consumed":
			return apiclient.TokenPair{}, errors.New("device code already used")
		default:
			return apiclient.TokenPair{}, fmt.Errorf("unexpected device status: %s", resp.Status)
		}
		time.Sleep(interval)
	}
}

func commandProject(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: peep project [list|create]")
	}
	sub := args[0]
	switch sub {
	case "list":
		return projectList(args[1:])
	case "create":
		return projectCreate(args[1:])
	default:
		return fmt.Errorf("unknown project command: %s", sub)
	}
}

func projectList(args []string) error {
	fs := flag.NewFlagSet("project list", flag.ExitOnError)
	teamID := fs.String("team", "", "Team identifier")
	limit := fs.Int("limit", 0, "Maximum number of projects to display")
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return errors.New("please login first using 'peep login'")
	}
	if strings.TrimSpace(*teamID) == "" {
		return errors.New("--team is required")
	}

	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	projects, err := client.ListProjects(ctx, token, *teamID)
	if err != nil {
		return err
	}
	count := len(projects)
	if *limit > 0 && *limit < count {
		count = *limit
	}
	for i := 0; i < count; i++ {
		p := projects[i]
		fmt.Printf("%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Type, p.RepoURL)
	}
	return nil
}

func projectCreate(args []string) error {
	fs := flag.NewFlagSet("project create", flag.ExitOnError)
	teamID := fs.String("team", "", "Team identifier")
	name := fs.String("name", "", "Project name")
	repo := fs.String("repo", "", "Repository URL")
	projType := fs.String("type", "frontend", "Project type (frontend|backend)")
	build := fs.String("build", "", "Optional build command")
	run := fs.String("run", "", "Optional run command")
	fs.Parse(args)

	if strings.TrimSpace(*teamID) == "" {
		return errors.New("--team is required")
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("--name is required")
	}
	if strings.TrimSpace(*repo) == "" {
		return errors.New("--repo is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return errors.New("please login first using 'peep login'")
	}

	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}
	input := apiclient.CreateProjectInput{
		TeamID:       *teamID,
		Name:         *name,
		RepoURL:      *repo,
		Type:         *projType,
		BuildCommand: *build,
		RunCommand:   *run,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	project, err := client.CreateProject(ctx, token, input)
	if err != nil {
		return err
	}
	fmt.Printf("project created: %s (%s)\n", project.ID, project.Name)
	return nil
}

func commandDeploy(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: peep deploy [trigger|list|delete]")
	}
	sub := args[0]
	switch sub {
	case "trigger":
		return deployTrigger(args[1:])
	case "list":
		return deployList(args[1:])
	case "delete":
		return deployDelete(args[1:])
	default:
		return fmt.Errorf("unknown deploy command: %s", sub)
	}
}

func deployTrigger(args []string) error {
	fs := flag.NewFlagSet("deploy trigger", flag.ExitOnError)
	projectID := fs.String("project", "", "Project identifier")
	commit := fs.String("commit", "", "Commit SHA")
	fs.Parse(args)

	if strings.TrimSpace(*projectID) == "" {
		return errors.New("--project is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return errors.New("please login first using 'peep login'")
	}

	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dep, err := client.TriggerDeployment(ctx, token, *projectID, *commit)
	if err != nil {
		return err
	}
	fmt.Printf("deployment triggered: %s status=%s\n", dep.ID, dep.Status)
	return nil
}

func deployList(args []string) error {
	fs := flag.NewFlagSet("deploy list", flag.ExitOnError)
	projectID := fs.String("project", "", "Project identifier")
	limit := fs.Int("limit", 5, "Maximum number of deployments")
	fs.Parse(args)

	if strings.TrimSpace(*projectID) == "" {
		return errors.New("--project is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return errors.New("please login first using 'peep login'")
	}
	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	deployments, err := client.ListDeployments(ctx, token, *projectID, *limit)
	if err != nil {
		return err
	}
	for _, dep := range deployments {
		fmt.Printf("%s\t%s\t%s\t%s\n", dep.ID, dep.Status, dep.Stage, dep.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func deployDelete(args []string) error {
	fs := flag.NewFlagSet("deploy delete", flag.ExitOnError)
	deploymentID := fs.String("deployment", "", "Deployment identifier")
	fs.Parse(args)
	if strings.TrimSpace(*deploymentID) == "" {
		return errors.New("--deployment is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return errors.New("please login first using 'peep login'")
	}

	client, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.DeleteDeployment(ctx, token, *deploymentID); err != nil {
		return err
	}
	fmt.Println("deployment deleted")
	return nil
}

func loadConfig() (cliConfig, error) {
	path, err := configPath()
	if err != nil {
		return cliConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cliConfig{APIBaseURL: "http://localhost:4000"}, nil
		}
		return cliConfig{}, err
	}
	var cfg cliConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cliConfig{}, err
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "http://localhost:4000"
	}
	return cfg, nil
}

func saveConfig(cfg cliConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func configPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "peep", "config.json"), nil
}

func printUsage() {
	fmt.Printf("peep CLI %s\n\n", buildVersion)
	fmt.Print(`Usage:
	peep login --email user@example.com [--password secret] [--api http://localhost:4000]
	peep project list --team <team-id>
	peep project create --team <team-id> --name <name> --repo <url> [--type frontend|backend] [--build cmd] [--run cmd]
	peep deploy trigger --project <project-id> [--commit sha]
	peep deploy list --project <project-id> [--limit N]
	peep deploy delete --deployment <deployment-id>
	peep version
`)
}

func printVersion() {
	fmt.Println(strings.TrimSpace(buildVersion))
}
