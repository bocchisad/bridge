package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/mell/bridge/internal/daemon"
	"github.com/mell/bridge/internal/ipc"
)

type app struct {
	mu           sync.Mutex
	daemonCancel context.CancelFunc
	seenIncoming map[string]bool
	peerItems    []*systray.MenuItem
	inItems      []*systray.MenuItem

	mStatus   *systray.MenuItem
	mPIN      *systray.MenuItem
	mPeers    *systray.MenuItem
	mIncoming *systray.MenuItem
	mInbox    *systray.MenuItem
	mQuit     *systray.MenuItem
}

func Run() error {
	systray.Run(onReady, func() {})
	return nil
}

func onReady() {
	a := &app{seenIncoming: map[string]bool{}}
	systray.SetTitle("bridge")
	systray.SetTooltip("bridge — starting…")

	a.mStatus = systray.AddMenuItem("Status: starting…", "")
	a.mStatus.Disable()
	a.mPIN = systray.AddMenuItem("Show pairing PIN", "start pairing on this device")
	systray.AddSeparator()
	a.mPeers = systray.AddMenuItem("Send to peer", "choose a peer and file")
	a.mIncoming = systray.AddMenuItem("Incoming", "pending transfers")
	a.mInbox = systray.AddMenuItem("Open inbox", "open downloads folder")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quit", "quit tray (daemon keeps running if external)")

	go a.ensureDaemon()
	go a.refreshLoop()
	go a.events()
}

func (a *app) events() {
	for {
		select {
		case <-a.mPIN.ClickedCh:
			a.showPIN()
		case <-a.mInbox.ClickedCh:
			a.openInbox()
		case <-a.mQuit.ClickedCh:
			a.mu.Lock()
			if a.daemonCancel != nil {
				a.daemonCancel()
			}
			a.mu.Unlock()
			systray.Quit()
			return
		}
	}
}

func (a *app) ensureDaemon() {
	if clientOK() {
		return
	}
	d, err := daemon.New()
	if err != nil {
		notify("bridge", "failed to start daemon: "+err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.daemonCancel = cancel
	a.mu.Unlock()
	go func() {
		_ = d.Run(ctx)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if clientOK() {
			notify("bridge", "daemon started")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (a *app) refreshLoop() {
	for {
		a.refreshOnce()
		time.Sleep(2 * time.Second)
	}
}

func (a *app) refreshOnce() {
	c, err := ipc.New()
	if err != nil || !clientOK() {
		a.setStatus("daemon offline", "bridge: daemon offline")
		systray.SetTitle("bridge ✕")
		return
	}
	st, err := c.Status()
	if err != nil {
		a.setStatus("daemon offline", "bridge: daemon offline")
		systray.SetTitle("bridge ✕")
		return
	}

	peers, _ := c.Peers()
	incoming, _ := c.Incoming()
	online := 0
	for _, p := range peers {
		if p.Online {
			online++
		}
	}

	title := "bridge"
	tip := fmt.Sprintf("bridge — %s", st.DeviceName)
	status := fmt.Sprintf("%s · %d peer(s) · %d online", st.DeviceName, len(peers), online)
	if len(incoming) > 0 {
		title = fmt.Sprintf("bridge (%d)", len(incoming))
		tip = fmt.Sprintf("%d incoming transfer(s)", len(incoming))
		status = fmt.Sprintf("%s · %d incoming!", st.DeviceName, len(incoming))
	}
	systray.SetTitle(title)
	a.setStatus(status, tip)

	a.rebuildPeers(c, peers)
	a.rebuildIncoming(c, incoming)
}

func (a *app) setStatus(line, tip string) {
	a.mStatus.SetTitle(line)
	systray.SetTooltip(tip)
}

func (a *app) rebuildPeers(c *ipc.Client, peers []ipc.PeerResp) {
	for _, it := range a.peerItems {
		it.Hide()
	}
	a.peerItems = nil
	if len(peers) == 0 {
		empty := a.mPeers.AddSubMenuItem("(no peers — pair first)", "")
		empty.Disable()
		a.peerItems = append(a.peerItems, empty)
		return
	}
	for _, p := range peers {
		p := p
		label := p.Name
		if p.Online {
			label += "  ● online"
		} else {
			label += "  ○ offline"
		}
		item := a.mPeers.AddSubMenuItem(label, "send file to "+p.Name)
		a.peerItems = append(a.peerItems, item)
		go func() {
			for range item.ClickedCh {
				path := pickPath()
				if path == "" {
					continue
				}
				notify("bridge", fmt.Sprintf("sending to %s…", p.Name))
				res, err := c.Send(p.Name, path, true)
				if err != nil {
					notify("bridge", "send failed: "+err.Error())
					continue
				}
				if res.Status == "rejected" || res.Status == "failed" {
					notify("bridge", "send "+res.Status+": "+res.Error)
					continue
				}
				notify("bridge", "sent to "+p.Name)
			}
		}()
	}
}

func (a *app) rebuildIncoming(c *ipc.Client, list []ipc.IncomingResp) {
	for _, it := range a.inItems {
		it.Hide()
	}
	a.inItems = nil

	if len(list) == 0 {
		empty := a.mIncoming.AddSubMenuItem("(none)", "")
		empty.Disable()
		a.inItems = append(a.inItems, empty)
		return
	}

	for _, in := range list {
		in := in
		a.mu.Lock()
		if !a.seenIncoming[in.TransferID] {
			a.seenIncoming[in.TransferID] = true
			a.mu.Unlock()
			notify("bridge", fmt.Sprintf("Incoming: %s from %s", in.Name, in.FromName))
		} else {
			a.mu.Unlock()
		}

		label := fmt.Sprintf("%s (%s)", in.Name, humanSize(in.Size))
		accept := a.mIncoming.AddSubMenuItem("Accept — "+label, "from "+in.FromName)
		reject := a.mIncoming.AddSubMenuItem("Reject — "+label, "from "+in.FromName)
		a.inItems = append(a.inItems, accept, reject)

		go func() {
			for range accept.ClickedCh {
				if err := c.Accept(in.TransferID); err != nil {
					notify("bridge", "accept failed: "+err.Error())
					continue
				}
				notify("bridge", "accepted "+in.Name)
			}
		}()
		go func() {
			for range reject.ClickedCh {
				_ = c.Reject(in.TransferID, "rejected from tray")
				notify("bridge", "rejected "+in.Name)
			}
		}()
	}
}

func (a *app) showPIN() {
	if !clientOK() {
		notify("bridge", "daemon offline")
		return
	}
	c, err := ipc.New()
	if err != nil {
		return
	}
	s, err := c.PairStart()
	if err != nil {
		notify("bridge", err.Error())
		return
	}
	notify("bridge", "Pairing PIN: "+s.PIN)
	// also print for copy from terminal if tray launched from there
	fmt.Fprintf(os.Stderr, "bridge pairing PIN: %s (expires %s)\n", s.PIN, s.ExpiresAt)
}

func (a *app) openInbox() {
	dir := defaultInbox()
	if c, err := ipc.New(); err == nil {
		if st, err := c.Status(); err == nil && st.InboxDir != "" {
			dir = st.InboxDir
		}
	}
	_ = os.MkdirAll(dir, 0o755)
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", dir).Start()
	default:
		_ = exec.Command("xdg-open", dir).Start()
	}
}

func defaultInbox() string {
	home, _ := os.UserHomeDir()
	return home + "/Downloads/bridge"
}

func clientOK() bool {
	c, err := ipc.New()
	if err != nil {
		return false
	}
	_, err = c.Status()
	return err == nil
}

func pickPath() string {
	switch runtime.GOOS {
	case "darwin":
		script := `set f to choose file with prompt "Send with bridge"
POSIX path of f`
		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			script = `set f to choose folder with prompt "Send folder with bridge"
POSIX path of f`
			out, err = exec.Command("osascript", "-e", script).Output()
			if err != nil {
				return ""
			}
		}
		return trimNL(string(out))
	default:
		path, how := pickPathLinux()
		if path == "" {
			notify("bridge", "File picker unavailable. Install: sudo pacman -S zenity")
			fmt.Fprintln(os.Stderr, "bridge: no file picker (tried zenity/kdialog/yad/wofi/rofi/fuzzel)")
			return ""
		}
		fmt.Fprintf(os.Stderr, "bridge: picked via %s: %s\n", how, path)
		return path
	}
}

func pickPathLinux() (string, string) {
	type attempt struct {
		name string
		args []string
	}
	// Prefer real GUI dialogs, then Hyprland launchers.
	attempts := []attempt{
		{"zenity", []string{"zenity", "--file-selection", "--title=Send with bridge"}},
		{"zenity-dir", []string{"zenity", "--file-selection", "--directory", "--title=Send folder with bridge"}},
		{"kdialog", []string{"kdialog", "--getopenfilename", "."}},
		{"kdialog-dir", []string{"kdialog", "--getexistingdirectory", "."}},
		{"yad", []string{"yad", "--file-selection", "--title=Send with bridge"}},
	}
	for _, a := range attempts {
		if _, err := exec.LookPath(a.args[0]); err != nil {
			continue
		}
		out, err := exec.Command(a.args[0], a.args[1:]...).Output()
		if err != nil {
			continue
		}
		if p := trimNL(string(out)); p != "" {
			return p, a.name
		}
	}
	if p := pickWithLauncher(); p != "" {
		return p, "launcher"
	}
	return "", ""
}

// pickWithLauncher lists files under common dirs via wofi/rofi/fuzzel.
func pickWithLauncher() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	roots := []string{
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "DATA"),
		home,
	}
	var existing []string
	for _, r := range roots {
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			existing = append(existing, r)
		}
	}
	if len(existing) == 0 {
		return ""
	}

	findCmd := "find"
	for _, r := range existing {
		findCmd += " " + shellQuote(r)
	}
	findCmd += ` -type f ! -path '*/.*' 2>/dev/null | head -n 4000`

	launchers := [][]string{
		{"wofi", "--dmenu", "-i", "-p", "bridge: send file"},
		{"rofi", "-dmenu", "-i", "-p", "bridge: send file"},
		{"fuzzel", "--dmenu", "-p", "bridge: send file"},
	}
	for _, launcher := range launchers {
		if _, err := exec.LookPath(launcher[0]); err != nil {
			continue
		}
		cmd := exec.Command("bash", "-c", findCmd+" | "+shellJoin(launcher))
		cmd.Env = os.Environ()
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if p := trimNL(string(out)); p != "" {
			return p
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func notify(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		_ = exec.Command("osascript", "-e", script).Start()
	default:
		_ = exec.Command("notify-send", title, body).Start()
	}
}

func humanSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
