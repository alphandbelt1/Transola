package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"transola/internal/meta"
)

type cliConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: transola <command> [options]\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Commands: health, login, upload, download, list, link\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	switch cmd {
	case "health":
		fs := flag.NewFlagSet("health", flag.ExitOnError)
		url := fs.String("url", "http://127.0.0.1:8080", "Server URL")
		_ = fs.Parse(args)
		if err := runHealth(*url); err != nil {
			log.Fatalf("health check failed: %v", err)
		}
	case "login":
		fs := flag.NewFlagSet("login", flag.ExitOnError)
		url := fs.String("url", "http://127.0.0.1:8080", "Server URL")
		email := fs.String("email", "", "Email")
		password := fs.String("password", "", "Password")
		_ = fs.Parse(args)
		if err := runLogin(*url, *email, *password); err != nil {
			log.Fatalf("login failed: %v", err)
		}
	case "upload":
		fs := flag.NewFlagSet("upload", flag.ExitOnError)
		file := fs.String("file", "", "Path to file")
		owner := fs.String("owner", "", "Owner email (defaults to your account)")
		url := fs.String("url", "", "Server URL (optional, defaults to saved config)")
		_ = fs.Parse(args)
		if err := runUpload(*file, *owner, *url); err != nil {
			log.Fatalf("upload failed: %v", err)
		}
	case "download":
		fs := flag.NewFlagSet("download", flag.ExitOnError)
		id := fs.String("id", "", "File ID")
		out := fs.String("out", "", "Output path (defaults to ID)")
		url := fs.String("url", "", "Server URL (optional, defaults to saved config)")
		_ = fs.Parse(args)
		if err := runDownload(*id, *out, *url); err != nil {
			log.Fatalf("download failed: %v", err)
		}
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		url := fs.String("url", "", "Server URL (optional, defaults to saved config)")
		owner := fs.String("owner", "", "Owner email (admin only)")
		typeFilter := fs.String("type", "", "Filter by type (image, video, audio, text, application)")
		since := fs.String("since", "", "Since time RFC3339")
		until := fs.String("until", "", "Until time RFC3339")
		limit := fs.Int("limit", 100, "Limit results")
		_ = fs.Parse(args)
		if err := runList(*url, *owner, *typeFilter, *since, *until, *limit); err != nil {
			log.Fatalf("list failed: %v", err)
		}
	case "link":
		fs := flag.NewFlagSet("link", flag.ExitOnError)
		id := fs.String("id", "", "File ID")
		url := fs.String("url", "", "Server URL (optional, defaults to saved config)")
		_ = fs.Parse(args)
		if err := runLink(*id, *url); err != nil {
			log.Fatalf("link failed: %v", err)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func runHealth(baseURL string) error {
	target := strings.TrimRight(baseURL, "/") + "/health"
	resp, err := http.Get(target) // #nosec G107 - URL is user-provided CLI flag
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	status, _ := payload["status"].(string)
	version, _ := payload["version"].(string)
	fmt.Printf("server status: %s\n", status)
	fmt.Printf("server version: %s\n", version)
	fmt.Printf("cli version: %s\n", meta.Version)
	if root, ok := payload["storage_root"].(string); ok {
		fmt.Printf("storage root: %s\n", root)
	}
	return nil
}

func runLogin(baseURL, email, password string) error {
	if email == "" {
		return errors.New("email is required")
	}
	if password == "" {
		fmt.Print("Password: ")
		fmt.Scanln(&password)
	}

	payload := map[string]string{
		"email":    email,
		"password": password,
	}
	body, _ := json.Marshal(payload)
	target := strings.TrimRight(baseURL, "/") + "/v1/auth/login"
	resp, err := http.Post(target, "application/json", strings.NewReader(string(body))) // #nosec G107
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("login failed: %s", strings.TrimSpace(string(b)))
	}
	var decoded struct {
		AccessToken string                 `json:"access_token"`
		User        map[string]interface{} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	cfg := cliConfig{
		Server: baseURL,
		Token:  decoded.AccessToken,
	}
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("login ok; token saved")
	return nil
}

func runUpload(path, owner, urlOverride string) error {
	if path == "" {
		return errors.New("file is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	baseURL := pickURL(cfg.Server, urlOverride)

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	sumHex := hex.EncodeToString(hasher.Sum(nil))
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}

	req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/v1/files", file)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", filepath.Base(path))
	req.Header.Set("X-Sha256", sumHex)
	if owner != "" {
		req.Header.Set("X-Owner", owner)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %s", strings.TrimSpace(string(b)))
	}
	var decoded map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	fmt.Println("upload ok")
	if fileMeta, ok := decoded["file"].(map[string]interface{}); ok {
		if id, ok := fileMeta["id"].(string); ok {
			fmt.Printf("file id: %s\n", id)
		}
		if sha, ok := fileMeta["sha256"].(string); ok {
			fmt.Printf("sha256: %s\n", sha)
		}
	}
	return nil
}

func runDownload(id, output, urlOverride string) error {
	if id == "" {
		return errors.New("id is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	baseURL := pickURL(cfg.Server, urlOverride)

	target := strings.TrimRight(baseURL, "/") + "/v1/files/" + id + "/content"
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("download failed: %s", strings.TrimSpace(string(b)))
	}
	if output == "" {
		output = id
	}
	outFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outFile.Close()
	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return err
	}
	fmt.Printf("downloaded to %s\n", output)
	return nil
}

func runLink(id, urlOverride string) error {
	if id == "" {
		return errors.New("id is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	baseURL := pickURL(cfg.Server, urlOverride)
	target := strings.TrimRight(baseURL, "/") + "/v1/files/" + id + "/link"
	req, err := http.NewRequest("POST", target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("link failed: %s", strings.TrimSpace(string(b)))
	}
	var decoded struct {
		Token     string `json:"token"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	fmt.Printf("download link (expires %s):\n%s\n", decoded.ExpiresAt, decoded.URL)
	fmt.Printf("token: %s\n", decoded.Token)
	return nil
}

func runList(urlOverride, owner, typeFilter, since, until string, limit int) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	baseURL := pickURL(cfg.Server, urlOverride)
	target := strings.TrimRight(baseURL, "/") + "/v1/files"
	queryParts := []string{}
	if owner != "" {
		queryParts = append(queryParts, "owner="+url.QueryEscape(owner))
	}
	if typeFilter != "" {
		queryParts = append(queryParts, "type="+url.QueryEscape(typeFilter))
	}
	if since != "" {
		queryParts = append(queryParts, "since="+url.QueryEscape(since))
	}
	if until != "" {
		queryParts = append(queryParts, "until="+url.QueryEscape(until))
	}
	if limit > 0 {
		queryParts = append(queryParts, "limit="+fmt.Sprintf("%d", limit))
	}
	if len(queryParts) > 0 {
		target += "?" + strings.Join(queryParts, "&")
	}

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("list failed: %s", strings.TrimSpace(string(b)))
	}
	var decoded struct {
		Files []map[string]interface{} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if len(decoded.Files) == 0 {
		fmt.Println("no files")
		return nil
	}
	for _, f := range decoded.Files {
		fmt.Printf("%s | %s | %s bytes | owner=%s\n", asString(f["id"]), asString(f["filename"]), asString(f["size"]), asString(f["owner"]))
	}
	return nil
}

func asString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	default:
		return ""
	}
}

func pickURL(saved, override string) string {
	if override != "" {
		return override
	}
	if saved != "" {
		return saved
	}
	return "http://127.0.0.1:8080"
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "transola", "config.json"), nil
}

func loadConfig() (cliConfig, error) {
	path, err := configPath()
	if err != nil {
		return cliConfig{}, err
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return cliConfig{}, errors.New("not logged in; run transola login")
	}
	cfg := cliConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cliConfig{}, err
	}
	return cfg, nil
}

func saveConfig(cfg cliConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, payload, 0o600)
}
