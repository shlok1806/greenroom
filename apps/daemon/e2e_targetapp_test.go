//go:build tart

// Which app machine_ui reads, on a real guest (issues #223 and #209): an accessory (LSUIElement)
// app with a window is read by name and by bundle id, a click by element lands on its button, and
// with no app named an accessory process holding accessibility focus does not hide the regular app
// in front. Runs once on the legacy tools and once through the guest agent (-desktop-toolkit),
// where machine_snapshot and machine_press read and press the same app. Boots one VM per subtest.
// Needs a local image built by scripts/build-image.sh (GREENROOM_BASE_IMAGE, default
// greenroom-base). Run with:
//
//	go test -tags tart -run TestEndToEndAccessoryApps -v -timeout 25m .
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shlok1806/greenroom/apps/daemon/internal/machine"
	"github.com/shlok1806/greenroom/apps/daemon/internal/mcpserver"
	greenroomsession "github.com/shlok1806/greenroom/apps/daemon/internal/session"
)

// accessoryFixture is the fixture's source. One binary, two bundles: AccessoryFixture shows a
// window with a "Press Me" button and activates itself, as a menu bar app's settings window
// does; FocusThief shows a non-activating panel and makes its text field first responder, so it
// takes keyboard (and accessibility) focus while another app stays active, as a launcher does.
const accessoryFixture = `import AppKit

final class Fixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!

    @objc func press(_ sender: Any?) {
        FileManager.default.createFile(atPath: "/tmp/greenroom-accessory-pressed", contents: Data("pressed\n".utf8))
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        let rect = NSRect(x: 300, y: 300, width: 360, height: 180)
        if Bundle.main.bundleIdentifier?.hasSuffix("focusthief") == true {
            let panel = NSPanel(contentRect: rect, styleMask: [.titled, .nonactivatingPanel], backing: .buffered, defer: false)
            panel.title = "Focus Thief"
            panel.becomesKeyOnlyIfNeeded = false
            panel.level = .floating
            let field = NSTextField(frame: NSRect(x: 20, y: 70, width: 320, height: 24))
            panel.contentView?.addSubview(field)
            panel.makeKeyAndOrderFront(nil)
            panel.makeFirstResponder(field)
            window = panel
            return
        }
        window = NSWindow(contentRect: rect, styleMask: [.titled], backing: .buffered, defer: false)
        window.title = "Accessory Fixture"
        let button = NSButton(title: "Press Me", target: self, action: #selector(press(_:)))
        button.frame = NSRect(x: 120, y: 70, width: 120, height: 32)
        window.contentView?.addSubview(button)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
}

let delegate = Fixture()
NSApplication.shared.delegate = delegate
NSApplication.shared.run()
`

// focusProbe prints who holds accessibility focus, who owns the menu bar and who is frontmost,
// each as "name policy" (0 regular, 1 accessory, 2 prohibited), so a failure shows the scene.
const focusProbe = `import AppKit
import ApplicationServices

func describe(_ app: NSRunningApplication?) -> String {
    guard let app else { return "none" }
    return "\(app.localizedName ?? "?") \(app.activationPolicy.rawValue)"
}

// The pid behind a system-wide attribute, and the AX error when there is none.
func focusPid(_ attribute: String) -> String {
    var value: CFTypeRef?
    let err = AXUIElementCopyAttributeValue(AXUIElementCreateSystemWide(), attribute as CFString, &value)
    guard err == .success, let value, CFGetTypeID(value) == AXUIElementGetTypeID() else { return "error \(err.rawValue)" }
    var pid: pid_t = 0
    guard AXUIElementGetPid(value as! AXUIElement, &pid) == .success else { return "no pid" }
    var name = [CChar](repeating: 0, count: 256)
    proc_name(pid, &name, UInt32(name.count))
    let app = NSRunningApplication(processIdentifier: pid)
    return app.map(describe) ?? "pid \(pid) \(String(cString: name)) (not an application)"
}

print("trusted=\(AXIsProcessTrusted())")
print("focused=\(focusPid(kAXFocusedApplicationAttribute))")
print("focusedElement=\(focusPid(kAXFocusedUIElementAttribute))")
print("menubar=\(describe(NSWorkspace.shared.menuBarOwningApplication))")
print("frontmost=\(describe(NSWorkspace.shared.frontmostApplication))")
`

// buildAccessoryFixture builds both bundles and the probe under ~/work/AccessoryFixture.
const buildAccessoryFixture = `set -e
d="$HOME/work/AccessoryFixture" && rm -rf "$d" && mkdir -p "$d"
cat > "$d/main.swift" <<'SWIFT'
` + accessoryFixture + `SWIFT
cat > "$d/probe.swift" <<'SWIFT'
` + focusProbe + `SWIFT
swiftc -O -o "$d/fixture" "$d/main.swift"
swiftc -O -o "$d/probe" "$d/probe.swift"
bundle() {
	mkdir -p "$d/$1.app/Contents/MacOS"
	cp "$d/fixture" "$d/$1.app/Contents/MacOS/$1"
	cat > "$d/$1.app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>$2</string>
	<key>CFBundleExecutable</key><string>$1</string>
	<key>CFBundleName</key><string>$1</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSUIElement</key><true/>
</dict>
</plist>
PLIST
}
bundle AccessoryFixture com.greenroom.e2e.accessoryfixture
bundle FocusThief com.greenroom.e2e.focusthief
echo built`

const (
	accessoryBundleID = "com.greenroom.e2e.accessoryfixture"
	pressedProof      = "/tmp/greenroom-accessory-pressed"
)

func TestEndToEndAccessoryApps(t *testing.T) {
	t.Run("legacy", func(t *testing.T) { accessoryAppChecks(t, false) })
	t.Run("toolkit", func(t *testing.T) { accessoryAppChecks(t, true) })
}

func accessoryAppChecks(t *testing.T, toolkit bool) {
	waitForAFreeSlot(t)
	root := t.TempDir()
	mgr, err := machine.NewManager(root, slog.New(slog.NewTextHandler(os.Stderr, nil)), machine.WithDesktopToolkit(toolkit))
	if err != nil {
		t.Fatal(err)
	}
	reg := greenroomsession.NewRegistry(root, greenroomsession.DefaultMaxDisputes)
	server := mcpserver.New(mgr, greenroomBaseImage(), reg)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	// tool calls name and returns its text and whether it was a tool error; a transport error is fatal.
	tool := func(name string, args map[string]any, out any) (string, bool) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != nil && !res.IsError {
			data, _ := json.Marshal(res.StructuredContent)
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("%s: decode structured content: %v", name, err)
			}
		}
		return contentText(res), res.IsError
	}

	var created machine.Machine
	if text, failed := tool("machine_create", nil, &created); failed {
		t.Fatalf("machine_create: %s", text)
	}
	runID := created.RunID
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer dcancel()
		if err := mgr.Destroy(dctx, runID); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	}()
	mc, err := mgr.Wait(ctx, runID, 6*time.Minute)
	if err != nil || mc.Status != machine.Ready {
		t.Fatalf("machine not ready: %+v %v", mc, err)
	}
	t.Logf("machine %s booted in %.1fs (toolkit %v)", runID, mc.BootSeconds, toolkit)

	exec := func(command string, timeout time.Duration) string {
		t.Helper()
		res, err := mgr.Exec(ctx, runID, command, "", timeout)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%q: %v exit %d\n%s\n%s", command, err, res.ExitCode, res.Stdout, res.Stderr)
		}
		return res.Stdout
	}
	exec(buildAccessoryFixture, 4*time.Minute)
	probe := func(when string) map[string]string {
		t.Helper()
		out := exec(`"$HOME/work/AccessoryFixture/probe"`, time.Minute)
		t.Logf("focus %s:\n%s", when, out)
		got := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				got[k] = v
			}
		}
		return got
	}

	// #223: an LSUIElement app with a window, by name and by bundle id.
	exec(`open "$HOME/work/AccessoryFixture/AccessoryFixture.app" && sleep 3 && rm -f `+pressedProof+
		` && lsappinfo info -only type -app `+accessoryBundleID, time.Minute)
	// #209: with no app named, a non-regular process in front is passed over for the app that owns
	// the menu bar. Here the accessory app's window is in front and Finder keeps the menu bar.
	front := probe("with AccessoryFixture launched")
	var tree machine.UITree
	noApp := func(when string) {
		t.Helper()
		text, failed := tool("machine_ui", map[string]any{"runId": runID}, &tree)
		t.Logf("machine_ui with no app, %s, read %q (failed %v)", when, tree.App, failed)
		if failed || tree.App != "Finder" {
			t.Errorf("machine_ui with no app, %s, read %q, want Finder, the regular app that owns the menu bar: %s", when, tree.App, text)
		}
		if toolkit {
			var snap machine.DesktopSnapshot
			if text, failed := tool("machine_snapshot", map[string]any{"runId": runID}, &snap); failed {
				t.Errorf("machine_snapshot with no app, %s: %s", when, text)
			} else if snap.App.Name != "Finder" || snap.Frontmost.Name != "Finder" {
				t.Errorf("machine_snapshot with no app, %s, read %q, frontmost %q, want Finder for both", when, snap.App.Name, snap.Frontmost.Name)
			}
		}
	}
	if !strings.HasPrefix(front["frontmost"], "AccessoryFixture ") || !strings.HasPrefix(front["menubar"], "Finder ") {
		t.Errorf("the scene did not set up: frontmost %q, menu bar %q (want AccessoryFixture and Finder)", front["frontmost"], front["menubar"])
	}
	noApp("with an accessory app in front")
	for _, name := range []string{"AccessoryFixture", accessoryBundleID} {
		text, failed := tool("machine_ui", map[string]any{"runId": runID, "app": name}, &tree)
		if failed {
			t.Errorf("machine_ui {app: %q}: %s", name, text)
			continue
		}
		if tree.App != "AccessoryFixture" {
			t.Errorf("machine_ui {app: %q} read %q, want AccessoryFixture", name, tree.App)
		}
		t.Logf("machine_ui {app: %q} read %s with %d elements", name, tree.App, len(tree.Elements))
	}
	text, failed := tool("machine_ui", map[string]any{"runId": runID, "app": "NoSuchApp"}, nil)
	t.Logf("machine_ui {app: NoSuchApp}: %s", text)
	if !failed || !strings.Contains(text, "AccessoryFixture (accessory)") {
		t.Errorf("an unknown name's error does not list the accessory app as one: %s", text)
	}

	// The click by element, through the tree that named the accessory app.
	button := 0
	if tree.App == "AccessoryFixture" {
		for _, e := range tree.Elements {
			if strings.TrimPrefix(e.Role, "AX") == "Button" && e.Title == "Press Me" {
				button = e.ID
			}
		}
		if button == 0 {
			t.Errorf("the tree of AccessoryFixture has no Press Me button: %s", tree.Outline())
		}
	}
	if button != 0 {
		exec(`open -a Finder && sleep 2`, time.Minute) // the click has to bring the fixture back
		text, failed := tool("machine_click", map[string]any{"runId": runID, "element": button}, nil)
		if failed {
			t.Errorf("machine_click {element: %d}: %s", button, text)
		}
		waitForProof(t, mgr, ctx, runID, "machine_click {element}")
	}

	if toolkit {
		// The toolkit's own read and press go through the same lookup (AXEngine.appTarget).
		var snap machine.DesktopSnapshot
		text, failed := tool("machine_snapshot", map[string]any{"runId": runID, "app": accessoryBundleID}, &snap)
		if failed {
			t.Errorf("machine_snapshot {app: %q}: %s", accessoryBundleID, text)
		} else {
			ref := ""
			for _, n := range snap.Nodes {
				if strings.TrimPrefix(n.Role, "AX") == "Button" && n.Name == "Press Me" {
					ref = n.Ref
				}
			}
			t.Logf("machine_snapshot read %q, Press Me is %q", snap.App.Name, ref)
			if snap.App.Name != "AccessoryFixture" || ref == "" {
				t.Errorf("machine_snapshot {app: %q} read %q with no Press Me ref:\n%s", accessoryBundleID, snap.App.Name, text)
			} else {
				exec(`rm -f `+pressedProof+` && open -a Finder && sleep 2`, time.Minute)
				if text, failed := tool("machine_press", map[string]any{"runId": runID, "ref": ref}, nil); failed {
					t.Errorf("machine_press {ref: %q}: %s", ref, text)
				}
				waitForProof(t, mgr, ctx, runID, "machine_press {ref}")
			}
		}
	}

	// #209: an accessory process holds accessibility focus while Finder is in front.
	exec(`osascript -e 'tell application "AccessoryFixture" to quit' || pkill -x AccessoryFixture; sleep 1; `+
		`open -a Finder && sleep 2 && open "$HOME/work/AccessoryFixture/FocusThief.app" && sleep 3`, time.Minute)
	// AX focus is what #209 saw go to AccessibilityUIServer. On greenroom-base-v10-r4 a process
	// started through tart exec gets kAXErrorCannotComplete (-25204) for the system-wide focus, so
	// the probe may not show the panel holding it; the read must name Finder either way.
	probe("with FocusThief's panel key")
	noApp("with an accessory panel key")
}

// waitForProof waits for the fixture's button to have written its proof file.
func waitForProof(t *testing.T, mgr *machine.Manager, ctx context.Context, runID, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := mgr.Exec(ctx, runID, "cat "+pressedProof, "", 15*time.Second)
		if err == nil && res.ExitCode == 0 && strings.Contains(res.Stdout, "pressed") {
			t.Logf("%s landed on the fixture's Press Me button", what)
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s did not press the fixture's button within 10 s", what)
			return
		}
		time.Sleep(time.Second)
	}
}
