package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// cdp is a Chrome DevTools Protocol connection over --remote-debugging-pipe.
type cdp struct {
	t   *testing.T
	w   io.Writer
	r   *bufio.Reader
	id  int
	log strings.Builder
}

// call sends a command and returns its result. It adds the console and log events that come before the reply to c.log.
func (c *cdp) call(session, method string, params any, out any) {
	c.t.Helper()
	c.id++
	b, err := json.Marshal(map[string]any{"id": c.id, "sessionId": session, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.w.Write(append(b, 0)); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	for {
		b, err := c.r.ReadBytes(0)
		if err != nil {
			c.t.Fatalf("%s: %v\n%s", method, err, c.log.String())
		}
		var m struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct{ Message string }
		}
		if err := json.Unmarshal(b[:len(b)-1], &m); err != nil {
			c.t.Fatal(err)
		}
		if m.Method == "Log.entryAdded" || m.Method == "Runtime.consoleAPICalled" || m.Method == "Runtime.exceptionThrown" {
			c.log.Write(m.Params)
			c.log.WriteByte('\n')
		}
		if m.ID != c.id {
			continue
		}
		if m.Error != nil {
			c.t.Fatalf("%s: %s", method, m.Error.Message)
		}
		if out != nil {
			if err := json.Unmarshal(m.Result, out); err != nil {
				c.t.Fatal(err)
			}
		}
		return
	}
}

// chromePage opens the page in headless Chromium, waits until the status is not "Planning...", and returns the DOM
// and the browser log. --dump-dom does not wait for the Web Worker reply, so this uses the DevTools Protocol.
func chromePage(t *testing.T, chrome, profile, url string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-first-run", "--no-sandbox",
		"--user-data-dir="+profile, "--remote-debugging-pipe", "about:blank")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.ExtraFiles = []*os.File{inR, outW}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	defer func() {
		inW.Close()
		if err := cmd.Wait(); err != nil && !t.Failed() {
			t.Errorf("chromium: %v\n%s", err, stderr.String())
		}
	}()
	c := &cdp{t: t, w: inW, r: bufio.NewReader(outR)}
	var tg struct{ TargetID string }
	c.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &tg)
	var at struct{ SessionID string }
	c.call("", "Target.attachToTarget", map[string]any{"targetId": tg.TargetID, "flatten": true}, &at)
	s := at.SessionID
	c.call(s, "Log.enable", nil, nil)
	c.call(s, "Runtime.enable", nil, nil)
	c.call(s, "Page.navigate", map[string]any{"url": url}, nil)
	for {
		var r struct{ Result struct{ Value string } }
		c.call(s, "Runtime.evaluate", map[string]any{"returnByValue": true, "expression": `(() => {
  const e = document.getElementById("status");
  return document.readyState === "complete" && e && e.textContent !== "Planning..." ? document.documentElement.outerHTML : "";
})()`}, &r)
		if r.Result.Value != "" {
			c.call("", "Browser.close", nil, nil)
			return r.Result.Value, c.log.String()
		}
		if ctx.Err() != nil {
			t.Fatalf("the page did not finish: %s", c.log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

var statusRe = regexp.MustCompile(`(?s)<p class="status[^"]*" id="status"[^>]*>(.*?)</p>`)

// TestPage builds the page with the fixture data and opens it in headless Chromium with a hostile share link.
func TestPage(t *testing.T) {
	chrome := lookPath(t, "CHROME", "chromium", "chromium-browser", "google-chrome", "chrome")
	def, err := os.ReadFile(filepath.Join("testdata", "defaults.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := page(filepath.Join("testdata", "game"), def)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "index.html")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
	share := func(v any) string {
		j, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return "file://" + file + "#cfg=j." + base64.RawURLEncoding.EncodeToString(j)
	}
	xss := `1"><img src=x id=pwn>`
	profile := t.TempDir()
	dom, log := chromePage(t, chrome, profile, share(map[string]any{
		"assumptions": map[string]any{"startItems": 5, "lights": nil, "startINT": 800, "descendXP": "80",
			"extraGathers": []any{map[string]any{"layer": xss, "item": "wood", "count": xss}}},
		"targets": []string{"blade", "plank"},
	}))
	check := func(what, dom, log string) {
		t.Helper()
		if strings.Contains(log, "Content Security Policy") {
			t.Errorf("%s: CSP blocked something:\n%s", what, log)
		}
		if strings.Contains(dom, `id="pwn"`) {
			t.Errorf("%s: the share link put an element in the page", what)
		}
	}
	check("plan", dom, log)
	m := statusRe.FindStringSubmatch(dom)
	if m == nil || !strings.Contains(html.UnescapeString(m[1]), "Planned") {
		t.Fatalf("status %q, want a plan", m)
	}
	dom, log = chromePage(t, chrome, profile, "file://"+file+"#setup")
	check("setup", dom, log)
	if !strings.Contains(dom, `data-f="layer"`) || !strings.Contains(dom, "Start INT") {
		t.Fatal("the setup view does not show the settings")
	}

	dom, log = chromePage(t, chrome, t.TempDir(), share(map[string]any{
		"assumptions": map[string]any{"maxLayer": 1}, "options": map[string]any{"gambleMode": "off"}, "targets": []string{"helmet"},
	}))
	check("no steps", dom, log)
	if !strings.Contains(dom, "The plan has no steps") {
		t.Fatal("the page does not show a plan without steps")
	}
}
