package main

import (
	"fmt"
	"os"
	"path/filepath"

	"air-traffic-control/backend/pkg/hostcache"
)

func main() {
	tmpDir := os.TempDir()
	dbPath := filepath.Join(tmpDir, "test_readonly.sqlite")

	// Create a database with some data using Open (write mode)
	store, err := hostcache.Open(dbPath)
	if err != nil {
		fmt.Printf("Open failed: %v\n", err)
		os.Exit(1)
	}
	
	testHost := hostcache.NodeRecord{
		ID:        "test-host-1",
		Name:      "test-host",
		ShortName: "test",
		Group:     "test-group",
		IP:        "10.0.0.1",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  []byte(`{"id":"test-host-1","name":"test-host"}`),
	}
	if err := store.UpsertHost(testHost); err != nil {
		fmt.Printf("UpsertHost failed: %v\n", err)
		os.Exit(1)
	}
	_ = store.Close()

	// Now open read-only - should not reset schema
	roStore, err := hostcache.OpenReadOnly(dbPath)
	if err != nil {
		fmt.Printf("OpenReadOnly failed: %v\n", err)
		os.Exit(1)
	}
	defer roStore.Close()

	// Verify we can read the data
	hosts, err := roStore.GetAllHosts()
	if err != nil {
		fmt.Printf("GetAllHosts failed: %v\n", err)
		os.Exit(1)
	}
	
	if len(hosts) != 1 || hosts[0].ID != "test-host-1" {
		fmt.Printf("Read-only mode failed: expected 1 host, got %v\n", hosts)
		os.Exit(1)
	}
	
	fmt.Println("Read-only mode works correctly!")
}
