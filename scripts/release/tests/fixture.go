// Local lifecycle substitute only. Never copied into a release bundle by the build script.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"))
	if len(os.Args) > 1 && os.Args[1] == "version" && name == "go" {
		command := strings.TrimSuffix(filepath.Base(os.Args[len(os.Args)-1]), ".exe")
		fmt.Println("fixture: go1.27.1\n\tpath\tgithub.com/moonseries667/AllDownloads-v2/backend/go/cmd/" + command + "\n\tbuild\t-trimpath=true\n\tbuild\tGOOS=windows\n\tbuild\tGOARCH=amd64")
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "version") {
		if capture := os.Getenv("ALLDOWNLOADS_RELEASE_TEST_ENV_FILE"); capture != "" {
			data, _ := json.Marshal(map[string]any{"tool": name, "USERPROFILE": os.Getenv("USERPROFILE"), "HOME": os.Getenv("HOME"), "TDL_DEBUG": os.Getenv("TDL_DEBUG"), "RCLONE_CONFIG": os.Getenv("RCLONE_CONFIG"), "args": os.Args[1:]})
			if err := os.WriteFile(capture, data, 0600); err != nil {
				panic(err)
			}
		}
		switch name {
		case "java":
			fmt.Fprintln(os.Stderr, `openjdk version "21.0.12"`)
		case "tdl":
			fmt.Println("Version: 0.20.4")
		case "rclone":
			fmt.Println("rclone v1.74.4")
		default:
			fmt.Println(name + " version 9.0.2-fixture")
		}
		return
	}
	root, _ := os.Getwd()
	if name == "runner" {
		if _, err := os.Stat(filepath.Join(root, "fail-runner")); err == nil {
			fmt.Fprintln(os.Stderr, "fixture deliberate runner failure")
			os.Exit(12)
		}
		_ = os.WriteFile(filepath.Join(root, "runtime", "runner-active"), []byte("active"), 0600)
		for {
			time.Sleep(time.Second)
		}
	}
	if name != "java" {
		os.Exit(11)
	}
	config := ""
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--spring.config.additional-location=") {
			u, err := url.Parse(strings.TrimPrefix(arg, "--spring.config.additional-location="))
			if err != nil {
				panic(err)
			}
			config = filepath.FromSlash(strings.TrimPrefix(u.Path, "/"))
		}
	}
	f, err := os.Open(config)
	if err != nil {
		panic(err)
	}
	port := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "server.port=") {
			port = strings.TrimPrefix(scanner.Text(), "server.port=")
		}
	}
	_ = f.Close()
	if _, err := strconv.Atoi(port); err != nil {
		panic("invalid test port")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		goState := "NOT_CONFIGURED"
		if _, err := os.Stat(filepath.Join(root, "runtime", "runner-active")); err == nil {
			goState = "ACTIVE"
		}
		w.Header().Set("Content-Type", "application/json")
		mount := map[string]any{"state": "MOUNTED", "reasonCode": nil}
		if _, err := os.Stat(filepath.Join(root, "mount-pending")); err == nil {
			mount = map[string]any{"state": "NOT_MOUNTED", "reasonCode": "WINFSP_REQUIRED"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"runtime": map[string]any{"services": []map[string]string{{"id": "java", "state": "ACTIVE"}, {"id": "go", "state": goState, "lastSeenAt": time.Now().UTC().Format(time.RFC3339Nano)}}, "mount": mount}}})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(root, "fail-front")); err == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintln(w, `<!doctype html><div id="app"></div>`)
	})
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		panic(err)
	}
}
