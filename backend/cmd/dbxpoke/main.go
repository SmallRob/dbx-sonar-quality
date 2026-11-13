// dbxpoke verifies that a packaged sidecar speaks the DBX JSON-RPC protocol.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dbxpoke <sidecar-binary>")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1])
	cmd.Env = append(os.Environ(), "DBX_PLUGIN_DATA_DIR=")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	reader := bufio.NewReader(stdout)
	send := func(id int, method string, params any, target any) error {
		line, _ := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
		if _, err := stdin.Write(append(line, '\n')); err != nil {
			return err
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			text, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("%s: %w (%s)", method, err, stderr.String())
			}
			var resp response
			if json.Unmarshal([]byte(text), &resp) != nil {
				continue
			}
			if resp.ID != id {
				continue
			}
			if resp.Error != nil {
				return fmt.Errorf("%s: %s", method, resp.Error.Message)
			}
			if target != nil {
				return json.Unmarshal(resp.Result, target)
			}
			return nil
		}
		return fmt.Errorf("%s timed out", method)
	}
	var init struct {
		ProtocolVersion int `json:"protocolVersion"`
		Plugin          struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"plugin"`
	}
	if err := send(1, "plugin/initialize", map[string]any{"host": map[string]any{"protocolVersions": []int{1}}}, &init); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL", err)
		os.Exit(1)
	}
	var ping map[string]any
	if err := send(2, "ping", nil, &ping); err != nil || ping["ok"] != true {
		fmt.Fprintln(os.Stderr, "FAIL ping", err)
		os.Exit(1)
	}
	var cfg map[string]any
	if err := send(3, "config/get", nil, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL config/get", err)
		os.Exit(1)
	}
	if _, exists := cfg["password"]; exists {
		fmt.Fprintln(os.Stderr, "FAIL config/get leaked password")
		os.Exit(1)
	}
	fmt.Printf("PASS initialize protocol=%d id=%s version=%s; ping and credential redaction ok\n", init.ProtocolVersion, init.Plugin.ID, init.Plugin.Version)
	_ = stdin.Close()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			fmt.Fprintln(os.Stderr, "FAIL sidecar exit", err)
			os.Exit(1)
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		fmt.Fprintln(os.Stderr, "FAIL sidecar did not exit")
		os.Exit(1)
	}
}
