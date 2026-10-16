package tray

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/systray"
	"github.com/mell/bridge/internal/ipc"
)

func Run() error {
	systray.Run(onReady, onExit)
	return nil
}

func onExit() {}

func onReady() {
	systray.SetTitle("bridge")
	systray.SetTooltip("bridge file transfer")

	mStatus := systray.AddMenuItem("Status: connecting…", "daemon status")
	mStatus.Disable()
	systray.AddSeparator()
	mPeers := systray.AddMenuItem("Peers", "paired peers")
	mIncoming := systray.AddMenuItem("Incoming", "pending transfers")
	mInbox := systray.AddMenuItem("Open inbox", "open inbox folder")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "quit tray")

	go refreshLoop(mStatus, mPeers, mIncoming)

	go func() {
		for {
			select {
			case <-mInbox.ClickedCh:
				openInbox()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			case <-mPeers.ClickedCh:
				// submenu rebuilt in refresh; click opens nothing extra
			case <-mIncoming.ClickedCh:
			}
		}
	}()
}

func refreshLoop(mStatus, mPeers, mIncoming *systray.MenuItem) {
	var peerItems []*systray.MenuItem
	var incomingItems []*systray.MenuItem

	for {
		c, err := ipc.New()
		if err != nil {
			mStatus.SetTitle("Status: error")
			time.Sleep(3 * time.Second)
			continue
		}
		st, err := c.Status()
		if err != nil {
			mStatus.SetTitle("Status: daemon offline")
			systray.SetTooltip("bridge: daemon offline — run bridge daemon")
		} else {
			mStatus.SetTitle(fmt.Sprintf("Status: %s", st.DeviceName))
			systray.SetTooltip(fmt.Sprintf("bridge: %s", st.DeviceName))
		}

		for _, it := range peerItems {
			it.Hide()
		}
		peerItems = nil
		peers, err := c.Peers()
		if err == nil {
			for _, p := range peers {
				p := p
				label := p.Name
				if p.Online {
					label += " • online"
				} else {
					label += " • offline"
				}
				item := mPeers.AddSubMenuItem(label, p.DeviceID)
				peerItems = append(peerItems, item)
				go func() {
					for range item.ClickedCh {
						path := pickFile()
						if path == "" {
							continue
						}
						_, _ = c.Send(p.Name, path, true)
					}
				}()
			}
		}

		for _, it := range incomingItems {
			it.Hide()
		}
		incomingItems = nil
		incoming, err := c.Incoming()
		if err == nil {
			for _, in := range incoming {
				in := in
				label := fmt.Sprintf("%s from %s", in.Name, in.FromName)
				item := mIncoming.AddSubMenuItem(label, "click to accept")
				reject := mIncoming.AddSubMenuItem("  └ reject "+in.Name, "reject")
				incomingItems = append(incomingItems, item, reject)
				go func() {
					for range item.ClickedCh {
						_ = c.Accept(in.TransferID)
					}
				}()
				go func() {
					for range reject.ClickedCh {
						_ = c.Reject(in.TransferID, "rejected from tray")
					}
				}()
			}
		}

		time.Sleep(2 * time.Second)
	}
}

func openInbox() {
	c, err := ipc.New()
	if err != nil {
		return
	}
	st, err := c.Status()
	if err != nil {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", st.InboxDir).Start()
	default:
		_ = exec.Command("xdg-open", st.InboxDir).Start()
	}
}

func pickFile() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e", `POSIX path of (choose file)`).Output()
		if err != nil {
			return ""
		}
		return trimNL(string(out))
	default:
		out, err := exec.Command("zenity", "--file-selection").Output()
		if err != nil {
			// fallback: empty — user can use CLI
			return ""
		}
		return trimNL(string(out))
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
