package groupassistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testControls(t *testing.T, approve func(context.Context, string) (bool, error)) *ControlTools {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "turn")
	if err := os.MkdirAll(filepath.Join(work, "mine"), 0700); err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(base, "tools")
	cfg := Config{SoftwareDir: filepath.Join(base, "software"), WorkDir: base, CodexHome: filepath.Join(base, "home"), AllowedHosts: []string{"*"}, ToolRoots: []string{tools}, PythonCommand: filepath.Join(tools, "python.exe"), NodeCommand: filepath.Join(tools, "node.exe"), NpmCLI: filepath.Join(tools, "npm-cli.js")}
	c, err := NewControlTools(cfg, &Snapshot{Dir: work, Identity: Identity{"group", "wecom:alice"}}, approve)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func jsonArgs(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

func TestControlledDocumentDownloadIsAutomaticAndConfined(t *testing.T) {
	approvals := 0
	c := testControls(t, func(context.Context, string) (bool, error) { approvals++; return false, nil })
	requests := 0
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\nfixture")), Request: req}, nil
	})
	result, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/report.pdf", "target": "report.pdf", "purpose": "document"}))
	if err != nil || !strings.Contains(result, "sha256=") || approvals != 0 || requests != 1 {
		t.Fatal(result, err, approvals, requests)
	}
	if data, err := c.mine.ReadFile("report.pdf"); err != nil || string(data) != "%PDF-1.7\nfixture" {
		t.Fatal(string(data), err)
	}
	for _, target := range []string{"../outside.pdf", "public/a.pdf", "C:/outside.pdf", "a\\b.pdf"} {
		// public/a.pdf is simply a member-owned nested name, not public authority.
		if target == "public/a.pdf" {
			continue
		}
		_, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/report.pdf", "target": target, "purpose": "document"}))
		if err == nil {
			t.Fatal("unsafe output path accepted", target)
		}
	}
	if requests != 1 {
		t.Fatal("rejected path performed HTTP request", requests)
	}
}

func TestControlledSoftwareDownloadRequiresApprovalBeforeRequest(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "approve"}[allow], func(t *testing.T) {
			requests, approvals := 0, 0
			c := testControls(t, func(_ context.Context, summary string) (bool, error) {
				approvals++
				if requests != 0 || !strings.Contains(summary, "script.py") {
					t.Fatal("software was requested before owner review", requests, summary)
				}
				return allow, nil
			})
			c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("print('fixture')")), Request: req}, nil
			})
			_, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/script.py", "target": "script.py", "purpose": "document"}))
			if approvals != 1 || allow && (err != nil || requests != 1) || !allow && (!errors.Is(err, ErrDenied) || requests != 0) {
				t.Fatal(approvals, requests, err)
			}
			_, statErr := c.mine.Stat("script.py")
			if allow && statErr != nil || !allow && !os.IsNotExist(statErr) {
				t.Fatal(statErr)
			}
		})
	}
}

func TestControlledDownloadRedirectDoesNotHideSoftwareOrPrivateHost(t *testing.T) {
	for _, location := range []string{"https://example.test/script.py", "http://127.0.0.1/admin", "https://private.test/a.pdf"} {
		requests, approvals := 0, 0
		c := testControls(t, func(context.Context, string) (bool, error) {
			approvals++
			if requests != 1 {
				t.Fatal("redirect approval was late")
			}
			return false, nil
		})
		c.cfg.AllowedHosts = []string{"example.test"}
		c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})
		_, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/report.pdf", "target": "report.pdf", "purpose": "document"}))
		if err == nil || requests != 1 || location == "https://example.test/script.py" && approvals != 1 || location != "https://example.test/script.py" && approvals != 0 {
			t.Fatal(location, requests, approvals, err)
		}
	}
}

func TestControlledDownloadRejectsDisguisedPayloadAndIdentityInjection(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) {
		t.Fatal("ambiguous payload must be retried with explicit software request")
		return false, nil
	})
	requests := 0
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(strings.NewReader("MZfake executable")), Request: req}, nil
	})
	_, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/a.png", "target": "a.png", "purpose": "document"}))
	if err == nil {
		t.Fatal("extension/type masquerade accepted")
	}
	if _, err := c.mine.Stat("a.png"); !os.IsNotExist(err) {
		t.Fatal("unreviewed executable was saved", err)
	}
	_, err = c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/a.png", "target": "a.png", "purpose": "document", "owner": "wecom:owner"}))
	if !errors.Is(err, ErrInvalid) || requests != 1 {
		t.Fatal("model supplied owner accepted", err, requests)
	}
}

func TestControlledPreviewStripsScriptsAndReturnsUntrustedSourceText(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) {
		t.Fatal("preview unexpectedly prompted")
		return false, nil
	})
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(`<html><script>danger()</script><p>useful information</p></html>`)), Request: req}, nil
	})
	result, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/", "target": "", "purpose": "preview"}))
	if err != nil || !strings.Contains(result, "untrusted") || !strings.Contains(result, "useful information") || strings.Contains(result, "danger()") {
		t.Fatal(result, err)
	}
}

func TestControlledPreviewRedirectCannotFetchSoftware(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) {
		t.Fatal("preview cannot authorize software")
		return false, nil
	})
	requests := 0
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://example.test/script.py"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := c.Handle(context.Background(), "workspace_fetch", jsonArgs(map[string]string{"url": "https://example.test/", "target": "", "purpose": "preview"}))
	if !errors.Is(err, ErrDenied) || requests != 1 {
		t.Fatal("preview followed software redirect", requests, err)
	}
}

func TestControlledNetworkRejectsHostAndPrivateAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "64:ff9b::7f00:1", "2002:7f00:1::", "fec0::1", "192.0.2.1"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("private address allowed", raw)
		}
	}
	if !publicAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func TestManagedInstallAndRemoveRejectionPreservesFiles(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) { return false, nil })
	c.runInstall = func(context.Context, string, []string, map[string]string) error {
		t.Fatal("installer ran after rejection")
		return nil
	}
	_, err := c.Handle(context.Background(), "workspace_software", jsonArgs(map[string]string{"action": "install", "ecosystem": "python", "package": "fixture==1.0", "id": ""}))
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	if err := c.software.Mkdir(id, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(softwareManifest{ID: id, Ecosystem: "python", Package: "fixture"})
	if err := c.software.WriteFile(id+"/manifest.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.Handle(context.Background(), "workspace_software", jsonArgs(map[string]string{"action": "remove", "id": id}))
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := c.software.Stat(id + "/manifest.json"); err != nil {
		t.Fatal("rejected uninstall removed files", err)
	}
}

func TestManagedInstallAndRemoveAreOwnerReviewedAndActorScoped(t *testing.T) {
	approvals := 0
	c := testControls(t, func(context.Context, string) (bool, error) { approvals++; return true, nil })
	c.runInstall = func(_ context.Context, stage string, command []string, _ map[string]string) error {
		if approvals != 1 || !strings.Contains(strings.Join(command, " "), "--only-binary=:all:") {
			t.Fatal("installer ran before approval or source builds permitted", approvals, command)
		}
		return os.WriteFile(filepath.Join(stage, "packages", "fixture.py"), []byte("fixture"), 0600)
	}
	result, err := c.Handle(context.Background(), "workspace_software", jsonArgs(map[string]string{"action": "install", "ecosystem": "python", "package": "fixture==1.0", "id": ""}))
	if err != nil || approvals != 1 {
		t.Fatal(result, err, approvals)
	}
	entries, err := fsReadDirNames(c.software)
	if err != nil || len(entries) != 1 || !softwareID.MatchString(entries[0]) {
		t.Fatal(entries, err)
	}
	id := entries[0]
	if _, err := c.software.Stat(id + "/packages/fixture.py"); err != nil {
		t.Fatal(err)
	}
	_, err = c.Handle(context.Background(), "workspace_software", jsonArgs(map[string]string{"action": "remove", "ecosystem": "", "package": "", "id": id}))
	if err != nil || approvals != 2 {
		t.Fatal(err, approvals)
	}
	if _, err := c.software.Stat(id); !os.IsNotExist(err) {
		t.Fatal("managed uninstall did not remove its own installation", err)
	}
	if _, err := c.Handle(context.Background(), "workspace_software", jsonArgs(map[string]string{"action": "remove", "id": "../other-member"})); err == nil {
		t.Fatal("managed uninstall escaped actor root")
	}
}

func fsReadDirNames(root *os.Root) ([]string, error) {
	entries, err := os.ReadDir(root.Name())
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, err
}

func TestManagedSoftwareRejectsPackageOptionsURLsAndInstallScripts(t *testing.T) {
	c := testControls(t, func(context.Context, string) (bool, error) {
		t.Fatal("invalid package reached owner approval")
		return true, nil
	})
	for _, pkg := range []string{"--target=C:/", "https://example.test/a.whl", "../package", "foo; rm", "x @ https://example.test/x"} {
		if _, err := c.installInvocation("python", pkg, t.TempDir()); err == nil {
			t.Fatal(pkg)
		}
	}
	command, err := c.installInvocation("node", "@scope/pkg@1.0.0", t.TempDir())
	if err != nil || !strings.Contains(strings.Join(command, " "), "--ignore-scripts") || !strings.Contains(strings.Join(command, " "), "--userconfig") {
		t.Fatal(command, err)
	}
}
