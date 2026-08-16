//go:build ignore

// Probes a running RouterOS instance and reports which properties each menu
// actually returns, so "does this attribute exist on this version?" becomes a
// measurement instead of a guess.
//
// Running it against several RouterOS versions produces the per-attribute
// support matrix needed to decide whether a schema field should be gated,
// deprecated, or left alone.
//
//	go run test/chr/schema_matrix.go -menus test/chr/menus.txt > 7.23.json
//	go run test/chr/schema_matrix.go -diff 7.12.2.json,7.23.json
//
// Environment: ROS_HOSTURL, ROS_USERNAME, ROS_PASSWORD, ROS_INSECURE.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type menuReport struct {
	Menu       string   `json:"menu"`
	Properties []string `json:"properties"`
	Rows       int      `json:"rows"`
	Error      string   `json:"error,omitempty"`
}

type versionReport struct {
	Version string       `json:"version"`
	Menus   []menuReport `json:"menus"`
}

func main() {
	menusFile := flag.String("menus", "", "file with one RouterOS menu path per line")
	diff := flag.String("diff", "", "comma-separated report files to compare instead of probing")
	flag.Parse()

	if *diff != "" {
		if err := printDiff(strings.Split(*diff, ",")); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	menus, err := readMenus(*menusFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	client := newClient()
	report := versionReport{Version: probeVersion(client)}
	for _, menu := range menus {
		report.Menus = append(report.Menus, probeMenu(client, menu))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func readMenus(file string) ([]string, error) {
	if file == "" {
		return nil, fmt.Errorf("-menus is required")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var menus []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		menus = append(menus, line)
	}
	return menus, nil
}

type restClient struct {
	base string
	user string
	pass string
	http *http.Client
}

func newClient() *restClient {
	base := strings.TrimSuffix(os.Getenv("ROS_HOSTURL"), "/")
	if base == "" {
		fmt.Fprintln(os.Stderr, "ROS_HOSTURL must be set")
		os.Exit(1)
	}
	transport := &http.Transport{}
	if os.Getenv("ROS_INSECURE") == "true" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test harness against a throwaway CHR
	}
	return &restClient{
		base: base,
		user: os.Getenv("ROS_USERNAME"),
		pass: os.Getenv("ROS_PASSWORD"),
		http: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

func (c *restClient) get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+"/rest"+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if readErr != nil {
			break
		}
	}
	return buf, nil
}

func probeVersion(c *restClient) string {
	body, err := c.get("/system/resource")
	if err != nil {
		return "unknown"
	}
	var res map[string]any
	if err := json.Unmarshal(body, &res); err != nil {
		return "unknown"
	}
	if v, ok := res["version"].(string); ok {
		return v
	}
	return "unknown"
}

// probeMenu records the union of property names the menu reports. A singleton
// menu returns an object; a list menu returns an array.
func probeMenu(c *restClient, menu string) menuReport {
	report := menuReport{Menu: menu}

	body, err := c.get(menu)
	if err != nil {
		report.Error = err.Error()
		return report
	}

	seen := map[string]struct{}{}

	var asList []map[string]any
	if err := json.Unmarshal(body, &asList); err == nil {
		report.Rows = len(asList)
		for _, row := range asList {
			for k := range row {
				seen[k] = struct{}{}
			}
		}
	} else {
		var asObject map[string]any
		if err := json.Unmarshal(body, &asObject); err != nil {
			report.Error = "unparseable response"
			return report
		}
		report.Rows = 1
		for k := range asObject {
			seen[k] = struct{}{}
		}
	}

	for k := range seen {
		report.Properties = append(report.Properties, k)
	}
	sort.Strings(report.Properties)
	return report
}

// printDiff renders which properties appear in which version, so a reviewer can
// see at a glance when an attribute was introduced.
func printDiff(files []string) error {
	reports := make([]versionReport, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(strings.TrimSpace(file))
		if err != nil {
			return err
		}
		var report versionReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return err
		}
		reports = append(reports, report)
	}

	// menu -> property -> set of versions that report it
	index := map[string]map[string]map[string]bool{}
	for _, report := range reports {
		for _, menu := range report.Menus {
			if _, ok := index[menu.Menu]; !ok {
				index[menu.Menu] = map[string]map[string]bool{}
			}
			for _, prop := range menu.Properties {
				if _, ok := index[menu.Menu][prop]; !ok {
					index[menu.Menu][prop] = map[string]bool{}
				}
				index[menu.Menu][prop][report.Version] = true
			}
		}
	}

	menus := make([]string, 0, len(index))
	for menu := range index {
		menus = append(menus, menu)
	}
	sort.Strings(menus)

	fmt.Printf("%-44s", "property")
	for _, report := range reports {
		fmt.Printf("  %-12s", report.Version)
	}
	fmt.Println()

	for _, menu := range menus {
		fmt.Printf("\n%s\n", menu)
		props := make([]string, 0, len(index[menu]))
		for prop := range index[menu] {
			props = append(props, prop)
		}
		sort.Strings(props)

		for _, prop := range props {
			// Only interesting when support differs across the versions compared.
			all := true
			for _, report := range reports {
				if !index[menu][prop][report.Version] {
					all = false
					break
				}
			}
			if all {
				continue
			}
			fmt.Printf("  %-42s", prop)
			for _, report := range reports {
				mark := "-"
				if index[menu][prop][report.Version] {
					mark = "yes"
				}
				fmt.Printf("  %-12s", mark)
			}
			fmt.Println()
		}
	}
	return nil
}
