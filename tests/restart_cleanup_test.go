//go:build integration
// +build integration

package server_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"webtyp.com/server"
)

// TestRestartCleanup tests that RestartServer properly cleans up processes
func TestRestartCleanup(t *testing.T) {
	// Enable this test manually
	// t.Skip("integration test - enable manually")

	tmp := t.TempDir()

	// prepare public folder
	public := filepath.Join(tmp, "public")
	if err := os.MkdirAll(public, 0755); err != nil {
		t.Fatalf("creating public folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatalf("creating index: %v", err)
	}

	// pick a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Create a go.mod file
	gomod := `module temp
go 1.20
`
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatalf("creating go.mod: %v", err)
	}

	// Create initial server version
	serverV1 := fmt.Sprintf(`package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Error getting working directory: %%v", err)
	} else {
		log.Printf("Current working directory: %%s", wd)
	}

	publicDir := "public"
	absPublicDir, err := filepath.Abs(publicDir)
	if err != nil {
		log.Printf("Error getting absolute path for public dir: %%v", err)
	} else {
		log.Printf("Public directory absolute path: %%s", absPublicDir)
	}

	if _, err := os.Stat(publicDir); os.IsNotExist(err) {
		log.Printf("WARNING: Public directory '%%s' does not exist!", publicDir)
	} else {
		log.Printf("Public directory '%%s' exists", publicDir)
	}

	http.Handle("/", http.FileServer(http.Dir(publicDir)))

	http.HandleFunc("/h", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Server is running v1")
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Server is running")
	})

	fmt.Printf("Server starting on port %%s...\n", port)
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
`, port)

	sourceDir := filepath.Join(tmp, "src", "app")
	outputDir := filepath.Join(tmp, "deploy")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("creating source directory: %v", err)
	}

	mainPath := filepath.Join(sourceDir, "main.go")
	if err := os.WriteFile(mainPath, []byte(serverV1), 0644); err != nil {
		t.Fatalf("creating main.go: %v", err)
	}

	h := server.New()
	h.SetAppRootDir(tmp)
	h.SetSourceDir(filepath.ToSlash(strings.TrimPrefix(sourceDir, tmp+string(os.PathSeparator))))
	h.SetOutputDir(filepath.ToSlash(strings.TrimPrefix(outputDir, tmp+string(os.PathSeparator))))
	h.SetPort(fmt.Sprintf("%d", port))
	h = h.SetExitChan(make(chan bool)).
		SetLogger(t.Log)

	// Test 1: Initial start
	if err := h.SetExternalServerMode(true); err != nil {
		t.Fatalf("failed to set external server mode: %v", err)
	} // Ensure it uses gorun
	go h.StartServer(nil)

	t.Cleanup(func() {
		select {
		case h.ExitChan <- true:
		default:
		}
		h.StopServer()
	})

	// Wait and verify server responds
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://127.0.0.1:" + fmt.Sprintf("%d", port) + "/h"

	if !server.WaitForPortListening(fmt.Sprintf("%d", port), 5*time.Second, false) {
		t.Fatalf("Initial server not responding")
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Initial server not responding: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Initial server wrong status: %d", resp.StatusCode)
	}

	// Test 2: Check for process conflicts before restart
	checkCmd := exec.Command("lsof", "-i", ":"+fmt.Sprintf("%d", port))
	if out, err := checkCmd.CombinedOutput(); err == nil && len(out) > 0 {
		if t.Failed() {
			t.Logf("Port %d usage before restart:\n%s", port, string(out))
		}
	}

	// Test 3: Restart with new version
	// Create server v2
	serverV2 := fmt.Sprintf(`package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Error getting working directory: %%v", err)
	} else {
		log.Printf("Current working directory: %%s", wd)
	}

	publicDir := "public"
	absPublicDir, err := filepath.Abs(publicDir)
	if err != nil {
		log.Printf("Error getting absolute path for public dir: %%v", err)
	} else {
		log.Printf("Public directory absolute path: %%s", absPublicDir)
	}

	if _, err := os.Stat(publicDir); os.IsNotExist(err) {
		log.Printf("WARNING: Public directory '%%s' does not exist!", publicDir)
	} else {
		log.Printf("Public directory '%%s' exists", publicDir)
	}

	http.Handle("/", http.FileServer(http.Dir(publicDir)))

	http.HandleFunc("/h", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Server is running v2")
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Server is running")
	})

	fmt.Printf("Server starting on port %%s...\n", port)
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
`, port)

	// Write the updated version
	if err := os.WriteFile(mainPath, []byte(serverV2), 0644); err != nil {
		t.Fatalf("updating main.server.go: %v", err)
	}

	// Attempt restart
	err = h.RestartServer()
	if err != nil {
		// Check what's using the port now
		checkCmd2 := exec.Command("lsof", "-i", ":"+fmt.Sprintf("%d", port))
		if out, err := checkCmd2.CombinedOutput(); err == nil && len(out) > 0 {
			t.Logf("Port %d usage after failed restart:\n%s", port, string(out))
		}

		t.Fatalf("Restart failed: %v", err)
	}

	// Test 4: Verify new version is running
	if !server.WaitForPortListening(fmt.Sprintf("%d", port), 5*time.Second, false) {
		t.Fatalf("Restarted server not responding")
	}
	resp2, err := client.Get(url)
	if err != nil {
		t.Fatalf("Restarted server not responding: %v", err)
	}
	defer resp2.Body.Close()

	body := make([]byte, 100)
	n, _ := resp2.Body.Read(body)
	responseText := string(body[:n])

	if responseText != "Server is running v2" {
		t.Fatalf("Expected 'Server is running v2', got '%s'", responseText)
	}

	// Cleanup
	h.StopServer()
}
